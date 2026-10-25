from typing import Any

from fastmcp import Client
from fastmcp.client.client import CallToolResult
from fastmcp.client.transports import StreamableHttpTransport
from mcp.types import Tool


class MCPProvider:
    def __init__(self) -> None:
        self._client: Client[StreamableHttpTransport] | None = None

    def setup(self, url: str, timeout: float = 180.0) -> None:
        if self._client is not None:
            raise RuntimeError("MCPProvider is already initialized.")

        self._client = Client(StreamableHttpTransport(url), timeout=timeout)

    @property
    def client(self) -> Client[StreamableHttpTransport]:
        if self._client is None:
            raise RuntimeError("MCPProvider is not initialized. Call setup() first.")

        return self._client

    async def list_tools(self) -> list[Tool]:
        async with self.client as client:
            return await client.list_tools()

    async def call_tool(
        self, name: str, arguments: dict[str, Any] | None = None
    ) -> CallToolResult:
        async with self.client as client:
            return await client.call_tool(name, arguments, raise_on_error=False)

    async def close(self) -> None:
        if self._client is not None:
            await self._client.close()
            self._client = None


mcp_provider = MCPProvider()
