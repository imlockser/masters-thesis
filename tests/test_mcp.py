import asyncio
from pathlib import Path
from unittest.mock import patch

import pytest
from fastmcp import Client

from services.mcp.app.main import main
from services.mcp.app.mcp import mcp
from services.mcp.app.settings import PROJECT_ROOT, Settings, settings
from services.mcp.app.solar import jobs


@pytest.fixture(autouse=True)
def no_registry_network(monkeypatch):
    monkeypatch.setattr(jobs, "refresh_model", lambda: None)


def test_settings_load_env_from_repository_root(monkeypatch, tmp_path):
    monkeypatch.delenv("MCP_NAME", raising=False)
    monkeypatch.delenv("MCP_HOST", raising=False)
    monkeypatch.chdir(tmp_path)
    assert PROJECT_ROOT == Path(__file__).resolve().parents[1]
    assert Settings.model_config.get("env_file") == PROJECT_ROOT / "env" / "mcp.env"
    configured = Settings()  # pyright: ignore[reportCallIssue]
    assert configured.mcp_name == "Energy Forecasting MCP"
    assert configured.mcp_host == "127.0.0.1"


def test_environment_overrides_env_file(monkeypatch):
    monkeypatch.setenv("MCP_NAME", "Test MCP")
    monkeypatch.setenv("MCP_HOST", "0.0.0.0")
    configured = Settings()  # pyright: ignore[reportCallIssue]
    assert configured.mcp_name == "Test MCP"
    assert configured.mcp_host == "0.0.0.0"


def test_server_registers_solar_forecast():
    async def connect():
        async with Client(mcp) as client:
            tools = await client.list_tools()
            assert [tool.name for tool in tools] == ["forecast_solar_generation"]

    asyncio.run(connect())


def test_main_starts_http_server():
    with patch.object(mcp, "run") as run:
        main()

    run.assert_called_once_with(transport="http", host=settings.mcp_host)
