import asyncio
import os
from uuid import uuid4

import psycopg
import pytest
from dbos import DBOS, DBOSClient, PortableWorkflowError
from openai.types.chat import ChatCompletionMessage
from psycopg import sql
from sqlalchemy import make_url

from services.agent.app import workflow
from services.agent.app.providers.llm import llm_provider


def test_queue_delivers_live_events_and_runs_ten_requests_concurrently(monkeypatch):
    database_url = os.getenv("TEST_DBOS_DATABASE_URL")
    if not database_url:
        pytest.skip("Set TEST_DBOS_DATABASE_URL to enable disposable PostgreSQL tests.")
    database_name = f"agent_test_{uuid4().hex}"
    url = make_url(database_url).set(drivername="postgresql")
    admin_url = url.set(database="postgres").render_as_string(hide_password=False)

    with psycopg.connect(admin_url, autocommit=True) as admin:
        admin.execute(
            sql.SQL("CREATE DATABASE {}").format(sql.Identifier(database_name))
        )

    runtime_url = url.set(database=database_name).render_as_string(hide_password=False)

    async def run():
        ready = asyncio.Event()
        release = asyncio.Event()
        active = 0

        async def complete(messages, *, tools, on_text):
            nonlocal active
            active += 1
            await on_text("Live ")
            if active == 10:
                ready.set()
            await release.wait()
            if messages[-1]["content"] == "fail":
                raise ConnectionError("Simulated failure")

            await on_text("answer.")

            return ChatCompletionMessage(role="assistant", content="Live answer.")

        async def list_tools():
            return []

        monkeypatch.setattr(llm_provider, "tool_call_stream", complete)
        monkeypatch.setattr(workflow.mcp_provider, "list_tools", list_tools)
        DBOS(
            config={
                "name": "energy-agent-test",
                "system_database_url": runtime_url,
                "use_listen_notify": True,
                "enable_otlp": False,
            }
        )
        client = None

        try:
            DBOS.listen_queues([workflow.QUEUE_NAME])
            DBOS.launch()
            await DBOS.register_queue_async(workflow.QUEUE_NAME, worker_concurrency=10)
            client = DBOSClient(
                system_database_url=runtime_url,
                application_name="energy-agent-test",
                use_listen_notify=True,
            )
            handles = [
                await client.enqueue_async(
                    {
                        "workflow_name": workflow.WORKFLOW_NAME,
                        "queue_name": workflow.QUEUE_NAME,
                        "serialization_type": workflow.PORTABLE,
                    },
                    {
                        "user_id": f"user-{index}",
                        "chat_id": str(uuid4()),
                        "messages": [
                            {
                                "role": "user",
                                "content": "fail" if index == 9 else "hello",
                            }
                        ],
                        "memory_version": 0,
                    },
                )
                for index in range(10)
            ]
            await asyncio.wait_for(ready.wait(), timeout=30)
            stream = client.read_stream_async(
                handles[0].workflow_id, workflow.STREAM_KEY, timeout_seconds=10
            )
            events = []

            try:
                while not events or events[-1]["type"] != "text_delta":
                    events.append(await anext(stream))

                assert events[-1]["text"] == "Live "
                assert not release.is_set()
                assert (await handles[0].get_status()).status == "PENDING"
                release.set()
                events.extend([event async for event in stream])
            finally:
                await stream.aclose()

            assert events[-1]["type"] == "run_completed"
            assert await handles[0].get_result() == events[-1]["result"]
            assert events[-1]["result"]["messages"] == [
                {"role": "assistant", "content": "Live answer."}
            ]

            for handle in handles[1:9]:
                assert (await handle.get_result())["messages"][-1][
                    "content"
                ] == "Live answer."

            failed_events = [
                event
                async for event in client.read_stream_async(
                    handles[9].workflow_id,
                    workflow.STREAM_KEY,
                    timeout_seconds=10,
                )
            ]
            assert failed_events[-1]["type"] == "run_failed"

            with pytest.raises(PortableWorkflowError, match="Simulated failure"):
                await handles[9].get_result()

            assert (await handles[9].get_status()).status == "ERROR"
            assert active == 10
        finally:
            release.set()
            if client:
                await asyncio.to_thread(client.destroy)
            await asyncio.to_thread(DBOS.destroy, workflow_completion_timeout_sec=5)

    try:
        asyncio.run(run())
    finally:
        with psycopg.connect(admin_url, autocommit=True) as admin:
            admin.execute(
                sql.SQL("DROP DATABASE {} WITH (FORCE)").format(
                    sql.Identifier(database_name)
                )
            )
