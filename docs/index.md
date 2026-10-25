# Documentation

Documentation describes the current implementation and stays synchronized with
the code. It contains no change history. Unresolved decisions are recorded in
the open-questions document.

| Document | Scope |
| --- | --- |
| [Local infrastructure](infrastructure.md) | Docker Compose services, their connections, configuration, data storage, and startup. |
| [Agent](agent.md) | Per-run agent state, individual steps, final response streaming, providers, and tools. |
| [Agent runtime](agent-runtime.md) | DBOS worker, queue contract, streamed events, completion, and failures. |
| [MCP service](mcp.md) | Server startup, environment configuration, and tool layout and registration. |
| [Solar forecasting](solar-forecast.md) | Registry refresh, 48-hour inference, hourly jobs, current-release cache, and rendered tool results. |
| [Backend](backend.md) | Go storage, migrations, frontend chat RPC procedures, versioned memory, and server startup. |
| [Agent API](agent-api.md) | SendMessage, five-turn context, DBOS dispatch, SSE replay, and result persistence. |
| [Frontend](frontend.md) | React chat interface, local user configuration, RPC/SSE integration, and development commands. |
| [Open questions](open-questions.md) | Deferred design decisions and when to revisit them. |
