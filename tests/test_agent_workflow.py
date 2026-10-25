import asyncio
import inspect
from unittest.mock import AsyncMock

import pytest
from openai.types.chat import ChatCompletionMessage
from pydantic import ValidationError

from services.agent.app import workflow
from services.agent.app.providers.llm import llm_provider


def request():
    return {
        "user_id": "user-1",
        "chat_id": "00000000-0000-0000-0000-000000000001",
        "messages": [{"role": "user", "content": "Remember my preferences."}],
        "memory_version": 4,
    }


@pytest.fixture
def runtime(monkeypatch):
    events = []

    async def write(key, event, **kwargs):
        assert key == workflow.STREAM_KEY
        assert kwargs["serialization_type"] == workflow.PORTABLE
        events.append(event)

    monkeypatch.setattr(workflow.DBOS, "write_stream_async", write)
    close = AsyncMock()
    monkeypatch.setattr(workflow.DBOS, "close_stream_async", close)
    monkeypatch.setattr(workflow.mcp_provider, "list_tools", AsyncMock(return_value=[]))
    save = AsyncMock(side_effect=[5, 6])
    monkeypatch.setattr(workflow.backend_provider, "save_user_memory", save)

    return events, close, save


def test_workflow_streams_before_completion_and_tracks_memory_version(
    runtime, monkeypatch
):
    events, close, save = runtime
    responses = iter(
        [
            ChatCompletionMessage.model_validate(
                {
                    "role": "assistant",
                    "content": "Saving.",
                    "tool_calls": [
                        {
                            "id": f"call-{index}",
                            "type": "function",
                            "function": {
                                "name": "remember",
                                "arguments": '{"summary":"Prefers solar."}',
                            },
                        }
                        for index in range(2)
                    ],
                }
            ),
            ChatCompletionMessage(role="assistant", content="Saved."),
        ]
    )

    async def complete(messages, *, tools, on_text):
        response = next(responses)
        await on_text(response.content)
        assert events[-1]["type"] == "text_delta"
        assert all(event["type"] != "run_completed" for event in events)

        return response

    monkeypatch.setattr(llm_provider, "tool_call_stream", complete)
    data = request()
    result = asyncio.run(inspect.unwrap(workflow.agent_request)(data))
    assert [message["role"] for message in result["messages"]] == [
        "assistant",
        "tool",
        "tool",
        "assistant",
    ]
    assert len(data["messages"]) == 1
    assert [call.args[-1] for call in save.await_args_list] == [4, 5]
    assert [event["sequence"] for event in events] == list(range(len(events)))
    assert events[-1] == {
        "sequence": len(events) - 1,
        "type": "run_completed",
        "result": result,
    }
    assert sum(event["type"] == "tool_completed" for event in events) == 2
    close.assert_awaited_once_with(workflow.STREAM_KEY)


def test_workflow_accepts_completed_history_with_tool_calls(runtime, monkeypatch):
    data = request()
    data["messages"] = [
        {"role": "user", "content": [{"type": "text", "text": "Old question"}]},
        {
            "role": "assistant",
            "tool_calls": [
                {
                    "id": "old-call",
                    "type": "function",
                    "function": {"name": "forecast", "arguments": "{}"},
                }
            ],
        },
        {"role": "tool", "tool_call_id": "old-call", "content": "Forecast ready."},
        {"role": "assistant", "content": "Old answer"},
        {"role": "user", "content": "New question"},
    ]
    complete = AsyncMock(
        return_value=ChatCompletionMessage(role="assistant", content="New answer")
    )
    monkeypatch.setattr(llm_provider, "tool_call_stream", complete)

    result = asyncio.run(inspect.unwrap(workflow.agent_request)(data))

    assert complete.await_args is not None
    assert complete.await_args.args[0][:5] == data["messages"]
    assert result["messages"] == [{"role": "assistant", "content": "New answer"}]


def test_failure_emits_terminal_event_and_propagates(runtime, monkeypatch):
    events, close, _ = runtime
    complete = AsyncMock(side_effect=ConnectionError("Private infrastructure details"))
    monkeypatch.setattr(llm_provider, "tool_call_stream", complete)

    with pytest.raises(ConnectionError):
        asyncio.run(inspect.unwrap(workflow.agent_request)(request()))

    assert events[-1]["type"] == "run_failed"
    assert events[-1]["error"] == "ConnectionError"
    complete.assert_awaited_once()
    close.assert_awaited_once()


def test_tool_limit_forces_one_final_stream(runtime, monkeypatch):
    events, _, _ = runtime
    monkeypatch.setattr(workflow, "MAX_STEPS", 1)

    async def complete(messages, **kwargs):
        return ChatCompletionMessage.model_validate(
            {
                "role": "assistant",
                "tool_calls": [
                    {
                        "id": "unknown-call",
                        "type": "function",
                        "function": {"name": "unknown", "arguments": "{}"},
                    }
                ],
            }
        )

    async def final(messages):
        yield "Final "
        yield "answer."

    monkeypatch.setattr(llm_provider, "tool_call_stream", complete)
    monkeypatch.setattr(llm_provider, "generate_stream", final)
    result = asyncio.run(inspect.unwrap(workflow.agent_request)(request()))
    assert result["messages"][-1] == {"role": "assistant", "content": "Final answer."}
    assert [event["text"] for event in events if event["type"] == "text_delta"] == [
        "Final ",
        "answer.",
    ]


@pytest.mark.parametrize(
    "field,value",
    [
        ("user_id", " "),
        ("chat_id", "invalid"),
        ("memory_version", -1),
        ("memory_version", True),
        ("messages", []),
        ("messages", [{"role": "assistant", "content": "Already finished"}]),
    ],
)
def test_request_validation(field, value):
    data = request()
    data[field] = value

    with pytest.raises(ValidationError):
        workflow.AgentRequest.model_validate(data)
