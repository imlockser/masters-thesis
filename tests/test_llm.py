import asyncio
import json
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock

import httpx2
import pytest
from openai import APIConnectionError, AsyncOpenAI, omit
from openai.types.chat import ChatCompletionMessage, ChatCompletionToolParam

from services.agent.app.providers.llm import (
    EmptyLLMResponse,
    IncompleteLLMResponse,
    LLMProvider,
)


def test_tool_stream_forwards_text_and_assembles_fragmented_calls():
    deltas = [
        {"role": "assistant", "content": "Checking "},
        {
            "content": "solar.",
            "tool_calls": [
                {
                    "index": 0,
                    "id": "call-1",
                    "type": "function",
                    "function": {"name": "forecast", "arguments": '{"source":'},
                }
            ],
        },
        {"tool_calls": [{"index": 0, "function": {"arguments": '"solar"}'}}]},
    ]

    class Stream(httpx2.AsyncByteStream):
        async def __aiter__(self):
            chunk = {
                "id": "response-1",
                "object": "chat.completion.chunk",
                "created": 1,
                "model": "test",
                "choices": [],
            }

            for delta in deltas:
                chunk["choices"] = [{"index": 0, "delta": delta, "finish_reason": None}]
                yield f"data: {json.dumps(chunk)}\n\n".encode()

            chunk["choices"] = [
                {"index": 0, "delta": {}, "finish_reason": "tool_calls"}
            ]
            yield f"data: {json.dumps(chunk)}\n\ndata: [DONE]\n\n".encode()

    async def run():
        llm = LLMProvider()
        llm._default_model = "test"
        llm._client = AsyncOpenAI(
            api_key="test",
            max_retries=0,
            http_client=httpx2.AsyncClient(
                transport=httpx2.MockTransport(
                    lambda _: httpx2.Response(
                        200,
                        headers={"content-type": "text/event-stream"},
                        stream=Stream(),
                    )
                )
            ),
        )
        parts = []

        async def on_text(text):
            parts.append(text)

        try:
            response = await llm.tool_call_stream(
                [],
                tools=[
                    {
                        "type": "function",
                        "function": {
                            "name": "forecast",
                            "parameters": {"type": "object"},
                        },
                    }
                ],
                on_text=on_text,
            )
        finally:
            await llm.close()

        assert parts == ["Checking ", "solar."]
        assert response.content == "Checking solar."
        assert response.tool_calls and response.tool_calls[0].type == "function"
        assert response.tool_calls[0].function.arguments == '{"source":"solar"}'

    asyncio.run(run())


@pytest.fixture
def provider(monkeypatch: pytest.MonkeyPatch):
    provider = LLMProvider()
    provider._default_model = "default-model"
    provider._schema_delay = 0
    create = AsyncMock()
    client = SimpleNamespace(
        chat=SimpleNamespace(completions=SimpleNamespace(create=create))
    )
    monkeypatch.setattr(provider, "_client", client)

    return provider, create


def respond(message):
    return SimpleNamespace(choices=[SimpleNamespace(message=message)])


def test_preserves_tool_calls_and_text(provider):
    llm, create = provider
    message = ChatCompletionMessage.model_validate(
        {
            "role": "assistant",
            "content": "Checking both forecasts.",
            "tool_calls": [
                {
                    "id": call_id,
                    "type": "function",
                    "function": {
                        "name": "forecast",
                        "arguments": '{"source":"solar"}',
                    },
                }
                for call_id in ("call_1", "call_2")
            ],
        }
    )
    tools: list[ChatCompletionToolParam] = [
        {"type": "function", "function": {"name": "forecast"}}
    ]
    create.return_value = respond(message)
    result = asyncio.run(llm.tool_call([], tools=tools, model="other-model"))
    assert result is message
    assert create.call_args.kwargs["tools"] == tools
    assert create.call_args.kwargs["model"] == "other-model"
    message.content = None
    assert asyncio.run(llm.tool_call([], tools=tools)) is message


def test_final_text_and_empty_tools(provider):
    llm, create = provider
    message = ChatCompletionMessage(role="assistant", content="Final answer.")
    create.return_value = respond(message)
    assert asyncio.run(llm.tool_call([], tools=[])) is message
    assert create.call_args.kwargs["tools"] is omit
    assert create.call_args.kwargs["model"] == "default-model"


def test_retries_transport_and_empty_responses(provider):
    llm, create = provider
    message = ChatCompletionMessage(role="assistant", content="Recovered.")
    create.side_effect = [
        APIConnectionError(request=MagicMock()),
        SimpleNamespace(choices=[]),
        respond(message),
    ]
    assert asyncio.run(llm.tool_call([], tools=[])) is message
    assert create.await_count == 3


class CompletionStream:
    def __init__(self, chunks):
        self.chunks = chunks
        self.closed = False
        self.delivered = 0

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        self.closed = True

    async def __aiter__(self):
        for chunk in self.chunks:
            if isinstance(chunk, Exception):
                raise chunk

            self.delivered += 1
            yield chunk


def chunk(text=None, finish_reason=None):
    return SimpleNamespace(
        choices=[
            SimpleNamespace(
                delta=SimpleNamespace(content=text), finish_reason=finish_reason
            )
        ]
    )


def test_stream_yields_before_completion_and_preserves_whitespace(provider):
    llm, create = provider
    stream = CompletionStream(
        [
            chunk(),
            chunk("Forecast"),
            SimpleNamespace(choices=[]),
            chunk(" "),
            chunk("ready."),
            chunk(finish_reason="stop"),
        ]
    )
    create.return_value = stream

    async def consume():
        response = llm.generate_stream([], model="stream-model")
        assert await anext(response) == "Forecast"
        assert stream.delivered == 2
        assert not stream.closed

        return [text async for text in response]

    assert asyncio.run(consume()) == [" ", "ready."]
    assert stream.closed
    create.assert_awaited_once_with(model="stream-model", messages=[], stream=True)


@pytest.mark.parametrize(
    "chunks, error",
    [
        ([chunk(finish_reason="stop")], EmptyLLMResponse),
        ([chunk(" "), chunk(finish_reason="stop")], EmptyLLMResponse),
        ([chunk("Partial")], IncompleteLLMResponse),
        ([chunk("Partial"), chunk(finish_reason="length")], IncompleteLLMResponse),
        (
            [chunk("Partial"), chunk(finish_reason="content_filter")],
            IncompleteLLMResponse,
        ),
        ([chunk("Partial"), ConnectionError("Disconnected")], ConnectionError),
    ],
)
def test_stream_errors_close_without_retry(provider, chunks, error):
    llm, create = provider
    stream = CompletionStream(chunks)
    create.return_value = stream

    async def consume():
        return [text async for text in llm.generate_stream([])]

    with pytest.raises(error):
        asyncio.run(consume())

    assert stream.closed
    assert create.await_count == 1


def test_stream_can_be_closed_early(provider):
    llm, create = provider
    stream = CompletionStream(
        [chunk("First"), chunk("Second"), chunk(finish_reason="stop")]
    )
    create.return_value = stream

    async def consume():
        response = llm.generate_stream([])
        assert await anext(response) == "First"
        await response.aclose()

    asyncio.run(consume())
    assert stream.closed
    assert stream.delivered == 1
    create.reset_mock(side_effect=True)
    create.return_value = respond(ChatCompletionMessage(role="assistant", content=" "))

    with pytest.raises(EmptyLLMResponse):
        asyncio.run(llm.tool_call([], tools=[]))

    assert create.await_count == 3
