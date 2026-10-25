from collections.abc import Awaitable, Callable
from functools import partial
from typing import Any

from fastmcp.client.client import CallToolResult
from mcp.types import TextContent, Tool
from openai.types.chat import ChatCompletionToolParam
from pydantic import ValidationError

from .providers.mcp import MCPProvider, mcp_provider
from .tools import remember
from .tools.remember.tool import RememberArguments


def _error(message: str) -> CallToolResult:
    return CallToolResult(
        content=[TextContent(type="text", text=message)],
        structured_content=None,
        meta=None,
        is_error=True,
    )


class Capabilities:
    def __init__(
        self,
        *,
        user_id: str,
        save_summary: Callable[[str, str], Awaitable[None]],
        mcp_tools: list[Tool],
        mcp: MCPProvider = mcp_provider,
    ) -> None:
        self._mcp = mcp
        self._internal: dict[
            str, Callable[[dict[str, Any]], Awaitable[CallToolResult]]
        ] = {"remember": partial(remember, user_id=user_id, save_summary=save_summary)}
        self._remote: set[str] = set()
        self.definitions: list[ChatCompletionToolParam] = [
            {
                "type": "function",
                "function": {
                    "name": "remember",
                    "description": remember.__doc__ or "",
                    "parameters": RememberArguments.model_json_schema(),
                },
            }
        ]

        for tool in mcp_tools:
            if tool.name in self._internal or tool.name in self._remote:
                raise ValueError(f"Duplicate tool name: {tool.name}")

            self._remote.add(tool.name)
            self.definitions.append(
                {
                    "type": "function",
                    "function": {
                        "name": tool.name,
                        "description": tool.description or "",
                        "parameters": tool.input_schema,
                    },
                }
            )

    async def call(self, name: str, arguments: dict[str, Any]) -> CallToolResult:
        if name in self._internal:
            try:
                return await self._internal[name](arguments)
            except ValidationError as error:
                return _error(str(error))
        if name in self._remote:
            return await self._mcp.call_tool(name, arguments)

        return _error(f"Unknown tool: {name}")
