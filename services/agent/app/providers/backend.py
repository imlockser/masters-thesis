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

    def setup(self, url: str, timeout: float = 30.0) -> None:
        if self._client is not None:
            raise RuntimeError("BackendProvider is already initialized.")

        self._url = url
        self._client = httpx.AsyncClient(timeout=timeout)

    async def save_user_memory(
        self, user_id: str, summary: str, expected_version: int
    ) -> int:
        if self._client is None:
            raise RuntimeError(
                "BackendProvider is not initialized. Call setup() first."
            )

        response = await self._client.post(
            self._url,
            json={
                "jsonrpc": "2.0",
                "id": 1,
                "method": "SaveUserMemory",
                "params": {
                    "user_id": user_id,
                    "summary": summary,
                    "expected_version": expected_version,
                },
            },
        )
        response.raise_for_status()

        payload = response.json()
        if payload.get("jsonrpc") != "2.0" or payload.get("id") != 1:
            raise RuntimeError("Invalid backend JSON-RPC response.")
        if error := payload.get("error"):
            if error.get("code") == -32001:
                raise MemoryConflictError("User memory was changed by another request.")

            raise RuntimeError("SaveUserMemory failed.")

        memory = SavedMemory.model_validate(payload.get("result"))
        if memory.user_id != user_id or memory.version != expected_version + 1:
            raise RuntimeError("SaveUserMemory returned an unexpected user or version.")

        return memory.version

    async def close(self) -> None:
        if self._client is not None:
            await self._client.aclose()
            self._client = None


backend_provider = BackendProvider()
