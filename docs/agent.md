# Agent

The agent has a per-run [Agent](../services/agent/app/agent.py), a Capabilities
catalog, and shared low-level providers under `app/providers/`: `llm_provider`,
`mcp_provider`, and `backend_provider`. The [DBOS runtime](agent-runtime.md)
implements the queued execution loop and event stream. The Go
[agent API](agent-api.md) prepares requests, enqueues workflows, persists results,
and forwards events through SSE.

## Agent state and execution

Construct `Agent(messages, capabilities, llm=llm_provider, emit=None)` once per run. It owns
a deep copy of the supplied history in `messages`; the caller prepares system
instructions, user memory, prior completed turns, and the current user request.
Independent instances can run concurrently. Do not invoke operations concurrently
on the same instance.

`step()` performs one LLM request and executes all selected function calls
sequentially. It records the assistant message, including any accompanying text,
then a `tool` message for each call with the matching `tool_call_id`. It returns
the original SDK assistant message. No tool calls means the model has already
provided its final answer; the method does not generate another answer or start
an outer loop.

Tool text blocks become plain `tool` message content, with a readable error prefix
on tool failures. Structured results are retained as typed `ToolArtifact` entries
in `agent.artifacts` and in completion events, never included in LLM history.
There is no fallback that serializes a structured artifact into text. Non-text
MCP blocks are not forwarded by the current text-only agent.
Malformed or non-object arguments become tool error messages;
validation failures and unknown tools are reported through Capabilities. Invalid
or reused call IDs and unsupported call types are rejected before execution.
Storage and transport exceptions propagate, retaining completed results and any
unanswered calls in history. Further operations reject unanswered calls, so
failed steps cannot silently rerun completed side effects or send invalid history
to the LLM. Recovery is the caller's responsibility.

`generate_final()` is an async iterator for explicit completion without tools,
such as after gathering enough information. It yields text deltas and appends the
complete assistant response only after successful exhaustion. Failed or explicitly
closed streams leave no partial assistant message in history. Consume the iterator
fully, or close it with `aclose()`/`contextlib.aclosing` when stopping early.

Do not automatically call `generate_final()` after a tool-free `step()` response:
that response is already final. With an asynchronous `emit(event)` callback,
`step()` uses streamed tool calling and emits text during generation; without
one, it retains ordinary non-streamed tool calling. Both methods emit generation,
message, and tool events through this callback without depending on DBOS.

## LLM provider

The [LLM provider](../services/agent/app/providers/llm.py) handles model requests.

The provider uses the asynchronous OpenAI SDK with a configurable endpoint and
model, allowing it to connect to LiteLLM. It supports text responses, structured
responses validated by Pydantic, and standard function tool calling.

`tool_call()` accepts chat history and function tool definitions and returns the
SDK's complete assistant message, containing tool calls, text, or both. An empty
tool list is omitted from the request. `Agent.step()` handles tool execution and
history; the outer caller decides whether to perform another step. Tool arguments
remain JSON strings until the agent parses them; the provider does not generate
or execute them separately.

`generate_stream()` requests a text completion without tools and yields SDK text
deltas, which may contain one or more tokens. It preserves whitespace, skips
non-text chunks, and closes the SDK stream on completion, errors, or explicit
closure. Empty output raises `EmptyLLMResponse`; truncation or a missing final
completion marker raises `IncompleteLLMResponse`. This method has no application
retry wrapper, avoiding duplicate text after partial output.

`tool_call_stream()` forwards text deltas to an awaited `on_text` callback and
returns the complete assistant message. The SDK assembles fragmented tool calls;
the agent executes them after generation completes. This method also has no
application retries. A delta can contain several tokens.

Empty or invalid responses trigger retries with corrective messages. Connection,
server, and rate limit errors are retried after a delay. Exhausted retries
propagate the error to the caller. SDK transport retries are configured separately.
The provider has no logging dependency; its client is initialized through
`setup()` and released through `close()`.

## MCP provider

The [MCP provider](../services/agent/app/providers/mcp.py) wraps FastMCP's
Streamable HTTP client. `setup(url, timeout)` configures the client without
connecting; use `http://127.0.0.1:8000/mcp` for the local MCP service.
`list_tools()` returns native MCP tool definitions, while `call_tool(name,
arguments)` returns the complete FastMCP result, including content blocks,
structured content, and the `is_error` flag. Tool failures remain results;
transport and protocol exceptions propagate. Calls are not automatically retried
because tools may have side effects.

Each operation opens and closes a client context. To reuse one connection across
several operations, wrap them in `async with mcp_provider.client:`; FastMCP
supports nested client contexts. `close()` releases the client and clears setup
state. Setup and shutdown belong to application lifecycle management; do not
close the shared client while requests are active.

The [backend provider](../services/agent/app/providers/backend.py) calls the Go
`SaveUserMemory` JSON-RPC procedure. It returns the new memory version, checks the
returned user and version, and raises `MemoryConflictError` on a version conflict.
HTTP, protocol, and storage failures propagate without retries. Its lifecycle
follows the same `setup()`/`close()` pattern.

Providers do not manage chat history or orchestrate tools.

## Capabilities and internal tools

[Capabilities](../services/agent/app/capabilities.py) is created once per agent
run with a trusted `user_id`, an asynchronous `save_summary(user_id, summary)`
callback, and the catalog fetched through `mcp_provider.list_tools()`.
Provider instances remain shared; user context and tool routing belong to each
Capabilities instance.

`definitions` combines local and MCP tools into one list accepted by
`llm_provider.tool_call()`. Duplicate names are rejected during construction.
`call(name, arguments)` dispatches parsed arguments to the local function or MCP
provider. Both return FastMCP's `CallToolResult`, preserving content blocks,
structured content, and `is_error`. Unknown tools and invalid local arguments
return error results; storage and transport failures propagate without retries.
MCP tool arguments are validated by the server.

Internal tools follow `app/tools/<tool_name>/tool.py`. Functions are explicitly
imported and listed in `tools/__init__.py`'s `__all__`; Capabilities registers
their schemas and binds execution context explicitly, without auto-discovery.
`Agent.step()` parses model arguments and formats results as tool messages.

### User memory

The [remember tool](../services/agent/app/tools/remember/tool.py) accepts only a
non-empty `summary`: the complete replacement text for the user's long-term
memory, retaining relevant earlier facts. Pydantic validates its arguments and
rejects extra fields, including any model-supplied `user_id`.

The tool awaits the injected save callback before confirming success. The DBOS
workflow connects it to the [Go backend](backend.md#user-memory-concurrency),
using the memory version supplied with the queued request and updating that
version after each successful save. Conflicts fail the run. Separate Capabilities
instances isolate request context; the backend's version check prevents lost updates.

The caller loads the current summary before a run and includes it in the supplied
messages as user context alongside system rules for its use. The agent does not
read application tables.
