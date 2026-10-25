import asyncio
from contextlib import aclosing
from typing import Any
from uuid import UUID

from dbos import DBOS, WorkflowSerializationFormat
from openai.types.chat import ChatCompletionMessageParam
from pydantic import BaseModel, Field, field_validator

from .agent import Agent
from .capabilities import Capabilities
from .providers.backend import backend_provider
from .providers.mcp import mcp_provider

QUEUE_NAME = "agent"
WORKFLOW_NAME = "agent_request"
STREAM_KEY = "events"
MAX_STEPS = 10
RUN_TIMEOUT = 180
PORTABLE = WorkflowSerializationFormat.PORTABLE


class AgentRequest(BaseModel):
    user_id: str = Field(min_length=1)
    chat_id: UUID
    messages: list[ChatCompletionMessageParam] = Field(min_length=1)
    memory_version: int = Field(ge=0, strict=True)

    @field_validator("user_id")
    @classmethod
    def validate_user_id(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("user_id must not be blank.")

        return value

    @field_validator("messages")
    @classmethod
    def validate_messages(
        cls, messages: list[ChatCompletionMessageParam]
    ) -> list[ChatCompletionMessageParam]:
        if messages[-1]["role"] != "user":
            raise ValueError("Messages must end with the current user request.")

        return messages


@DBOS.workflow(name=WORKFLOW_NAME, max_recovery_attempts=0, serialization_type=PORTABLE)
async def agent_request(request: dict[str, Any]) -> dict[str, Any]:
    sequence = 0

    async def emit(event: dict[str, Any]) -> None:
        nonlocal sequence
        await DBOS.write_stream_async(
            STREAM_KEY, {"sequence": sequence, **event}, serialization_type=PORTABLE
        )
        sequence += 1

    try:
        async with asyncio.timeout(RUN_TIMEOUT):
            data = AgentRequest.model_validate(request)
            await emit({"type": "run_started", "chat_id": str(data.chat_id)})
            version = data.memory_version

            async def save_summary(user_id: str, summary: str) -> None:
                nonlocal version
                version = await backend_provider.save_user_memory(
                    user_id, summary, version
                )

            capabilities = Capabilities(
                user_id=data.user_id,
                save_summary=save_summary,
                mcp_tools=await mcp_provider.list_tools(),
            )
            # Preserve portable JSON; SDK iterable fields can be consumed by Pydantic serialization.
            agent = Agent(request["messages"], capabilities, emit=emit)
            initial_count = len(agent.messages)

            for _ in range(MAX_STEPS):
                response = await agent.step()
                if not response.tool_calls:
                    break
            else:
                async with aclosing(agent.generate_final()) as stream:
                    async for _ in stream:
                        pass

            result = {
                "chat_id": str(data.chat_id),
                "messages": agent.messages[initial_count:],
                "artifacts": [
                    artifact.model_dump(mode="json") for artifact in agent.artifacts
                ],
            }
            await emit({"type": "run_completed", "result": result})

            return result
    except (Exception, asyncio.CancelledError) as error:
        await emit({"type": "run_failed", "error": type(error).__name__})
        raise
    finally:
        await DBOS.close_stream_async(STREAM_KEY)
