import asyncio
import json

import httpx
import pytest

from services.agent.app.providers.backend import BackendProvider, MemoryConflictError

RUN_ID = "00000000-0000-0000-0000-000000000002"
NONCE = "22" * 32


@pytest.mark.parametrize("conflict", [False, True])
def test_save_memory_contract(conflict):
    requests = []

    def respond(request):
        assert request.url.path == "/internal/agent/memory"
        assert not request.url.query
        secret = request.headers["Authorization"].removeprefix("Bearer ")
        assert len(secret) == 64
        assert secret not in request.content.decode()
        requests.append(json.loads(request.content))
        if conflict:
            return httpx.Response(409)
        else:
            body = {"user_id": "user-1", "version": 3}

        return httpx.Response(200, json=body)

    async def run():
        provider = BackendProvider()
        provider._callback_key = bytes.fromhex("11" * 32)
        provider._url = "http://backend/internal/agent/memory"
        provider._client = httpx.AsyncClient(
            base_url="http://backend/internal/agent/memory",
            transport=httpx.MockTransport(respond),
        )

        try:
            if conflict:
                with pytest.raises(MemoryConflictError):
                    await provider.save_user_memory(
                        "user-1", "New summary", 2, run_id=RUN_ID, grant_nonce=NONCE
                    )
            else:
                assert (
                    await provider.save_user_memory(
                        "user-1", "New summary", 2, run_id=RUN_ID, grant_nonce=NONCE
                    )
                    == 3
                )
        finally:
            await provider.close()

    asyncio.run(run())
    assert requests == [
        {"run_id": RUN_ID, "summary": "New summary", "expected_version": 2}
    ]


def test_wrong_memory_response_is_rejected():
    async def run():
        provider = BackendProvider()
        provider._callback_key = bytes.fromhex("11" * 32)
        provider._url = "http://backend/internal/agent/memory"
        provider._client = httpx.AsyncClient(
            base_url="http://backend/internal/agent/memory",
            transport=httpx.MockTransport(
                lambda _: httpx.Response(
                    200,
                    json={
                        "user_id": "another-user",
                        "version": 1,
                    },
                )
            ),
        )

        try:
            with pytest.raises(RuntimeError, match="unexpected user or version"):
                await provider.save_user_memory(
                    "user-1", "Summary", 0, run_id=RUN_ID, grant_nonce=NONCE
                )
        finally:
            await provider.close()

    asyncio.run(run())
