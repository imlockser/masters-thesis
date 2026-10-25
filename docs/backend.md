# Backend

The Go module in [services/backend](../services/backend) implements application
storage using GORM and PostgreSQL. Versioned SQL migrations use Goose. A local
JSON-RPC 2.0 HTTP server exposes chat procedures, `SaveUserMemory`, and
`SendMessage`, using jrpc2's HTTP bridge. [Agent requests](agent-api.md) connect
storage to the Python DBOS worker and expose its event stream through SSE.

## Configuration and migrations

The backend uses the `app` database. `postgres/initdb` creates databases;
Goose creates and evolves application tables. Opening a Store does not migrate
the database automatically.

Create the ignored `env/backend.env`, without comments, with these settings:

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | `app` |
| `DBOS_SYSTEM_DATABASE_URL` | `agent_dbos_sys`, matching the agent worker. |
| `PORT` | Backend HTTP port; defaults to `8081` when unset or empty. |

Credentials must match `env/postgres.env`. Commands read configuration from the
process environment; load it from the repository root:

```sh
set -a
source env/backend.env
set +a
cd services/backend
go run ./cmd/migrate
```

The migration command applies pending embedded SQL migrations and exits; running
it again leaves the schema unchanged. Use migrations for schema changes instead
of GORM AutoMigrate. The initial migration also defines a Down section, which
drops the application tables and their data; the command only applies Up.

`storage.Open(ctx, databaseURL)` opens and checks the connection pool, with at
most 20 open connections. Reuse the Store across concurrent requests and call
`Close()` at shutdown. Every operation accepts `context.Context` for cancellation.

## Data model

| Table | Purpose and relationships |
| --- | --- |
| `chats` | UUID ID, ZITADEL `user_id`, title, and creation time. |
| `messages` | Generated numeric ID, chat foreign key, role, JSONB payload, and creation time. Deleting a chat cascades to its messages. |
| `user_memory` | One summary per `user_id`, an incrementing version, and update time. Independent of chats. |
| `agent_runs` | Request/workflow UUID, chat and user-message references, context snapshot, pending/completed/failed state, structured artifacts, and completion time. Deleted with its chat. |

IDs and creation times are generated automatically. `user_id` is an external
identity, without a foreign key into ZITADEL's database. Indexes cover chat
ownership and message ordering within a chat. Chat deletion is permanent and
does not delete user memory.

`MessageContent` is the structured `content` argument to `AddMessage`: its
`content` field contains JSON text or a content-block array, with optional
`tool_calls` and `tool_call_id`. The role is supplied separately. The full
structure is stored in `messages.payload`; function-call arguments remain JSON
strings as returned by the LLM. Role-specific fields and function-call argument
objects are validated before insertion.

## Storage operations

All methods belong to `storage.Store` and receive `ctx` as their first argument.

| Method | Behavior |
| --- | --- |
| `CreateChat(userID, title)` | Returns the created chat. |
| `ListChats(userID)` | Returns owned chats, newest first by creation time and ID. |
| `GetChat(chatID, userID)` | Returns chat metadata, all stored messages, and the pending run ID when present. |
| `AddMessage(chatID, userID, role, content)` | Checks ownership and message order, then returns the inserted message. |
| `GetHistory(chatID, userID, n)` | Returns the last `n` completed turns, flattened into chronological LLM messages. |
| `RenameChat(chatID, userID, newTitle)` | Returns the updated chat. |
| `DeleteChat(chatID, userID)` | Permanently deletes an owned chat, messages, and run records; rejects pending runs. |
| `DeleteAllChats(userID)` | Atomically deletes owned chats and their messages/runs; rejects the operation if any selected chat has a pending run. |
| `GetUserMemory(userID)` | Returns summary and version; absent memory has empty summary and version `0`. |
| `SaveUserMemory(userID, summary, expectedVersion)` | Atomically saves the complete replacement summary and returns its new version. |

Missing and foreign-owned chats both return `ErrChatNotFound`. Invalid IDs,
empty users/titles/summaries, unsupported roles, and non-positive history limits
return `ErrInvalidInput`. Ownership checks and message insertion share a
transaction and a chat row lock, also protecting against concurrent deletion.
The caller must obtain `userID` from trusted context. The current local RPC
endpoint accepts the user ID from its trusted internal caller; ZITADEL
authentication is not connected.

## JSON-RPC

