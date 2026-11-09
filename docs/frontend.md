# Frontend

The root `frontend/` directory contains a React/TypeScript application using Vite,
pnpm, and Ant Design. A sidebar manages chats; the main view shows conversation
history, tool activity, and streamed assistant responses. The interface uses
English, with UTC time context for forecasts.

| Module under `frontend/src` | Responsibility |
| --- | --- |
| `App.tsx` | Ant Design light/dark theme and the persisted browser theme preference. |
| `auth/oidc.ts`, `auth/AuthGuard.tsx` | ZITADEL OIDC configuration and access-token readiness before mounting the application. |
| `auth/useAccessToken.ts` | Stable getter for the current OIDC access token, shared by RPC and SSE. |
| `api/types.ts` | Backend chat/message shapes and the agent event contract. |
| `api/rpc.ts` | Typed JSON-RPC calls, current OIDC access token, and backend error handling. |
| `api/events.ts` | Fetch-based SSE, bearer headers, event decoding, resume cursor, reconnect, and cancellation. |
| `chat/useChat.ts` | Chat selection, CRUD, request submission, context recovery, and stream lifecycle. |
| `chat/run.ts` | Ordered, duplicate-resistant event application to one run's temporary messages. |
| `chat/ChatPage.tsx` | Page layout, dialogs, composer, and loading/error states. |
| `chat/ChatSidebar.tsx`, `MessageList.tsx`, `MessageInput.tsx` | Chat controls, conversation/tool rendering, and keyboard input. |
| `chat/UserMenu.tsx` | OIDC profile display, confirmed bulk chat deletion, and OIDC logout in the sidebar footer. |

## Configuration and startup

Create the ignored `env/frontend.env`, without comments, with these settings:

| Variable | Local value | Use |
| --- | --- | --- |
| `BACKEND_URL` | `http://127.0.0.1:8081` | Vite development proxy target; not exposed as client configuration. |
| `VITE_ZITADEL_ISSUER` | `http://localhost:8080` | OIDC issuer. |
| `VITE_ZITADEL_CLIENT_ID` | React application's Client ID | Public OIDC client identifier. |
| `VITE_ZITADEL_PROJECT_ID` | `398272774335889413` | Project audience requested in the token. |

All settings are required. The Vite configuration loads only this env file directly;
variables already present in the process environment take precedence. Chats and
global memory belong to the authenticated token subject. The
development server binds to loopback.

Only `VITE_` settings are exposed to the browser. Access tokens are obtained at
runtime, never configured in env files. Vite's automatic dotenv loading is
disabled; configuration is kept in the repository's root `env/` directory.

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

## Authentication and token transport

`main.tsx` wraps the application in `AuthProvider` and `AuthGuard`.
ZITADEL login uses Authorization Code with PKCE. The guard waits for OIDC
initialization and navigation to finish, and requires authentication and an
access token before mounting the chat. The sidebar footer displays the OIDC
given/family name, falling back to name or preferred username, optional email, and an HTTP(S) picture or
initials. Missing claims use a neutral account label and icon. It makes no
additional profile requests and never decodes access tokens for display.

The full footer button opens a dropdown to its right with Delete all chats and Log out.
ChatPage owns the bulk confirmation so it persists when switching between the
desktop sidebar and mobile drawer. Bulk deletion shows a loading state; repeat submissions
and modal cancellation are blocked while the request is pending. The existing
DeleteAllChats callback clears the list, selection, and conversation only after
the backend confirms success. Errors keep the confirmation open and preserve
chat state. Logout uses `signoutRedirect` and reports redirect failures.
The footer remains visible while the chat list scrolls, in both the desktop
sidebar and mobile drawer. Long profile fields are truncated with ellipsis.

`useAccessToken` reads the current `react-oidc-context` state, including callbacks
retained across token renewal. `useRpc` and the SSE client use this getter before
each request or reconnect. Both `POST /rpc` and
`GET /agent/runs/{run_id}/events` include `Authorization: Bearer <access_token>`.
Calls without a token or before OIDC is ready are rejected locally. The
application adds no separate token storage and does not log tokens or include
them in request bodies or URLs.

The backend validates tokens through ZITADEL introspection. It derives identity
only from verified `sub`. RPC sends business parameters only, and SSE URLs contain
the run ID without a user query parameter. RPC rejects caller-supplied `user_id`.
The localStorage chat-selection key is a browser preference, not an identity;
restoring it requires the selected chat to appear in the authenticated chat list.

