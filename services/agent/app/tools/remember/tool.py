from collections.abc import Awaitable, Callable
from typing import Any

from fastmcp.client.client import CallToolResult
from mcp.types import TextContent
from pydantic import BaseModel, ConfigDict, Field


class RememberArguments(BaseModel):
    model_config = ConfigDict(extra="forbid", str_strip_whitespace=True)

    summary: str = Field(
        min_length=1,
        description="The complete updated user summary, preserving relevant prior facts.",
    )


async def remember(
    arguments: dict[str, Any],
    *,
    user_id: str,
    save_summary: Callable[[str, str], Awaitable[None]],
) -> CallToolResult:
    """Save the complete long-term user summary, preserving relevant prior facts."""
    parsed = RememberArguments.model_validate(arguments)
    await save_summary(user_id, parsed.summary)

    return CallToolResult(
        content=[TextContent(type="text", text="User summary saved.")],
        structured_content=None,
        meta=None,
    )
