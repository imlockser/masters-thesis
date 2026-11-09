# Authentication

The Go backend protects `POST /rpc` and `GET /agent/runs/{run_id}/events`
with `internal/auth`. React supplies the current ZITADEL access token in the
Authorization Bearer header. Verified project roles are available in request
context. User RPC and SSE require `viewer`, `developer`, or `admin` from the
configured project. [Viewer provisioning](viewer-provisioning.md) handles signed
ZITADEL registration events through a separate internal endpoint.

## Validation and identity

Every opening request calls the configured issuer's `/oauth/v2/introspect`
with a form-encoded token and HTTP Basic credentials of the AI Platform API
application. Both Basic credentials are form-encoded before Base64, as required
by [ZITADEL client_secret_basic](https://zitadel.com/docs/apis/openidoauth/authn-methods#client-secret-basic).
Tokens may be opaque or JWT; the backend does not decode untrusted token claims.

The trusted JSON response must have `active: true`, exact `iss`, future Unix
`exp`, the project ID in `aud`, and a non-empty `sub` without surrounding
whitespace or control characters. Audience can be a string or an array.
The introspection request has a five-second timeout, a bounded response body,
and no redirects. Validation is not cached and secrets are not logged.

| Condition | HTTP result |
| --- | --- |
| Missing/malformed Bearer header, inactive/expired token, wrong issuer/audience, missing subject | 401 with a Bearer challenge. |
| Connection failure, timeout, non-200 introspection response, malformed JSON/response | 503 with a generic message. |
| Valid token without an allowed project role | 403 Forbidden before RPC execution or SSE run lookup. |
| Valid token with an allowed project role | Original handler and ResponseWriter receive the request with verified `sub` and roles in typed context keys. |

An introspection HTTP 401 means the API credentials were rejected, so the backend
returns 503 rather than treating it as an invalid user token. Upstream response
bodies and errors are not exposed. Request cancellation reaches introspection;
its timeout does not become a deadline on a successful SSE connection.

RPC handlers and SSE storage lookups use only the context subject. RPC rejects
`user_id` as an unknown parameter. SSE URLs contain only the run ID, and query
parameters cannot determine identity. Chat ownership checks return not-found
for foreign data. Existing records retain their stored owners; no automatic
reassignment or data migration is performed.

## Project roles

Authentication reads `urn:zitadel:iam:org:project:{ZITADEL_PROJECT_ID}:roles`
from the trusted introspection response. If absent, it uses
`urn:zitadel:iam:org:project:roles`. In ZITADEL 4.19.5, introspection scopes
this legacy claim to the introspecting API application's project, so
**AI Platform API must belong to the configured AI Platform project**.
See [ZITADEL introspection](https://github.com/zitadel/zitadel/blob/v4.19.5/internal/api/oidc/introspect.go#L91)
and [role claims](https://zitadel.com/docs/guides/integrate/retrieve-user-roles).

An explicit project claim takes precedence, including an empty one. Claims for
other projects are ignored. Only `viewer`, `developer`, and `admin` with a
non-empty organization assignment are retained. Missing roles produce an empty
set; malformed selected role claims return 503. Roles from HTTP headers, query
parameters, or RPC parameters are never trusted.

`auth.Roles(ctx)` returns a copy of the verified roles; `auth.HasRole(ctx, role)`
checks exact membership without role inheritance. Middleware replaces any
existing role context. After authentication, `auth.RequireUserRole` requires at
least one allowed role for `/rpc` and user SSE. All three roles can call every
current user RPC method, limited to their own resources. Unknown roles and roles
from other projects grant no access. SSE checks run ownership independently of
roles, including `admin`.
Worker memory callbacks retain their separate run grant authorization.

## Local configuration

Open `env/backend.env`; its database and port settings are described in
[Backend](backend.md#configuration-and-migrations). All service env files live
in the root `env/` directory, are ignored by Git, and contain no comments.

| Variable | Value |
| --- | --- |
| `ZITADEL_ISSUER` | `http://localhost:8080` |
| `ZITADEL_PROJECT_ID` | `398272774335889413` |
| `ZITADEL_CLIENT_ID` | Client ID of **AI Platform API**, not the React application. |
| `ZITADEL_CLIENT_SECRET` | Client secret of **AI Platform API**. |

In ZITADEL Console, open the organization → Projects → AI Platform →
Applications → AI Platform API → Configuration. Use its Client ID and Client
Secret with authentication method Basic. If the existing secret is no longer
available, explicitly regenerate it in Console and update the local env file.
Do not put API credentials in frontend configuration or share them in chat.
See the [ZITADEL Basic Authentication guide](https://zitadel.com/docs/guides/integrate/token-introspection/basic-auth).

The backend reads process environment variables and does not load dotenv files.
From the repository root, with PostgreSQL and the DBOS worker running:

```sh
set -a
source env/backend.env
set +a
cd services/backend
go run ./cmd/server
```

Missing auth configuration or credential placeholders stop startup. Wrong real
credentials produce HTTP 503 on authenticated requests. Compose contains only
infrastructure services; it does not launch or configure the Go process.

## Verification and limits

Run `go test ./...`, `go vet ./...`, and `go build ./...` from `services/backend`.
Tests use dummy credentials and httptest introspection servers. With
`TEST_DATABASE_URL`, PostgreSQL tests create isolated databases and check that
forged user IDs cannot read, rename, delete, enqueue for, or overwrite another
user's chats, runs, or memory. Go/Python integration fixtures use dummy Bearer
tokens and a stub introspection server for user routes. Worker callbacks use
the run grant and the internal memory endpoint.

Without a token, both commands return 401 before RPC or run lookup:

```sh
curl -i http://127.0.0.1:8081/rpc -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"ListChats","params":{}}'
curl -i http://127.0.0.1:8081/agent/runs/00000000-0000-0000-0000-000000000001/events
```

For a valid token, sign in through the frontend and inspect Network in DevTools:
ListChats should return HTTP 200 and only the account's chats. Submit a forecast;
the events GET should open with HTTP 200 and `text/event-stream`. Reconnects
carry Authorization and Last-Event-ID and are authenticated again. Do not export
HAR files or screenshots containing credentials. An open stream is authenticated
once; tokens are not rechecked for each event.

## Worker memory callback

`POST /internal/agent/memory` accepts only `run_id`, `summary`, and
`expected_version`. Its Bearer credential is specific to that run; it cannot
invoke RPC methods. User RPC and SSE retain their ZITADEL middleware.

Go creates a random 32-byte nonce and derives a run secret using
HMAC-SHA256(WORKER_CALLBACK_KEY, run_id + ":" + nonce). The shared 64-hex-character
key lives only in `env/backend.env` and `env/agent.env`. The app database stores
the public nonce, SHA-256 hash of the secret, and a 24-hour expiry. DBOS receives
only the nonce, so its persisted workflow inputs contain no callback secret or
browser token. The worker derives the secret locally and sends it only in the
Authorization header. Keep both key values identical and stable across restarts.

The backend checks the hash in constant time, expiry, and pending run state,
then derives the owner from the stored chat. It locks the chat against concurrent
completion/deletion. A callback receipt and the existing memory CAS update commit
in one transaction. Repeating the same `(run_id, expected_version)` and normalized
summary returns the original version; different content or a stale unsaved
version returns 409. Completed/failed or expired grants return 401 and cannot
write. Deleting a run cascades its callback receipts.

Apply database migrations before starting the backend and worker. Queued
workflow inputs require `run_id` and `memory_grant_nonce`. Queue delay
counts toward the 24-hour grant lifetime. Rotating the shared key requires draining
pending runs first. Database/queue access and the shared key remain trusted.
