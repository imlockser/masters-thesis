# Frontend

The root `frontend/` directory contains a React/TypeScript application using Vite,
pnpm, and Ant Design. A sidebar manages chats; the main view shows conversation
history, tool activity, and streamed assistant responses. The interface uses
English, with UTC time context for forecasts.

| Module under `frontend/src` | Responsibility |
| --- | --- |
| `api/types.ts` | Backend chat/message shapes and the agent event contract. |
| `api/rpc.ts` | Typed JSON-RPC calls, configured local identity, and backend error handling. |
| `api/events.ts` | EventSource subscription, reconnect status, event decoding, and terminal closure. |
| `chat/useChat.ts` | Chat selection, CRUD, request submission, context recovery, and stream lifecycle. |
| `chat/run.ts` | Ordered, duplicate-resistant event application to one run's temporary messages. |
| `chat/ChatPage.tsx` | Page layout, dialogs, composer, and loading/error states. |
| `chat/ChatSidebar.tsx`, `MessageList.tsx`, `MessageInput.tsx` | Chat controls, conversation/tool rendering, and keyboard input. |

## Configuration and startup

Create the ignored `env/frontend.env`, without comments, with both settings:

| Variable | Local value | Use |
| --- | --- | --- |
| `VITE_USER_ID` | `local-user` | Identity supplied with every RPC request; compiled into the client. |
| `BACKEND_URL` | `http://127.0.0.1:8081` | Vite development proxy target; not exposed as client configuration. |

Both settings are required. The Vite configuration loads this file directly;
variables already present in the process environment take precedence. All local
clients using the configured user ID share its chats and global memory. Login and
registration are not connected; the development server binds to loopback.

Start PostgreSQL and the application dependencies, the
[MCP service](mcp.md), the [agent worker](agent-runtime.md#local-startup), and the
[Go backend](backend.md#json-rpc). The worker initializes DBOS before the backend
connects. Configure an actual LiteLLM model and API key in `env/agent.env` for live
agent answers; application setup alone does not register an LLM in the gateway.

From the repository root:

```sh
pnpm --dir frontend install
pnpm --dir frontend dev
```

Open `http://127.0.0.1:5173`. Vite proxies `/rpc` and `/agent/` to the backend,
including SSE, so browser calls use relative URLs. Production builds are emitted
to `frontend/dist/`; deployment routing is not configured.

## Conversation flow

`ListChats` populates the sidebar. Selection is remembered in localStorage for the
configured user. `CreateChat` starts an empty conversation; renaming and deletion
use the matching RPC procedures. Deletion requires confirmation. The backend
rejects deletion of pending chats, including bulk deletion.

`GetChat` loads the complete stored history. User/assistant text forms the main
conversation; assistant function calls have collapsible text results and running,
done, or error status. Markdown and tables use react-markdown and remark-gfm;
raw HTML is not rendered. Structured artifacts are not displayed as JSON.

Sending creates a UUID `request_id` and calls `SendMessage`. After acknowledgment,
the user message is shown locally and EventSource opens the returned `events_url`.
Input is disabled while the selected chat is loading, submitting, or running.
Enter sends; Shift+Enter inserts a newline, while IME composition is preserved.

Each generation has a separate message keyed by `(run_id, message_index)`.
Text deltas append to that message; completed messages replace provisional text.
The index addresses the queued agent context, not the full displayed history.
Already processed `sequence` values are ignored. Text is displayed as received,
without a simulated typing timer. Automatic scrolling follows new output only
while the reader stays near the bottom.

On `run_completed` or `run_failed`, the stream closes and `GetChat` replaces the
temporary view with persisted messages. Failures are shown in a readable error
banner. The backend independently stores results; closing or switching the UI
does not cancel the workflow.

## Recovery

EventSource reconnects interrupted connections with its native `Last-Event-ID`.
Transport errors show reconnect status; a terminal stream error offers manual
recovery. Switching chats closes that reader and clears its temporary run state.

When `GetChat` returns `active_run_id`, the frontend confirms the same
`SendMessage` using that ID and the stored pending user message, then subscribes
from the beginning. This repairs an unconfirmed enqueue and reconstructs partial
output after page reload. Backend idempotency prevents another workflow or user
message. A lost submission response also retains the original request ID/content
for the Retry action; a JSON-RPC call ID is separate and changes on each call.

## Verification

```sh
pnpm --dir frontend test
pnpm --dir frontend lint
pnpm --dir frontend build
```

Vitest checks message assembly across generations, duplicate events, failures,
artifact separation, RPC error handling, stable submission IDs, and EventSource
closure/reconnect behavior without running application services. Browser checks
cover chat CRUD, streamed text, recovery after reload, lost submission responses,
failures, and mobile layout. `tests/fixtures/agent_worker.py` provides deterministic
LLM/MCP responses for isolated integration checks; live inference uses the
configured gateway and MCP services.
