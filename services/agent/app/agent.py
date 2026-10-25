import json
from collections.abc import AsyncGenerator, Awaitable, Callable
from contextlib import aclosing
from copy import deepcopy
from typing import Any, cast

from openai.types.chat import (
    ChatCompletionMessage,
    ChatCompletionMessageFunctionToolCall,
    ChatCompletionMessageParam,
)
from pydantic import BaseModel

from .capabilities import Capabilities
from .providers.llm import LLMProvider, llm_provider


class ToolArtifact(BaseModel):
    tool_call_id: str
    name: str
    data: dict[str, Any]


class Agent:
    def __init__(
        self,
        messages: list[ChatCompletionMessageParam],
        capabilities: Capabilities,
        *,
        llm: LLMProvider = llm_provider,
        emit: Callable[[dict[str, Any]], Awaitable[None]] | None = None,
    ) -> None:
        self.messages = deepcopy(messages)
        self.artifacts: list[ToolArtifact] = []
        self.capabilities = capabilities
        self._llm = llm
        self._emit_event = emit

    async def _emit(self, event_type: str, **data: Any) -> None:
        if self._emit_event is not None:
            await self._emit_event({"type": event_type, **data})

    async def step(self) -> ChatCompletionMessage:
        seen = self._check_ready()
        message_index = len(self.messages)
        await self._emit("generation_started", message_index=message_index)

        async def on_text(text: str) -> None:
            await self._emit("text_delta", message_index=message_index, text=text)

        if self._emit_event is None:
            response = await self._llm.tool_call(
                self.messages, tools=self.capabilities.definitions
            )
        else:
            response = await self._llm.tool_call_stream(
                self.messages, tools=self.capabilities.definitions, on_text=on_text
            )

        calls: list[ChatCompletionMessageFunctionToolCall] = []

        for call in response.tool_calls or []:
            if call.type != "function":
                raise ValueError("Only function tool calls are supported.")
            if not call.id or call.id in seen:
                raise ValueError(
                    "Tool call IDs must be non-empty and unique in history."
                )

            seen.add(call.id)
            calls.append(call)

        self.messages.append(
            cast(
                ChatCompletionMessageParam,
                response.model_dump(
                    mode="json",
                    exclude_none=True,
                    include={"role", "content", "tool_calls"},
                ),
            )
        )
        await self._emit(
            "message_completed", message_index=message_index, message=self.messages[-1]
        )

        for call in calls:
            artifact: ToolArtifact | None = None
            await self._emit(
                "tool_started", tool_call_id=call.id, name=call.function.name
            )

            try:
                arguments = json.loads(call.function.arguments)
                if not isinstance(arguments, dict):
                    raise TypeError("Tool arguments must be a JSON object.")
            except (ValueError, TypeError):
                content = "Tool error: arguments must be a JSON object."
            else:
                result = await self.capabilities.call(call.function.name, arguments)
                content = "\n".join(
                    block.text for block in result.content if block.type == "text"
                )
                if not content:
                    content = "Tool returned no text content."
                if result.is_error:
                    content = "Tool error: " + content

                if result.structured_content is not None:
                    artifact = ToolArtifact(
                        tool_call_id=call.id,
                        name=call.function.name,
                        data=result.structured_content,
                    )
                    self.artifacts.append(artifact)

            self.messages.append(
                {"role": "tool", "tool_call_id": call.id, "content": content}
            )
            await self._emit(
                "tool_completed",
                tool_call_id=call.id,
                name=call.function.name,
                message_index=len(self.messages) - 1,
                message=self.messages[-1],
                artifact=artifact.model_dump(mode="json")
                if artifact is not None
                else None,
            )

        return response

    async def generate_final(self) -> AsyncGenerator[str]:
        self._check_ready()
        chunks: list[str] = []
        message_index = len(self.messages)
        await self._emit("generation_started", message_index=message_index)

        async with aclosing(self._llm.generate_stream(self.messages)) as stream:
            async for text in stream:
                chunks.append(text)
                await self._emit("text_delta", message_index=message_index, text=text)
                yield text

        self.messages.append({"role": "assistant", "content": "".join(chunks)})
        await self._emit(
            "message_completed", message_index=message_index, message=self.messages[-1]
        )

    def _check_ready(self) -> set[str]:
        seen: set[str] = set()
        pending: set[str] = set()

        for message in self.messages:
            if message["role"] == "assistant":
                for call in message.get("tool_calls") or []:
                    seen.add(call["id"])
                    pending.add(call["id"])
            elif message["role"] == "tool":
                pending.discard(message["tool_call_id"])

        if pending:
            raise RuntimeError("History contains unanswered tool calls.")

        return seen
