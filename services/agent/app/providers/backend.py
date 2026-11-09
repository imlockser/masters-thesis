import hashlib
import hmac

import httpx
from pydantic import BaseModel, PositiveInt


class MemoryConflictError(RuntimeError):
    pass


class SavedMemory(BaseModel):
    user_id: str
    version: PositiveInt


class BackendProvider:
    def __init__(self) -> None:
        self._client: httpx.AsyncClient | None = None
        self._url = ""
        self._callback_key: bytes | None = None

    def setup(self, url: str, callback_key: str, timeout: float = 30.0) -> None:
        if self._client is not None:
            raise RuntimeError("BackendProvider is already initialized.")

        key = bytes.fromhex(callback_key)
        if len(key) != 32:
            raise ValueError(
                "WORKER_CALLBACK_KEY must contain 64 hexadecimal characters."
            )
        self._callback_key = key
        self._url = str(
            httpx.URL(url).copy_with(
                path="/internal/agent/memory", query=None, fragment=None
            )
        )
        self._client = httpx.AsyncClient(timeout=timeout)

    async def save_user_memory(
        self,
        user_id: str,
        summary: str,
        expected_version: int,
        *,
        run_id: str,
        grant_nonce: str,
    ) -> int:
        if self._client is None or self._callback_key is None:
            raise RuntimeError(
                "BackendProvider is not initialized. Call setup() first."
            )

        secret = hmac.new(
            self._callback_key, f"{run_id}:{grant_nonce}".encode(), hashlib.sha256
        ).hexdigest()
        response = await self._client.post(
            self._url,
            headers={"Authorization": f"Bearer {secret}"},
            json={
                "run_id": run_id,
                "summary": summary,
                "expected_version": expected_version,
            },
        )
        if response.status_code == 409:
            raise MemoryConflictError("User memory was changed by another request.")
        response.raise_for_status()

        memory = SavedMemory.model_validate(response.json())
        if memory.user_id != user_id or memory.version != expected_version + 1:
            raise RuntimeError("SaveUserMemory returned an unexpected user or version.")

        return memory.version

    async def close(self) -> None:
        if self._client is not None:
            await self._client.aclose()
            self._client = None


backend_provider = BackendProvider()
