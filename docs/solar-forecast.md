# Solar forecasting

The MCP service owns data sources, Registry access, solar inference, and its
cached forecast tool. Notebooks import data sources from
`services.mcp.app.data_sources` using the repository root.

| Module under `services/mcp/app` | Responsibility |
| --- | --- |
| `providers/registry.py` | Shared Registry client and validated active models per process. |
| `solar/contract.py` | Ordered 33-feature contract, units, semantics, and preprocessing policy. |
| `solar/inference.py` | Model validation, live weather, feature preparation, 48 predictions, and SHAP. |
| `solar/models.py` | Pydantic numerical results and deterministic hourly/combined `render()` methods. |
| `solar/cache.py` | Atomic publication and covered-interval reads of the current release. |
| `solar/jobs.py` | Two independent hourly tasks owned by the MCP lifespan. |
| `tools/forecast_solar_generation/tool.py` | MCP validation, cached reads, rendered text, and structured artifacts. |

## Configuration and startup

Create `env/mcp.env` with all settings listed in the
[MCP configuration](mcp.md#configuration-and-startup). Set the model, MaStR
path/snapshot date, and local URLs explicitly. Relative data paths resolve against
the repository root. Start PostgreSQL, SeaweedFS, and MLflow through Compose.

Register the pinned model selected in notebook 04 once:

```sh
uv run python -m services.mcp.app.solar.register models:/m-0eed3692206b4f9d8b3d82ac6dcca89c
uv run python -m services.mcp.app.main
```

Registration checks the training feature list/target, attaches the
explicit `inference_contract` Registry-version tag, and tests the model and
explainer before assigning the configured alias (`solar_generation@champion`
by default). Registration creates a new version; do not run it at every startup.
An incompatible version may remain registered, but is not assigned the alias.
Contract metadata declares the preprocessing used; it does not prove training
provenance or forecast accuracy.

The model loader uses MLflow's sklearn flavor to preserve the native LightGBM
object needed by SHAP. Downloads use the MLflow artifact proxy rather than
Docker-internal S3 URLs. Inference runs inside the MCP process.

## Lifespan and schedules

MCP attempts an initial Registry refresh, then starts two wall-clock UTC schedules:

| Time | Operation |
| --- | --- |
| Every hour at `x:00` | Resolve the alias and refresh the active model if its version changed. |
| Every hour at `x:05` | Calculate all 48 future hourly predictions and replace the cached release. |

These are background tasks in the persistent MCP process, not system crontab
entries or DBOS steps. Blocking downloads, computation, and file reads run in
threads. Each schedule waits for its previous invocation to finish; missed
invocations are skipped instead of overlapping or accumulating a backlog.
Times specify intended start times, not hard real-time execution guarantees.
Shutdown cancels the schedules, waits for active threaded work, then clears the
provider. A process restart resumes future schedule boundaries without replay.

A changed version is prepared separately: full contract equality, exact feature
order, native model type, finite day/night control predictions, and SHAP
additivity must pass. Only then is the active reference replaced. In-flight
calculations retain their previous bundle; old objects become collectible once
no calculation references them. Concurrent refreshes share a loading lock.
The active entry and solar bundle are frozen Pydantic models. The solar bundle
accepts the native LightGBM instance through `arbitrary_types_allowed`.

The forecast job takes the latest fully validated bundle available when it starts.
If a Registry update is still loading at `x:05`, it can use the old bundle. If
none is ready, the refresh fails and leaves the cached forecast unchanged. A
Registry outage also leaves existing cached reads available. Model promotion
changes predictions only after the next successful forecast publication.
The provider bounds MLflow HTTP calls to 30 seconds per request with no automatic
HTTP retries; failed refreshes are retried at the next hourly invocation.

## Calculation and time semantics

The horizon contains 48 hourly labels beginning at the next whole UTC hour.
Each label represents `[timestamp, timestamp + 1 hour)`. Training and inference
join sources on unchanged `timestamp_utc` values, without temporal shifts.

Live Open-Meteo forecasts use the ten coordinates and ten variables in the feature
contract, explicit units, equal-weight weather means, and four cloud-cover maxima.
All locations/hours, uniqueness, units, finite values, and physical ranges are
checked. Calendar features use Europe/Berlin, including DST; pvlib uses
its solar-position defaults and the Ineichen clear-sky calculation.

MaStR is read locally: the last capacity at or before issue time is held constant.
Its snapshot date, value timestamp, file hash, and capacity are recorded. Its
refresh is separate; snapshot age does not reject a prediction automatically.
No generation-history window or target lag enters this model.

For the exact same 48 input rows, calculate raw CF and tree-path-dependent SHAP.
Raw MW equals raw CF times fixed capacity. Only the lower zero bound is applied;
there is no upper clipping or night-time override. Full contributions retain
feature names, values, units, and MW attribution. Before publication, validate:

```text
SHAP base CF + all SHAP CF contributions = raw CF
base MW + all SHAP MW contributions + output adjustment = served MW
```

MW denotes average hourly power; MWh is power times the one-hour duration.
MW-scaled SHAP is conditional on the fixed capacity and does not separately
attribute the external multiplication. Explanations describe model attribution,
not physical causality or explanations of forecast error.

## Current-release cache

The default cache is `data/predictions/solar/`. Each completed release contains
`weather.parquet`, `forecast.parquet`, and `metadata.json`. Forecast Parquet rows
include numerical predictions, all feature values, and all `shap_*` contributions
in CF; capacity converts them back to MW. There are no production plot files.

Write and round-trip-check staged forecast data, publish its directory, then
atomically replace `latest.json`. Readers and publication share a process-local
lock. After successful replacement the previously pointed-to release is removed.
Cache reads and writes are restricted to its configured directory. Failures
before pointer replacement preserve the old current release. A crash can leave
unreferenced staging/completed directories; cleanup policy remains open.

There is exactly one current release, with no fallback search, merging, or age
rejection. A failed refresh preserves it. A reader checks only that the requested
hours are fully covered and returns the original issue time and source metadata.
The designation "current" means the most recently published result, not a
guarantee that upstream data is fresh. Multiple MCP writers are not supported.

## Tool and artifact contract

`forecast_solar_generation(start_time, end_time)` requires timezone-aware dates
which normalize to whole UTC hours. Start is inclusive, end exclusive; duration
must be positive and at most 48 hours. Missing caches or incomplete coverage
return tool errors. The tool never triggers inference or queries Registry.

The earliest allowed start advances every hour at `x:05` UTC, independently of
refresh completion. From `15:00` through `15:04:59.999999`, a request may start at
`15:00`; from `15:05` through `16:04:59.999999`, it must start at `16:00` or later.
Earlier starts return a validation error with the allowed lower bound. A request
for the current hour before `:05` includes that entire hour's forecast energy.
Complete cache coverage is still required, including when a refresh is delayed.

The primary result is `total_energy_mwh`: the sum of served hourly energy for
exactly the requested interval, calculated before display rounding. It excludes
hours outside the request and is computed from the selected rows rather than
stored as a release-wide total.

`content` contains `ForecastResult.render()`: the total forecast energy in MWh
at the top, region/time/units/issue-time context, followed by a
details section with each requested hour's forecast, baseline,
top three strictly positive and top three strictly negative SHAP contributions,
remaining sum, and output adjustment. Each selected contribution includes a
stable semantic label, actual feature value, and unit. Selection is per hour;
zero contributions are omitted and fewer than three are allowed. Numbers are
rounded only for rendering; artifacts preserve precision.

`structuredContent` serializes the Pydantic result with `total_energy_mwh`, all hourly contributions
and technical provenance. The Agent sends only text blocks to the LLM, retaining
this artifact separately by tool call ID. DBOS results/events carry artifacts
outside message history. Artifacts are returned without S3 persistence.

## Verification

Run from the root:

```sh
uv run python -m pytest
uv run ruff check
source .venv/bin/activate
pyright
```

Pyright needs the project's Python environment to resolve installed dependencies.
For Neovim, launch it from the activated environment or select `.venv/bin/python`
as the Python language server's interpreter. No project-specific lint settings are required.

Unit tests exercise Registry replacement/failure/concurrency, timezone validation,
DST features, weather quality, native LightGBM + SHAP, atomic cache replacement,
age-independent reads, rendering, MCP output separation, and agent artifacts.
Live integration uses the configured Registry, current MaStR file, and weather
API; it publishes a real current release. It is separate from network-free tests.
