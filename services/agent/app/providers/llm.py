import asyncio
from collections.abc import AsyncGenerator, Awaitable, Callable, Coroutine
from functools import wraps
from typing import Any, Concatenate

from openai import (
    APIConnectionError,
    AsyncOpenAI,
    InternalServerError,
    Omit,
    RateLimitError,
    omit,
)
from openai.types.chat import (
    ChatCompletionMessage,
    ChatCompletionMessageParam,
    ChatCompletionToolParam,
    completion_create_params,
)
from pydantic import BaseModel, ValidationError


class EmptyLLMResponse(RuntimeError):
    pass


class IncompleteLLMResponse(RuntimeError):
    pass


class InvalidLLMJSON(RuntimeError):
    def __init__(self, message: str, content: str = "") -> None:
        super().__init__(message)
        self.content = content


UNREADY = "LLMProvider is not initialized. Call setup() first."

FAULT = "What you returned was rejected: {error}\nAnswer again so that it is not."


def _correction(
    fault: EmptyLLMResponse | InvalidLLMJSON,
) -> list[ChatCompletionMessageParam]:
    written = getattr(fault, "content", "")
    shown: list[ChatCompletionMessageParam] = (
        [{"role": "assistant", "content": written}] if written else []
    )

    return [*shown, {"role": "user", "content": FAULT.format(error=fault)}]


def retry_generate[**P, R](
    func: Callable[
        Concatenate["LLMProvider", list[ChatCompletionMessageParam], P],
        Coroutine[Any, Any, R],
    ],
) -> Callable[
    Concatenate["LLMProvider", list[ChatCompletionMessageParam], P],
    Coroutine[Any, Any, R],
]:
    @wraps(func)
    async def wrapper(
        self: "LLMProvider",
        messages: list[ChatCompletionMessageParam],
        *args: P.args,
        **kwargs: P.kwargs,
    ) -> R:
        history = messages

        for _ in range(1, self._schema_attempts):
            try:
                return await func(self, history, *args, **kwargs)
            except (EmptyLLMResponse, InvalidLLMJSON) as fault:
                history = [*history, *_correction(fault)]
                await asyncio.sleep(self._schema_delay)
            except (APIConnectionError, InternalServerError, RateLimitError):
                await asyncio.sleep(self._schema_delay)

        return await func(self, history, *args, **kwargs)

    return wrapper


class LLMProvider:
    def __init__(self) -> None:
        self._client: AsyncOpenAI | None = None
        self._default_model: str | None = None
        self._schema_attempts = 3
        self._schema_delay = 1.0

    def setup(
        self,
        url: str,
        api_key: str,
        model: str,
        schema_attempts: int = 3,
        schema_delay: float = 1.0,
        timeout: float = 180.0,
        transport_retries: int = 0,
    ) -> None:
        if self._client is not None:
            raise RuntimeError("LLMProvider is already initialized.")

        self._client = AsyncOpenAI(
            api_key=api_key,
            base_url=url,
            timeout=timeout,
            max_retries=transport_retries,
        )
        self._default_model = model
        self._schema_attempts = schema_attempts
        self._schema_delay = schema_delay

    @property
    def client(self) -> AsyncOpenAI:
        if self._client is None:
            raise RuntimeError(UNREADY)

        return self._client

    @property
    def default_model(self) -> str:
        if self._default_model is None:
            raise RuntimeError(UNREADY)

        return self._default_model

    async def _complete(
        self,
        messages: list[ChatCompletionMessageParam],
        response_format: completion_create_params.ResponseFormat | Omit = omit,
        model: str | None = None,
        temperature: float | Omit = omit,
    ) -> str:
        response = await self.client.chat.completions.create(
            model=model or self.default_model,
            messages=messages,
            response_format=response_format,
            temperature=temperature,
        )
        content = response.choices[0].message.content

        if not content or not content.strip():
            raise EmptyLLMResponse("LLM returned an empty response.")

        return content

    async def _json(
        self,
        messages: list[ChatCompletionMessageParam],
        schema: dict[str, Any],
        name: str,
        model: str | None = None,
        temperature: float | Omit = omit,
    ) -> str:
        return await self._complete(
            messages,
            {"type": "json_schema", "json_schema": {"name": name, "schema": schema}},
            model,
            temperature,
        )

    @retry_generate
    async def generate(
        self, messages: list[ChatCompletionMessageParam], *, model: str | None = None
    ) -> str:
        return await self._complete(messages, model=model)

    async def generate_stream(
        self, messages: list[ChatCompletionMessageParam], *, model: str | None = None
    ) -> AsyncGenerator[str]:
        has_text = False
        finished = False

        async with await self.client.chat.completions.create(
            model=model or self.default_model,
            messages=messages,
            stream=True,
        ) as stream:
            async for chunk in stream:
                if not chunk.choices:
                    continue

                choice = chunk.choices[0]
                text = choice.delta.content
                if text:
                    has_text = has_text or bool(text.strip())
                    yield text
                if choice.finish_reason is not None:
                    if choice.finish_reason != "stop":
                        raise IncompleteLLMResponse(
                            f"LLM stream stopped with reason: {choice.finish_reason}."
                        )

                    finished = True

        if not has_text:
            raise EmptyLLMResponse("LLM returned an empty response.")
        if not finished:
            raise IncompleteLLMResponse("LLM stream ended without a completion marker.")

    @retry_generate
    async def generate_json[T: BaseModel](
        self,
        messages: list[ChatCompletionMessageParam],
        *,
        response_model: type[T],
        model: str | None = None,
        temperature: float | Omit = omit,
    ) -> T:
        content = await self._json(
            messages,
            response_model.model_json_schema(),
            response_model.__name__,
            model,
            temperature,
        )

        try:
            return response_model.model_validate_json(content)
        except ValidationError as exc:
            raise InvalidLLMJSON(
                f"LLM response does not match {response_model.__name__}: {exc}",
                content,
            ) from exc

    @retry_generate
    async def tool_call(
        self,
        messages: list[ChatCompletionMessageParam],
        *,
        tools: list[ChatCompletionToolParam],
        model: str | None = None,
    ) -> ChatCompletionMessage:
        response = await self.client.chat.completions.create(
            model=model or self.default_model,
            messages=messages,
            tools=tools or omit,
        )
        if not response.choices:
            raise EmptyLLMResponse("LLM returned no choices.")

        message = response.choices[0].message
        if not message.tool_calls and not (message.content and message.content.strip()):
            raise EmptyLLMResponse("LLM returned an empty response.")

        return message

    async def close(self) -> None:
        if self._client is not None:
            await self._client.close()
            self._client = None

    async def tool_call_stream(
        self,
        messages: list[ChatCompletionMessageParam],
        *,
        tools: list[ChatCompletionToolParam],
        on_text: Callable[[str], Awaitable[None]],
        model: str | None = None,
    ) -> ChatCompletionMessage:
        async with self.client.chat.completions.stream(
            model=model or self.default_model,
            messages=messages,
            tools=tools or omit,
        ) as stream:
            async for event in stream:
                if event.type == "content.delta":
                    await on_text(event.delta)

            response = await stream.get_final_completion()

        if not response.choices:
            raise EmptyLLMResponse("LLM returned no choices.")

        choice = response.choices[0]
        if choice.finish_reason not in {"stop", "tool_calls"}:
            raise IncompleteLLMResponse("LLM stream did not complete successfully.")

        message = choice.message
        if not message.tool_calls and not (message.content and message.content.strip()):
            raise EmptyLLMResponse("LLM returned an empty response.")

        return message


llm_provider = LLMProvider()
