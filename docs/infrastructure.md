# Local infrastructure

[compose.yaml](../compose.yaml) defines the current development environment.
Application services run outside Compose. The standalone
[MCP service](mcp.md) runs separately.

## Services and connections

| Service | Purpose and dependencies | Host access |
| --- | --- | --- |
| PostgreSQL | Shared database server for the services. | `localhost:5432` |
| LiteLLM | LLM Gateway; stores model configuration in PostgreSQL. | `http://localhost:4000` |
| SeaweedFS | S3 storage for MLflow artifacts. | S3: `http://localhost:8333`; additional port: `23646` |
| MLflow | Experiment metadata in PostgreSQL, artifacts in SeaweedFS. | `http://localhost:5001` |
| ZITADEL | Identity service with its own database in PostgreSQL. | Through Traefik. |
| ZITADEL Login | Separate login interface that communicates with ZITADEL. | `http://localhost:8080/ui/v2/login/` |
| Traefik (`zitadel-proxy`) | Routes HTTP requests to ZITADEL and the login interface. | `http://localhost:8080` |

Within the Compose network, services address each other by service name; for
example, MLflow uses `http://seaweedfs:8333`. Published ports provide host access;
MLflow's internal port is `5000`.

Traefik routes `/ui/v2/login` to the login service, `/api` to ZITADEL with that
prefix removed, and other paths to ZITADEL. For `/`, the request path is rewritten
to `/ui/v2/login/`. The proxy uses the Docker socket to discover routes from
Compose labels.

LiteLLM, MLflow, and ZITADEL wait for PostgreSQL's healthcheck to pass. It checks
connections to every database listed in `REQUIRED_DBS`. The login service and
proxy wait for ZITADEL to become ready. SeaweedFS has no readiness check:
MLflow only waits for its container to start.

## Configuration and data

Each service receives settings from its corresponding file in `env/`.
ZITADEL and ZITADEL Login share `env/zitadel.env`.
These Compose env files are local and excluded from Git. After cloning, prepare five files separately:
`postgres.env`, `litellm.env`, `seaweedfs.env`, `mlflow.env`, and `zitadel.env`.

- `postgres.env`: initial database, PostgreSQL credentials, and `REQUIRED_DBS`.
  The initial database is `agent_dbos_sys`, used by the [DBOS agent worker](agent-runtime.md).
  [SQL initialization](../postgres/initdb/01-create-dbs.sql) adds `litellm`,
  `mlflow`, and `app`. The `app` database is for chats, messages, and user memory;
  its tables are managed by [backend migrations](backend.md). `REQUIRED_DBS`
  checks all four databases.
  ZITADEL initializes its own database through `start-from-init`.
- `litellm.env`: Gateway keys, connection to the `litellm` database, and model
  storage in the database.
- `seaweedfs.env`: S3 keys and the `mlflow-artifacts` bucket.
- `mlflow.env`: connection to the `mlflow` database, S3 endpoint and artifact
  bucket, and S3 credentials matching SeaweedFS.
- `zitadel.env`: PostgreSQL connection, initial credentials, master key, local
  URLs, and login service settings. The initial login client token is written to
  `/zitadel/bootstrap/login-client.pat` and read by the login service.

| Docker volume | Contents |
| --- | --- |
| `postgres_data` | All PostgreSQL databases. |
| `seaweed_data` | SeaweedFS data, including MLflow artifacts. |
| `zitadel_bootstrap` | Initial login client token; the login service mounts this volume read-only. |

PostgreSQL initialization runs only on empty storage. Changing SQL or initial
environment settings does not automatically update existing databases.

If `app` is missing from an existing PostgreSQL volume, create it while the
container is running and recreate services with the configured environment files:

```sh
docker compose exec postgres sh -c 'createdb -U "$POSTGRES_USER" app'
docker compose up -d
```

The first command is only needed if `app` does not already exist. A plain
`docker compose restart` neither reruns initialization nor applies env-file
changes. On fresh storage, `docker compose up -d` creates the database through
the initialization script automatically.

## Startup and shutdown

Docker with Compose and prepared local env files are required. Run commands from
the repository root:

```sh
docker compose config --quiet
docker compose up -d
docker compose ps
docker compose logs -f
```

MLflow installs `psycopg[binary]` and `boto3` at startup, requiring network access
to Python packages. Check service availability through container status and logs;
`up -d` alone does not confirm readiness.

To stop services while preserving data:

```sh
docker compose down
```

`docker compose down -v` deletes volumes along with databases, artifacts, and the
initial token. This option is unnecessary for a normal shutdown.

The environment is intended for local development: ZITADEL uses HTTP, and MLflow
allows all Host headers. The proxy port binds to `127.0.0.1`; other published
ports in Compose are not restricted to the loopback address.
