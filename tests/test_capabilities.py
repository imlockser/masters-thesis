import asyncio
from unittest.mock import AsyncMock

import pytest
from fastmcp.client.client import CallToolResult
from mcp.types import Tool

from services.agent.app.capabilities import Capabilities
from services.agent.app.providers.mcp import MCPProvider


@pytest.fixture
def capabilities():
    save = AsyncMock()
    mcp = AsyncMock(spec=MCPProvider)
    tool = Tool(
        name="forecast",
        description="Get a forecast.",
        input_schema={"type": "object", "properties": {}},
    )
    catalog = Capabilities(
        user_id="user-1", save_summary=save, mcp_tools=[tool], mcp=mcp
    )

    return catalog, save, mcp, tool


def test_combines_definitions(capabilities):
    catalog, _, _, tool = capabilities
    definitions = {entry["function"]["name"]: entry for entry in catalog.definitions}
    assert set(definitions) == {"remember", "forecast"}
    parameters = definitions["remember"]["function"]["parameters"]
    assert set(parameters["properties"]) == {"summary"}
    assert parameters["required"] == ["summary"]
    assert not parameters["additionalProperties"]
    assert definitions["forecast"]["function"]["parameters"] == tool.input_schema


def test_remember_uses_bound_user(capabilities):
    catalog, save, _, _ = capabilities
    result = asyncio.run(catalog.call("remember", {"summary": " Prefers solar. "}))
    assert not result.is_error
    save.assert_awaited_once_with("user-1", "Prefers solar.")


@pytest.mark.parametrize(
    "arguments",
    [{}, {"summary": " "}, {"summary": 42}, {"summary": "Fact", "user_id": "other"}],
)
def test_invalid_memory_arguments_do_not_write(capabilities, arguments):
    catalog, save, _, _ = capabilities
    result = asyncio.run(catalog.call("remember", arguments))
    assert result.is_error
    save.assert_not_awaited()


def test_storage_failure_propagates(capabilities):
    catalog, save, _, _ = capabilities
    save.side_effect = RuntimeError("Storage unavailable")

    with pytest.raises(RuntimeError, match="Storage unavailable"):
        asyncio.run(catalog.call("remember", {"summary": "Prefers solar."}))

    assert save.await_count == 1


@pytest.mark.parametrize("is_error", [False, True])
def test_routes_mcp_calls_and_preserves_results(capabilities, is_error):
    catalog, save, mcp, _ = capabilities
    expected = CallToolResult(
        content=[], structured_content={"forecast": 42}, meta=None, is_error=is_error
    )
    mcp.call_tool.return_value = expected
    assert asyncio.run(catalog.call("forecast", {"source": "solar"})) is expected
    mcp.call_tool.assert_awaited_once_with("forecast", {"source": "solar"})
    save.assert_not_awaited()


def test_unknown_tool_is_not_executed(capabilities):
    catalog, save, mcp, _ = capabilities
    assert asyncio.run(catalog.call("unknown", {})).is_error
    save.assert_not_awaited()
    mcp.call_tool.assert_not_awaited()


@pytest.mark.parametrize("names", [["remember"], ["forecast", "forecast"]])
def test_rejects_duplicate_names(names):
    with pytest.raises(ValueError, match="Duplicate tool name"):
        Capabilities(
            user_id="user-1",
            save_summary=AsyncMock(),
            mcp_tools=[Tool(name=name, input_schema={}) for name in names],
        )


def test_ten_concurrent_runs_keep_users_separate():
    async def exercise():
        saved = []
        entered = 0
        ready = asyncio.Event()

        async def save(user_id, summary):
            nonlocal entered
            entered += 1
            if entered == 10:
                ready.set()
            await ready.wait()
            saved.append((user_id, summary))

        catalogs = [
            Capabilities(user_id=str(index), save_summary=save, mcp_tools=[])
            for index in range(10)
        ]
        await asyncio.wait_for(
            asyncio.gather(
                *(
                    catalog.call("remember", {"summary": f"Summary {index}"})
                    for index, catalog in enumerate(catalogs)
                )
            ),
            timeout=2,
        )
        assert set(saved) == {(str(index), f"Summary {index}") for index in range(10)}

    asyncio.run(exercise())
