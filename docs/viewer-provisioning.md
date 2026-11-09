# Viewer provisioning

`POST /internal/zitadel/events` receives Actions V2 JSON events. It uses the
specific Target's signing key, independently of user OIDC and worker run grants.
The endpoint reads at most 1 MiB and verifies `ZITADEL-Signature`:
`t=<Unix seconds>,v1=<hex HMAC-SHA256(key, timestamp + "." + raw body)>`.
Comparison is constant-time; past and future timestamps must be within five
minutes. Payloads and credentials are not logged.

Only `event_type: user.human.added` is processed. `aggregateID` identifies the
new user; `userID` identifies the actor and is ignored. `aggregateType` must be
`user`; `resourceOwner` and `instanceID` must match configuration. Other signed
event types return 204 without API calls.

Before granting access, the backend retrieves the configured project through
`GET /management/v1/projects/{project_id}` and checks its ID and
`project.details.resourceOwner`. This implementation provisions users in the
organization that owns AI Platform. It lists authorizations filtered by the new
user and project, then calls
`/zitadel.authorization.v2.AuthorizationService/CreateAuthorization` with only
`viewer` if no authorization exists. API calls use the provisioner PAT.

Existing authorizations, including developer/admin, custom, inactive, and empty
assignments, are left unchanged. No Update/Delete/Activate API is called.
ZITADEL's unique user/org/project constraint prevents concurrent duplicates;
after conflicts or transient failures, the backend re-reads assignments before
trying again. There are at most four attempts within an eight-second request
budget. Duplicate delivery needs no local receipt or migration.

| Result | HTTP status |
| --- | --- |
| Provisioned, already assigned, or ignored event type | 204 |
| Invalid JSON or user event metadata | 400 |
| Missing/invalid signature or timestamp outside tolerance | 401 |
| Unexpected organization or instance | 403 |
| Body exceeds 1 MiB | 413 |
| Missing configuration, API failure, or project owner mismatch | 503 |

## Local configuration

All values belong in the existing ignored `env/backend.env`:

| Variable | Value/source |
| --- | --- |
| `ZITADEL_ISSUER` | Existing issuer, `http://localhost:8080`. |
| `ZITADEL_PROJECT_ID` | Existing AI Platform project, `398272774335889413`. |
| `ZITADEL_ALLOWED_ORG_ID` | AI Platform owner organization, `395721483181162500`. |
| `ZITADEL_INSTANCE_ID` | Local instance, `395721483181096964`. |
| `ZITADEL_PROVISIONER_PAT` | Existing PAT of `ai-platform-provisioner`. |
| `ZITADEL_ACTIONS_SIGNING_KEY` | `signingKey` returned when creating the specific Target below. |

The organization and instance IDs match the local project's stored metadata;
the project owner is also checked through the API for each processed event.
The provisioner needs `project.read`, `user.grant.read`, and `user.grant.write`.
In Console, use Users → Service Users → ai-platform-provisioner → Personal Access
Tokens. Use its existing PAT; if unavailable, create a new PAT there and paste it
only into the local env file. Missing values or `PASTE_`/`REPLACE_` placeholders
disable provisioning with 503; the other backend routes remain available.

Docker Desktop reaches the loopback backend through `host.docker.internal`.
ZITADEL 4.19.5 also blocks private destinations in its outbound HTTP denylist.
`env/zitadel.env` contains `ZITADEL_HTTPCLIENT_DENYLIST` with the default blocked
ranges except the current Docker host IP `192.168.65.254/32`. It retains all
other private, loopback, and metadata restrictions. If Docker's host IP changes,
recalculate this single-address exception. Compose and backend binding do not
need changes; the backend remains on `127.0.0.1:8081`.

Apply the prepared ZITADEL env configuration manually:

```sh
docker compose up -d --no-deps --force-recreate zitadel
```

## Target and Execution setup

Run these commands manually using an administrator PAT with
`action.target.write` and `action.execution.write`, such as an IAM_OWNER.
Keep it in a temporary local `ZITADEL_SETUP_PAT` shell variable; do not put it in
frontend configuration or substitute the backend API client secret.

Create the Target:

```sh
curl --fail-with-body -X POST http://localhost:8080/v2/actions/targets \
  -H "Authorization: Bearer ${ZITADEL_SETUP_PAT}" \
  -H 'Content-Type: application/json' \
  --data '{
    "name": "AI Platform viewer provisioning",
    "endpoint": "http://host.docker.internal:8081/internal/zitadel/events",
    "restWebhook": {"interruptOnError": true},
    "timeout": "10s",
    "payloadType": "PAYLOAD_TYPE_JSON"
  }'
```

Copy the returned `signingKey` into `ZITADEL_ACTIONS_SIGNING_KEY`. Reload
`env/backend.env` and restart the Go backend. Keep the returned `id` as Target ID.
Set the Execution, replacing `PASTE_TARGET_ID`:

```sh
curl --fail-with-body -X PUT http://localhost:8080/v2/actions/executions \
  -H "Authorization: Bearer ${ZITADEL_SETUP_PAT}" \
  -H 'Content-Type: application/json' \
  --data '{
    "condition": {"event": {"event": "user.human.added"}},
    "targets": ["PASTE_TARGET_ID"]
  }'
unset ZITADEL_SETUP_PAT
```

SetExecution replaces the target list for that condition; preserve any existing
targets if this instance already has an Execution for `user.human.added`.
Actions V2 must be enabled on the instance. No Target or Execution is created by
the backend.

## Verification and limits

Create a new human user in the allowed organization. In Console, open its
Authorizations and confirm `viewer` on AI Platform, then sign in as that user and
check that ListChats succeeds. Developer/admin assignments must remain intact.
Existing users are not backfilled. Unsigned POSTs to the webhook must return 401.
Go tests use stub APIs, dummy keys/PATs, repeated deliveries, concurrent callbacks,
conflicts with delayed visibility, and unavailable APIs.

Keep the backend running during registration. An unavailable backend or a failure
beyond its bounded retries is not guaranteed to be redelivered: the ZITADEL
4.19.5 event worker cancels interrupted executions. Check the failed execution
and assign the missing viewer role manually; this implementation adds no durable
retry queue. Never replay an expired signed HTTP request.

Protocol references: [4.19.5 signature implementation](https://github.com/zitadel/zitadel/blob/v4.19.5/pkg/actions/signing.go),
[Authorization API](https://github.com/zitadel/zitadel/blob/v4.19.5/proto/zitadel/authorization/v2/authorization_service.proto),
[Actions event setup](https://zitadel.com/docs/guides/integrate/actions/testing-event),
[execution worker](https://github.com/zitadel/zitadel/blob/v4.19.5/internal/execution/worker.go).