After loading the environment and applying migrations, start the Python
[agent worker](agent-runtime.md#local-startup) to initialize DBOS and register its
queue. Then run from `services/backend`:

```sh
go run ./cmd/server
```

The server listens only on `127.0.0.1`, using `PORT` or the default `8081`.
When changing the port, update `BACKEND_URL` in `env/frontend.env` and
`BACKEND_RPC_URL` in `env/agent.env` to match. Send HTTP POST requests to `/rpc`
with `Content-Type: application/json`. Bodies are limited to 1 MiB. Shutdown on
Ctrl+C or SIGTERM closes SSE readers and persistence observers, allows active
HTTP requests to finish, and releases shared clients and database pools. It does
not cancel queued/running Python workflows. The server does not apply migrations
at startup.

Example request:

```json
{
  "jsonrpc": "2.0",
  "id": "save-memory-1",
  "method": "SaveUserMemory",
  "params": {
    "user_id": "example-user",
    "summary": "Prefers day-ahead solar forecasts.",
    "expected_version": 0
  }
}
```

All three parameters are required; `expected_version` must be a non-negative
integer, including explicit `0` for the first save. Unknown parameter fields are
rejected. The result contains the saved `user_id`, `summary`, `version`, and
`updated_at`, preserving the request ID in the JSON-RPC response.

| Error code | Meaning |
| --- | --- |
| `-32602` | Invalid or missing parameters. |
| `-32001` | Memory version conflict; reload and reconcile before saving again. |
| `-32603` | Internal failure; database details are not returned. |
| `-32601` | Unknown method; low-level history/message operations are not exposed. |

This is an internal development endpoint without authentication. It must remain
loopback-only until caller authentication and user authorization are implemented.

## Chat procedures

Frontend chat operations use the same `POST /rpc` JSON-RPC envelope. All parameters
listed below are required; unknown fields are rejected.

| Method | Parameters | Result |
| --- | --- | --- |
| `CreateChat` | `user_id`, `title` | Created chat with generated `id` and `created_at`. |
| `ListChats` | `user_id` | Owned chats, ordered by `created_at DESC, id DESC`; `[]` when empty. |
| `GetChat` | `chat_id`, `user_id` | Chat fields, `messages`, and optional `active_run_id`. |
| `RenameChat` | `chat_id`, `user_id`, `new_title` | Updated chat. |
| `DeleteChat` | `chat_id`, `user_id` | `null` on success. |
| `DeleteAllChats` | `user_id` | `null` on success, including when no chats exist. |

Titles must be non-empty after trimming; chat IDs must be UUIDs and user IDs must
be non-empty. A missing or foreign-owned chat returns `-32002`; invalid parameters
return `-32602`. Deleting a pending chat returns `-32003`. Bulk deletion checks all
selected chats under row locks and deletes none if one is pending. User memory
is independent and survives chat deletion.

`GetChat.messages` contains the full stored conversation, ordered by message ID,
including user, assistant, and tool messages and incomplete turns. Each message
has `id`, `chat_id`, `role`, `payload`, and `created_at`. This UI view is separate
from the five-completed-turn history sent to the agent. Chat metadata, messages,
and pending run ID are read from one consistent snapshot. Empty messages are `[]`;
`active_run_id` is omitted when no run is pending. List and detail reads are not
paginated.

On opening a chat, the frontend can use `active_run_id` to connect to its
[SSE stream](agent-api.md#sse-lifecycle), including after reloading the page.
New user messages go through `SendMessage`; `AddMessage`, `GetHistory`, and
`GetUserMemory` remain internal storage methods.

## History and tool calls

A turn starts at a `user` message and ends with an `assistant` message without
tool calls. It can contain multiple rounds of assistant calls and tool results.
Each tool result must reference a pending call; duplicate results, reused call
IDs within a turn, and an assistant response before all results arrive are
rejected with `ErrInvalidOrder`.

History selects completed turns using one consistent database snapshot, then
orders all their messages by generated message ID. It excludes incomplete turns,
returns fewer turns when necessary, and returns an empty list for an empty chat.
`LLMMessage` serializes directly into role/content/tool-call fields without
database IDs or a payload wrapper. System instructions, current user input, and
user memory are added separately by the caller preparing the queued agent request.

`agent_runs` permits one pending run per chat; different chats can run concurrently.
Preparing and completing runs lock the chat row. `AddMessage` rejects direct
writes while a run is pending; chat deletion is also blocked. A failed run leaves
its user message as an incomplete turn, excluded from future history; another
request can then start.

## User memory concurrency

Read the summary and version together before a run. Save with that version:
version `0` creates the first record, and a successful save increments the
version. If another request has already changed the memory, the write returns
`ErrMemoryConflict` without replacing the stored summary. The caller must reload
the memory and reconcile it, rather than retrying the old summary blindly.

The Python [DBOS workflow](agent-runtime.md) connects `remember` to this procedure
through the backend provider. Its per-run callback carries the supplied memory
version, updates it after successful saves, and propagates conflicts without retries.

## Verification

Run from `services/backend` after loading `env/backend.env` as above:

```sh
go fmt ./...
go vet ./...
TEST_DATABASE_URL="$DATABASE_URL" go test ./...
```

PostgreSQL integration tests create randomly named `app_test_*` and
`backend_agent_test_*` databases,
migrate them, and remove them afterward. The supplied database user needs
permission to create databases; tests never migrate or clear the database named
in `TEST_DATABASE_URL`. Without this variable, integration tests are skipped.
Tests cover ownership, cascade deletion, complete-turn history and tool-call
ordering, version conflicts, cancellation, and ten concurrent independent chats.
RPC tests exercise HTTP/JSON-RPC request handling, required parameters, error
mapping, and the absence of internal storage procedures without requiring a database.
PostgreSQL-backed HTTP tests cover chat CRUD, ordered full history, pending run
IDs, ownership, atomic busy-chat deletion rejection, and cascade deletion.
Go/Python integration starts the real agent workflow with deterministic LLM/MCP
fixtures using the root `.venv`. It checks enqueue, live SSE and replay, five-turn
history, memory RPC, artifacts, disconnect persistence, prepared-run recovery, and
ten overlapping requests. It requires the Python environment from `uv sync`.
