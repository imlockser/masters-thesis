# Agent runtime

The [worker](../services/agent/app/main.py) initializes shared providers and DBOS
against `agent_dbos_sys`. Queue `agent` executes workflow `agent_request`, with
up to ten concurrent runs per worker. Each run owns its Agent, Capabilities, and
memory version. There are no DBOS steps.

## Local startup

Create the ignored `env/agent.env` with every required setting below, without
comments. Match PostgreSQL credentials to `env/postgres.env`. URLs assume
host-based Python and Go processes connecting to Docker infrastructure.

| Variable | Value |
| --- | --- |
| `DBOS_SYSTEM_DATABASE_URL` | PostgreSQL URL for `agent_dbos_sys`. |
| `LLM_BASE_URL` | LiteLLM gateway URL. |
| `LLM_API_KEY` | Gateway API key. |
| `LLM_MODEL` | Model name configured in the gateway. |
| `MCP_URL` | MCP HTTP endpoint, locally `http://127.0.0.1:8000/mcp`. |
| `BACKEND_RPC_URL` | Backend endpoint, locally `http://127.0.0.1:8081/rpc`. |

All settings are required; there are no URL defaults in the settings model.

Start Compose and the [MCP service](mcp.md), then run from the repository root:

```sh
uv sync
uv run python -m services.agent.app.main
```

DBOS initializes its own system tables; application tables use Go migrations.
Start the [Go backend](backend.md#json-rpc) after the worker initializes DBOS and
registers queue `agent`. The backend URL is used only when `remember` is called.
Ctrl+C or SIGTERM stops the worker, allowing ten seconds for active workflows
before shutdown. The worker is currently a local process, outside Compose.

## Queue contract

Enqueue one JSON object as the workflow's sole argument:

```json
{
  "user_id": "example-user",
  "chat_id": "00000000-0000-0000-0000-000000000001",
  "messages": [{"role": "user", "content": "Forecast solar generation."}],
  "memory_version": 0
}
```

The Go [agent API](agent-api.md) validates ownership, stores the current user
message, and prepares the complete LLM history: instructions with UTC submission
time, current memory, the last five completed turns, and the new user request
last. `memory_version` accompanies that memory snapshot; use `0` when memory does
not exist. The agent reads no application tables. The backend enforces one pending
run per chat. After request validation, Agent receives the original portable JSON
messages, preserving tool-call lists and content blocks without SDK iterable
reserialization.

The workflow discovers MCP tools, builds Capabilities, and performs up to ten
agent steps. A response without tool calls completes the run. After ten steps
with tools, one generation without tools produces the final answer. The complete
run has a 180-second execution timeout; queue waiting is outside that timeout.
Provider transport failures and memory conflicts fail the workflow without retries.

The workflow returns `{"chat_id": "...", "messages": [...], "artifacts": [...]}`. Messages contain
only newly generated assistant and tool messages in LLM order, including the
final assistant response. Supplied history and the already stored user request
are excluded. Enqueuing returns a workflow handle/ID immediately, not this result.
Artifacts contain `tool_call_id`, tool `name`, and structured `data`, separately
from messages. They are not passed to the LLM or persisted to S3.

## Event stream

Each workflow writes portable JSON events to DBOS stream `events`. Inputs and
outputs also use portable JSON.
Every event has a zero-based `sequence`, matching its stream offset.

| Event type | Additional fields |
| --- | --- |
| `run_started` | `chat_id` |
| `generation_started` | `message_index` |
| `text_delta` | `message_index`, `text` |
| `message_completed` | `message_index`, full assistant `message` |
| `tool_started` | `tool_call_id`, `name` |
| `tool_completed` | `tool_call_id`, `name`, `message_index`, full tool `message`, separate `artifact` or null |
| `run_completed` | `result`, identical to the workflow return value |
| `run_failed` | `error`, the exception class name without infrastructure details |

`message_index` addresses the full agent history, including supplied messages.
Text arrives during generation, including text accompanying tool calls; it is
provisional until the corresponding message completes. A tool result may contain
`is_error` without failing the workflow. Infrastructure exceptions propagate.
Terminal events are followed by stream closure. Abrupt process or database
failure can prevent a terminal event from being written.

A Python caller uses native DBOS APIs in an async context:

```python
from dbos import DBOSClient, WorkflowSerializationFormat

client = DBOSClient(
    system_database_url=database_url,
    application_name="energy-agent",
    use_listen_notify=True,
)
handle = await client.enqueue_async(
    {
        "workflow_name": "agent_request",
        "queue_name": "agent",
        "serialization_type": WorkflowSerializationFormat.PORTABLE,
    },
    request,
)
async for event in client.read_stream_async(handle.workflow_id, "events", offset=0):
    # Forward events to the consumer; run_completed carries the final result.
    print(event)
```

Reuse the client for the caller's lifetime and destroy it at shutdown. Close an
interrupted stream with `contextlib.aclosing`. Resume at the last processed
`sequence + 1`. `use_listen_notify=True` enables PostgreSQL notifications for
stream waiting instead of repeatedly querying workflow status. DBOS's native
queue scheduler polls internally; this is separate from event delivery.

The Go backend uses portable arguments to enqueue this workflow and reads `events`
through the DBOS client. It persists the full result on completion, independently
of SSE connections. Completed-message events are forwarded without inserting
individual messages, avoiding duplicate persistence.

## Failure policy and verification

`max_recovery_attempts=0` disables rerunning an interrupted workflow after a worker
crash. A crash leaves partial events and may have already committed tool effects;
it does not roll them back. Do not manually restart such a run without reconciling
those effects. DBOS can mark an interrupted run `RETRIES_EXCEEDED` on recovery.
The caller must handle missing terminal events using workflow status when needed.

Run `uv run python -m pytest` for unit tests. To include PostgreSQL integration,
set `TEST_DBOS_DATABASE_URL` to a local PostgreSQL URL with database-creation
permissions. Tests create and remove an isolated `agent_test_*` database; they
do not modify the supplied database. Coverage includes ten overlapping queued
runs, live events before completion, failure propagation, streamed tool calling,
and versioned memory saves. Live model quality and per-delta storage performance
are not covered by these tests.
