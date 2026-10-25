# masters-thesis

Master's thesis project: an interactive agent chat for forecasting electricity
generation in Germany.

The repository includes local Docker Compose infrastructure, data preparation
modules and notebooks, a DBOS agent worker with tool execution and streamed events,
an MCP service with live solar inference, hourly model/forecast refresh and cached
forecast tools, a Go backend with chat storage, queued agent requests and SSE
responses, and a React chat frontend. Application authentication is not connected.

## Documentation

The [documentation index](docs/index.md) links to component descriptions and
setup instructions.

## Stack

Python for data, ML, MCP, and the ReAct agent; Go for the backend; PostgreSQL for
storage; LiteLLM for LLM inference; DBOS for queues; MLflow and SeaweedFS for model
artifacts. React, TypeScript, Vite, and Ant Design provide the frontend. Docker
Compose provides local infrastructure, including ZITADEL; application services
run as local processes.

## Working rules

- Discuss the approach and agree on a todo list before implementation; start only
  with explicit approval.
- Keep changes minimal and avoid speculative features.
- Use Pydantic for Python data structures and separate logical code sections with
  blank lines.
- Update documentation alongside architectural changes.
- Describe only the current implementation in all documentation, including
  notebook text. Omit change history and keep unresolved decisions in the
  open-questions document.
- Use `uv` for Python; run `uv run ruff check` and `pyright` after changes.
  Run `pyright` with `.venv` activated. Change dependencies only with separate approval.
- Use `pytest` for tests; run them with `uv run python -m pytest`.
- For Go, use `go fmt ./...`, `go vet ./...`, and `go test ./...` within the
  backend module. Database integration tests require `TEST_DATABASE_URL`.
- Use `pnpm` for the frontend; run its lint, tests, and build before completing changes.
- Keep local env files containing secrets out of Git.
- Declare every service setting in its env file; keep env files free of comments
  and do not maintain env templates.
- Use English for all project content, including documentation, descriptions,
  and comments.
