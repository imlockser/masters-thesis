import asyncio
from unittest.mock import AsyncMock, MagicMock

import pytest
from fastmcp import Client, FastMCP
from fastmcp.exceptions import ToolError

from services.agent.app.providers import mcp as provider_module
from services.agent.app.providers.mcp import MCPProvider


def test_setup_and_lifecycle(monkeypatch):
    provider = MCPProvider()

    with pytest.raises(RuntimeError, match="not initialized"):
        _ = provider.client

    client = MagicMock()
    client.close = AsyncMock()
    factory = MagicMock(return_value=client)
    monkeypatch.setattr(provider_module, "Client", factory)
    provider.setup("http://localhost:8000/mcp", timeout=10)
    assert provider.client is client
    assert factory.call_args.args[0].url == "http://localhost:8000/mcp"
    assert factory.call_args.kwargs["timeout"] == 10

    with pytest.raises(RuntimeError, match="already initialized"):
        provider.setup("http://localhost:8000/mcp")

    async def close():
        await provider.close()
        await provider.close()

    asyncio.run(close())
    client.close.assert_awaited_once()

    with pytest.raises(RuntimeError, match="not initialized"):
        _ = provider.client

    provider.setup("http://localhost:8000/mcp")
    assert factory.call_count == 2


def test_tools_results_and_connection_reuse(monkeypatch):
    server = FastMCP("Provider test")

    @server.tool
    def forecast(source: str) -> dict[str, str]:
        """Return the requested source."""
        return {"source": source}

    @server.tool
    def fail() -> str:
        raise ToolError("Forecast unavailable")

    client = Client(server)
    provider = MCPProvider()
    monkeypatch.setattr(provider, "_client", client)

    async def exercise():
        try:
            tools = await provider.list_tools()
            assert {tool.name for tool in tools} == {"forecast", "fail"}
            assert not client.is_connected()

            async with provider.client:
                result = await provider.call_tool("forecast", {"source": "solar"})
                assert not result.is_error
                assert result.structured_content == {"source": "solar"}
                assert result.content
                assert client.is_connected()
                failure = await provider.call_tool("fail")
                assert failure.is_error
                assert "Forecast unavailable" in str(failure.content)
                invalid = await provider.call_tool("forecast", {})
                assert invalid.is_error

            assert not client.is_connected()
        finally:
            await provider.close()

    asyncio.run(exercise())


def test_transport_errors_propagate_without_retry(monkeypatch):
    client = MagicMock()
    client.__aenter__ = AsyncMock(return_value=client)
    client.__aexit__ = AsyncMock(return_value=False)
    client.call_tool = AsyncMock(side_effect=ConnectionError("Disconnected"))
    provider = MCPProvider()
    monkeypatch.setattr(provider, "_client", client)

    with pytest.raises(ConnectionError, match="Disconnected"):
        asyncio.run(provider.call_tool("forecast", {"source": "solar"}))

    client.call_tool.assert_awaited_once_with(
        "forecast", {"source": "solar"}, raise_on_error=False
    )
    client.__aexit__.assert_awaited_once()