`api/events.ts` uses fetch streaming and AbortController. Native EventSource does
not support custom Authorization headers and is not used by the frontend.

## Conversation flow

`ListChats` populates the sidebar. Selection is remembered as a browser preference
in localStorage. Clicking the sidebar logo/title opens an unsaved draft, with a
New chat tooltip and keyboard button behavior; the composer is available even
when no chats exist. Only sending non-empty content calls CreateChat, followed by
SendMessage. The confirmed conversation appears in the sidebar. A failed send
retains its known created chat ID and request ID for retry. Renaming is available
from the button left of the header title and the chat's sidebar menu; both open
the same Rename chat dialog. Deletion requires confirmation. The backend rejects
deletion of pending chats, including bulk deletion.

New chat titles use the first submitted message. Whitespace is collapsed;
titles longer than 80 Unicode characters are shortened with an ellipsis,
preferring a word boundary. Manually assigned titles and later messages do not
trigger automatic renaming.

The top-right header button switches Ant Design's light/dark algorithm. The
browser remembers the selection under `energy-assistant:theme`, defaulting to
light. Custom backgrounds use theme tokens.
General borders and separators share the composer's muted border color in each
theme. Table gridlines retain their standard contrast, and the active composer
retains its blue focus.

`GetChat` loads the complete stored history. Its neutral loading indicator appears
only after 300 ms, keeping fast chat switches free of spinner flashes.
User/assistant text forms the main
conversation; assistant function calls have collapsible text results and running,
done, or error status. Markdown and tables use react-markdown and remark-gfm;
text newlines and `<br>` line breaks are rendered explicitly; other raw HTML is
not rendered. Code spans and fenced code preserve their original content.
Structured artifacts are not displayed as JSON.
Wide Markdown tables scroll horizontally inside a keyboard-focusable container
without widening the conversation. Cells are limited to 20rem and wrap long
text. Cell line breaks preserve inline formatting. Tables that fit require no scrolling.

Sending creates a UUID `request_id` and calls `SendMessage`. After acknowledgment,
the user message is shown locally and fetch opens the returned `events_url`.
Input is disabled while the selected chat is loading, submitting, or running.
The composer combines its borderless textarea and icon-only send button in one
container. Sending blurs it immediately; becoming ready after a response does
not restore focus. Typing a printable character outside editable controls focuses the
composer and appends it. Open dialogs and menus, keyboard shortcuts, and IME
composition are left alone. Space and Enter do not activate an empty composer.
With text present, Enter sends and Shift+Enter inserts a newline.

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

The SSE client reconnects after three seconds on network errors, HTTP 5xx,
`stream_error`, or EOF before a terminal event. `Last-Event-ID` contains the last
accepted sequence, including zero; incomplete frames are discarded and replayed
events are ignored. UTF-8 decoding spans network chunks, and the reader supports
LF/CRLF/CR lines, multiline data, and heartbeat comments.

HTTP 4xx, unexpected content types, missing tokens, and malformed events stop
the subscription with a readable error. Terminal agent events close the stream.
Switching chats or unmounting aborts the fetch, cancels its reader and pending
reconnect timer, and clears the temporary run state. The agent workflow continues.

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
artifact separation, bearer headers, missing-token/readiness guards, RPC error
handling, stable submission IDs, split SSE frames/UTF-8, reconnect with a renewed
token and resume cursor, duplicate events, terminal closure, and cancellation
without running application services. Manual browser verification should
cover chat CRUD, streamed text, recovery after reload, lost submission responses,
failures, and mobile layout. `tests/fixtures/agent_worker.py` provides deterministic
LLM/MCP responses for isolated integration checks; live inference uses the
configured gateway and MCP services.

To inspect token transport manually, sign in, open browser DevTools → Network,
and select a `/rpc` POST request. Under Request Headers, verify that Authorization
uses the Bearer scheme. Send a message and inspect the same header on
`/agent/runs/{run_id}/events`. Temporarily interrupt the stream; the reconnect
should carry Authorization and Last-Event-ID. After OIDC renews the token, new
requests and reconnects should use its current value. Do not copy token values
into logs or screenshots. No RPC or SSE calls should run before sign-in completes.
