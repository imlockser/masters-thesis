# Open questions

- Add a common definition and registration mechanism for internal agent tools so
  Capabilities can consume their schemas and executors as easily as FastMCP tools.
  The current explicit registration is sufficient for the single `remember` tool;
  revisit this when adding more internal tools.

- Evaluate +1…+48-hour accuracy with weather runs available at each historical
  issue time. Historical Forecast API stitches initial forecast hours; existing
  annual scores do not establish live lead-time accuracy. Check Previous/Single
  Runs coverage and collect live releases for evaluation.
- Compare history-window/lagged-generation models with the current weather-based
  LightGBM on identical time splits; verify availability of every lag at issue time.
- Define MaStR refresh cadence and acceptable snapshot age. Currently the local
  operating-fleet snapshot is held constant; it is not a point-in-time historical fleet.
- Define how long a failed-refresh cache should remain usable. The last
  published release remains current regardless of age; only requested coverage
  is checked. There are no fresh/stale thresholds or fallback releases.
- Add S3 persistence and lifecycle rules for structured tool artifacts, forecast
  evaluation archives, and orphaned cache directories after crashes. Current
  successful updates remove the previously active cached release.
- Add request-specific inference when a real model requires it; define execution
  limits and native-model thread safety before exposing concurrent calls. The
  shared Registry provider supports named models and preparation callbacks.
- Coordinate schedules/cache publication when running multiple MCP processes.
  Singleton state and locks are currently per process; two replicas would each
  run both hourly jobs.
- Connect ZITADEL registration/login to the frontend and token validation to RPC
  and SSE. Replace the configured local user ID with verified identity and give
  the agent worker authenticated service access to `SaveUserMemory`.
- Define cloud deployment; application services currently run as local processes.
