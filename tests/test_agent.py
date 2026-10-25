import asyncio
from unittest.mock import AsyncMock, MagicMock

import pytest
from fastmcp.client.client import CallToolResult
from mcp.types import TextContent, Tool
from openai.types.chat import ChatCompletionMessage, ChatCompletionMessageParam

from services.agent.app.agent import Agent
from services.agent.app.capabilities import Capabilities
from services.agent.app.providers.llm import LLMProvider
from services.agent.app.providers.mcp import MCPProvider


@pytest.fixture
def agent_setup():
    save = AsyncMock()
    mcp = AsyncMock(spec=MCPProvider)
    mcp.call_tool.return_value = CallToolResult(
        content=[TextContent(type="text", text="Forecast ready.")],
        structured_content={"power": 42},
        meta=None,
    )
    capabilities = Capabilities(
        user_id="user-1",
        save_summary=save,
        mcp=mcp,
        mcp_tools=[Tool(name="forecast", input_schema={"type": "object"})],
    )
    llm = MagicMock(spec=LLMProvider)
    messages: list[ChatCompletionMessageParam] = [
        {"role": "user", "content": "Question"}
    ]
    agent = Agent(messages, capabilities, llm=llm)

    return agent, llm, save, mcp, messages


def tool_response(*calls):
    return ChatCompletionMessage.model_validate(
        {
            "role": "assistant",
            "content": "Checking the requested information.",
            "tool_calls": [
                {
                    "id": call_id,
                    "type": "function",
                    "function": {"name": name, "arguments": arguments},
                }
                for call_id, name, arguments in calls
            ],
        }
    )


def test_history_is_owned_by_each_agent(agent_setup):
    agent, llm, _, _, original = agent_setup
    other = Agent(original, agent.capabilities, llm=llm)
    assert agent.messages[0] is not original[0]
    agent.messages.append({"role": "assistant", "content": "Answer"})
    assert len(original) == len(other.messages) == 1


def test_step_executes_local_and_mcp_tools(agent_setup):
    agent, llm, save, mcp, _ = agent_setup
    response = tool_response(
        ("call-1", "remember", '{"summary":"Prefers solar."}'),
        ("call-2", "forecast", '{"source":"solar"}'),
    )
    llm.tool_call.return_value = response
    assert asyncio.run(agent.step()) is response
    assert llm.tool_call.await_count == 1
    assert llm.tool_call.call_args.kwargs["tools"] == agent.capabilities.definitions
    save.assert_awaited_once_with("user-1", "Prefers solar.")
    mcp.call_tool.assert_awaited_once_with("forecast", {"source": "solar"})
    assert [message["role"] for message in agent.messages] == [
        "user",
        "assistant",
        "tool",
        "tool",
    ]
    assert agent.messages[1]["content"] == response.content
    results = [message for message in agent.messages if message["role"] == "tool"]
    assert [message["tool_call_id"] for message in results] == ["call-1", "call-2"]
    assert results[1]["content"] == "Forecast ready."
    assert agent.artifacts[0].data == {"power": 42}
    assert agent.artifacts[0].tool_call_id == "call-2"
    assert "power" not in results[1]["content"]


def test_step_can_finish_without_tools(agent_setup):
    agent, llm, save, mcp, _ = agent_setup
    response = ChatCompletionMessage(role="assistant", content="Final answer.")
    llm.tool_call.return_value = response
    assert asyncio.run(agent.step()) is response
    assert agent.messages[-1] == {"role": "assistant", "content": "Final answer."}
    save.assert_not_awaited()
    mcp.call_tool.assert_not_awaited()
    llm.generate_stream.assert_not_called()


@pytest.mark.parametrize("arguments", ["invalid", "[]", "null", "42"])
def test_invalid_arguments_become_tool_errors(agent_setup, arguments):
    agent, llm, save, _, _ = agent_setup
    llm.tool_call.return_value = tool_response(("call-1", "remember", arguments))
    asyncio.run(agent.step())
    result = agent.messages[-1]
    assert result["role"] == "tool"
    assert result["tool_call_id"] == "call-1"
    assert result["content"].startswith("Tool error:")
    save.assert_not_awaited()


def test_unknown_tool_is_reported_to_model(agent_setup):
    agent, llm, _, mcp, _ = agent_setup
    llm.tool_call.return_value = tool_response(("call-1", "unknown", "{}"))
    asyncio.run(agent.step())
    result = agent.messages[-1]
    assert result["role"] == "tool"
    assert result["content"].startswith("Tool error:")
    mcp.call_tool.assert_not_awaited()


def test_duplicate_call_ids_do_not_execute_or_change_history(agent_setup):
    agent, llm, save, mcp, original = agent_setup
    llm.tool_call.return_value = tool_response(
        ("same", "remember", '{"summary":"Fact"}'),
        ("same", "forecast", "{}"),
    )

    with pytest.raises(ValueError, match="unique"):
        asyncio.run(agent.step())

    assert agent.messages == original
    save.assert_not_awaited()
    mcp.call_tool.assert_not_awaited()


def test_failure_leaves_pending_calls_and_blocks_new_requests(agent_setup):
    agent, llm, save, mcp, _ = agent_setup
    llm.tool_call.return_value = tool_response(
        ("call-1", "remember", '{"summary":"Fact"}'),
        ("call-2", "forecast", "{}"),
    )
    mcp.call_tool.side_effect = ConnectionError("MCP unavailable")

    with pytest.raises(ConnectionError):
        asyncio.run(agent.step())

    assert len(agent.messages) == 3

    with pytest.raises(RuntimeError, match="unanswered"):
        asyncio.run(agent.step())

    async def finish():
        return [part async for part in agent.generate_final()]

    with pytest.raises(RuntimeError, match="unanswered"):
        asyncio.run(finish())

    assert llm.tool_call.await_count == 1
    save.assert_awaited_once()
    mcp.call_tool.assert_awaited_once()
    llm.generate_stream.assert_not_called()


def test_final_stream_appends_only_when_fully_consumed(agent_setup):
    agent, llm, _, _, _ = agent_setup

    async def tokens():
        yield "Forecast "
        yield "ready."

    llm.generate_stream.return_value = tokens()

    async def consume():
        stream = agent.generate_final()
        assert await anext(stream) == "Forecast "
        assert len(agent.messages) == 1
        assert [part async for part in stream] == ["ready."]

    asyncio.run(consume())
    assert agent.messages[-1] == {"role": "assistant", "content": "Forecast ready."}
    llm.tool_call.assert_not_awaited()


@pytest.mark.parametrize("interrupt", [False, True])
def test_failed_or_closed_final_stream_does_not_commit_partial_text(
    agent_setup, interrupt
):
    agent, llm, _, _, original = agent_setup
    closed = False

    async def tokens():
        nonlocal closed

        try:
            yield "Partial"
            raise ConnectionError("LLM disconnected")
        finally:
            closed = True

    llm.generate_stream.return_value = tokens()

    async def consume():
        stream = agent.generate_final()
        assert await anext(stream) == "Partial"
        if interrupt:
            await stream.aclose()
        else:
            with pytest.raises(ConnectionError):
                await anext(stream)

    asyncio.run(consume())
    assert closed
    assert agent.messages == original
    llm.generate_stream.assert_called_once()
