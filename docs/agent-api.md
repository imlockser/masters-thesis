# Agent API

The Go backend connects application storage to the Python DBOS worker. It owns
request preparation and result persistence; the agent receives a complete context
snapshot and accesses the backend only through `SaveUserMemory`.

| Entry point | Purpose |
| --- | --- |
| `POST /rpc`, method `SendMessage` | Save a user request, enqueue the agent, and return its run ID. |
| `GET /agent/runs/{run_id}/events?user_id=...` | Immediately open an SSE connection and replay/follow agent events. |

Both are local development interfaces on `127.0.0.1:8081`. The caller supplies
`user_id` as RPC parameters or an SSE query parameter. Ownership is checked against
the chat; this is not authentication. ZITADEL is not connected. Frontend chat
creation, listing, conversation reads, renaming, and deletion use the
[chat procedures](backend.md#chat-procedures).

## Submission and context

```json
{
  "jsonrpc": "2.0",
  "id": "send-1",
  "method": "SendMessage",
  "params": {
    "request_id": "00000000-0000-0000-0000-000000000002",
    "chat_id": "00000000-0000-0000-0000-000000000001",
    "user_id": "example-user",
    "content": "Forecast Germany's solar generation for tomorrow."
  }
}
```

All four parameters are required. IDs must be UUIDs; user and content must be
non-empty. Unknown fields are rejected. History length is fixed at five completed
agent answers, including each turn's user request, assistant tool calls, and tool
results. Fewer turns are returned when less history exists; incomplete turns are
excluded. The caller cannot choose a different window.

Preparing a run locks its chat, checks ownership and pending work, reads history
and memory, and writes the current user message and `agent_runs` record in one
application transaction. The snapshot stores ordered messages, user identity,
memory text/version, and submission time. Dispatch adds system instructions and
UTC submission time, followed by explicitly marked memory context, historical
turns, and the new user message. The memory text is context, not instructions.

The backend uses one shared Go DBOS client against `agent_dbos_sys`, application
`energy-agent`. It enqueues `agent_request` on queue `agent` with one portable
JSON argument, matching the [worker contract](agent-runtime.md#queue-contract).
The Python worker owns DBOS schema initialization and queue registration; the Go
client does not launch a worker or run DBOS migrations.

The JSON-RPC result is returned after enqueue, without waiting for generation:

```json
{
  "run_id": "00000000-0000-0000-0000-000000000002",
  "events_url": "/agent/runs/00000000-0000-0000-0000-000000000002/events?user_id=example-user"
}
```

`request_id` is also the DBOS workflow ID. Repeating the same request returns the
same run without another user message or workflow. Reusing it with another chat
or content is rejected. A pending run blocks another request for its chat;
different chats can run concurrently.

| JSON-RPC code | Meaning |
| --- | --- |
| `-32602` | Invalid or missing parameters. |
| `-32002` | Missing or foreign-owned chat. |
| `-32003` | Another run is pending in this chat. |
| `-32004` | Request ID reused for different input. |
| `-32603` | Infrastructure failure; retry with the same request ID. |

Application storage and DBOS are separate databases. If dispatch fails after the
application transaction commits, the prepared run remains pending. Repeating
`SendMessage` with the same ID or restarting the backend dispatches that snapshot
under the same workflow ID. Existing workflows are retrieved without restarting
them, including failed workflows; the agent retains its no-recovery policy.

## SSE lifecycle

Open the returned `events_url` immediately; no status polling is required.
The server checks ownership, returns `text/event-stream`, and flushes a connection
comment before waiting for events. Keepalive comments are sent every 15 seconds.
The normal HTTP write timeout is disabled for the SSE connection.

Events retain the worker's JSON payload and event type. SSE `id` equals the
zero-based `sequence`:

```text
id: 2
event: text_delta
data: {"sequence":2,"type":"text_delta","message_index":3,"text":"Forecast "}

```

Events include provisional text, completed messages, tool activity, separate
artifacts, and terminal results. See the [event contract](agent-runtime.md#event-stream).
Structured artifacts are never inserted into LLM message content.

On reconnect, send `Last-Event-ID` with the last processed sequence. The Go SDK
reads the durable stream from its beginning; the handler forwards only later
events. This also handles opening SSE after workflow completion. PostgreSQL
notifications wake stream readers; the SDK retains bounded checks for missed
notifications and workflow termination. No application status-polling loop is used.

A client disconnect stops its reader without canceling the workflow or result
persistence. `run_completed` is forwarded after the backend has stored the result.
The client closes EventSource after `run_completed` or `run_failed`. A backend
shutdown closes its readers without canceling Python workflows.

Before opening SSE, invalid parameters return HTTP 400, missing/foreign runs
return 404, and stream initialization failures return 503. An infrastructure
failure after opening emits `stream_error` without an event ID; reconnect using
the last processed ID. A terminated workflow without a terminal event produces
`run_failed` with `MissingTerminalEvent` after its outcome is reconciled.

## Persistence and recovery

An independent observer follows every dispatched run. On workflow completion,
the backend validates and inserts all new assistant/tool messages in their LLM
order, stores structured artifacts in `agent_runs.artifacts`, and marks the run
completed in one application transaction. The current user message is already
stored. A locked completion check makes repeated observers/SSE replay idempotent.
Text deltas and individual message events are not separately inserted.

A failed workflow marks the run failed and releases the chat. Its incomplete user
turn remains outside history; committed tool effects such as memory updates are
not rolled back. Invalid results are rejected atomically and marked failed.
Infrastructure failures leave the run pending for retry/reconnection or startup
reconciliation. Backend startup dispatches/reconciles pending records using their
original IDs and snapshots. S3 persistence is not implemented; artifacts remain
in application storage and DBOS results/events.
