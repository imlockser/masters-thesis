import asyncio
import json

import httpx
import pytest

from services.agent.app.providers.backend import BackendProvider, MemoryConflictError


@pytest.mark.parametrize("conflict", [False, True])
def test_save_memory_contract(conflict):
    requests = []

    def respond(request):
        assert request.url.path == "/rpc"
        requests.append(json.loads(request.content))
        body = {"jsonrpc": "2.0", "id": 1}
        if conflict:
            body["error"] = {"code": -32001, "message": "Version conflict"}
        else:
            body["result"] = {"user_id": "user-1", "version": 3}

        return httpx.Response(200, json=body)

    async def run():
        provider = BackendProvider()
        provider._url = "http://backend/rpc"
        provider._client = httpx.AsyncClient(
            base_url="http://backend/rpc", transport=httpx.MockTransport(respond)
        )

        try:
            if conflict:
                with pytest.raises(MemoryConflictError):
                    await provider.save_user_memory("user-1", "New summary", 2)
            else:
                assert await provider.save_user_memory("user-1", "New summary", 2) == 3
        finally:
            await provider.close()

    asyncio.run(run())
    assert requests == [
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "SaveUserMemory",
            "params": {
                "user_id": "user-1",
                "summary": "New summary",
                "expected_version": 2,
            },
        }
    ]


def test_wrong_memory_response_is_rejected():
    async def run():
        provider = BackendProvider()
        provider._url = "http://backend/rpc"
        provider._client = httpx.AsyncClient(
            base_url="http://backend/rpc",
            transport=httpx.MockTransport(
                lambda _: httpx.Response(
                    200,
                    json={
                        "jsonrpc": "2.0",
                        "id": 1,
                        "result": {"user_id": "another-user", "version": 1},
                    },
                )
            ),
        )

        try:
            with pytest.raises(RuntimeError, match="unexpected user or version"):
                await provider.save_user_memory("user-1", "Summary", 0)
        finally:
            await provider.close()

    asyncio.run(run())
