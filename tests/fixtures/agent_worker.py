"""Deterministic agent worker for Go/Python integration tests."""

import asyncio
import os
import signal
from unittest.mock import patch
from uuid import uuid4

from dbos import DBOS
from fastmcp.client.client import CallToolResult
from mcp.types import TextContent, Tool
from openai.types.chat import ChatCompletionMessage

from services.agent.app import workflow
from services.agent.app.providers.backend import backend_provider
from services.agent.app.providers.llm import llm_provider


async def main() -> None:
    released = asyncio.Event()
    stopped = asyncio.Event()
    loop = asyncio.get_running_loop()
    loop.add_signal_handler(signal.SIGUSR1, released.set)
    loop.add_signal_handler(signal.SIGUSR2, released.clear)
    loop.add_signal_handler(signal.SIGTERM, stopped.set)

    async def complete(messages, *, tools, on_text):
        if messages[-1]["role"] == "tool":
            answer = (
                "The forecast solar energy is **42 MWh**.\n\n"
                "| Hour (UTC) | Energy |\n| --- | ---: |\n| 16:00–17:00 | 20 MWh |\n| 17:00–18:00 | 22 MWh |"
                if messages[-1]["content"].startswith("Total forecast energy:")
                else "Done."
            )
            await on_text(answer)
            return ChatCompletionMessage(role="assistant", content=answer)

        await on_text("Working ")
        await released.wait()
        if messages[-1]["content"] == "fail":
            raise ConnectionError("Simulated provider failure")

        remember = messages[-1]["content"] == "remember"
        name = "remember" if remember else "forecast_solar_generation"
        arguments = (
            '{"summary":"Prefers solar forecasts and UTC times."}'
            if remember
            else "{}"
        )
        return ChatCompletionMessage.model_validate(
            {
                "role": "assistant",
                "content": "Working ",
                "tool_calls": [
                    {
                        "id": f"call-{uuid4().hex}",
                        "type": "function",
                        "function": {"name": name, "arguments": arguments},
                    }
                ],
            }
        )

    async def list_tools():
        return [
            Tool(
                name="forecast_solar_generation",
                description="Test forecast",
                input_schema={"type": "object", "properties": {}},
            )
        ]

    async def call_tool(name, arguments):
        return CallToolResult(
            content=[TextContent(type="text", text="Total forecast energy: 42 MWh.")],
            structured_content={"total_energy_mwh": 42},
            meta=None,
        )

    backend_provider.setup(os.environ["TEST_BACKEND_RPC_URL"])
    DBOS(
        config={
            "name": "energy-agent",
            "system_database_url": os.environ["TEST_DBOS_DATABASE_URL"],
            "use_listen_notify": True,
            "enable_otlp": False,
        }
    )
    with (
        patch.object(workflow.mcp_provider, "list_tools", list_tools),
        patch.object(workflow.mcp_provider, "call_tool", call_tool),
        patch.object(llm_provider, "tool_call_stream", complete),
    ):
        try:
            DBOS.listen_queues([workflow.QUEUE_NAME])
            DBOS.launch()
            await DBOS.register_queue_async(workflow.QUEUE_NAME, worker_concurrency=10)
            print("READY", flush=True)
            await stopped.wait()
        finally:
            released.set()
            await asyncio.to_thread(DBOS.destroy, workflow_completion_timeout_sec=5)
            await backend_provider.close()


if __name__ == "__main__":
    asyncio.run(main())
