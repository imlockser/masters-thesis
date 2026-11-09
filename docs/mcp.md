# MCP service

The [MCP service](../services/mcp/app/main.py) provides a FastMCP server over
Streamable HTTP. It registers the four tools below and is not part of Compose.
The agent's [MCP client provider](agent.md#mcp-provider) discovers and executes
tools from the [DBOS workflow](agent-runtime.md).

The service also owns [solar forecasting](solar-forecast.md): data sources,
a shared Registry provider, and lifespan tasks at `x:00` for models and `x:05`
for cached predictions. Data-source modules live under `app/data_sources`.

| Tool | Purpose |
| --- | --- |
| `forecast_solar_generation` | Cached model forecast with hourly energy and SHAP explanations. |
| `find_best_solar_window` | Consecutive whole hours with the most forecast solar energy; earliest wins ties. |
| `get_electricity_prices` | Published DE-LU day-ahead wholesale prices, including negative prices. |
| `get_generation_mix` | Germany's reported public net generation by source, excluding industrial self-supply. |

The solar search reads the same current-release cache as the forecast tool,
using the same future-hour rules. It accepts a timezone-aware search interval of
at most 48 hours and an integer duration from 1 to 48 hours within that interval.
It optimizes nationwide generation, not household output or electricity prices.

Tool descriptions expose the temporal contracts to the agent through `tools/list`:
DE-LU price history begins on 2018-10-01; German generation history begins on
2015-01-01, with source-dependent gaps. The seven-day limit is per API-tool call,
not a history-retention limit. Generation contains reported past intervals only
and may lag by hours. Tomorrow's prices require published next-day auction results;
they are not a rolling 48-hour price forecast. Both solar tools use only the current
48-hour forecast release and retain no historical archive. Exact latest API
timestamps vary with publication and are not fixed historical dates.

## Energy-Charts provider and cache

`providers/energy_charts.py` owns one `EnergyChartsProvider` and HTTPX async client
per MCP process, initialized and closed by the existing lifespan. The public
[Energy-Charts API](https://api.energy-charts.info/) needs no API key or additional
environment settings. Its v2 responses carry units, timestamps, resolution and
license attribution. Prices are wholesale EUR/MWh, not household retail tariffs.

Both API tools accept timezone-aware `start_time` (inclusive) and `end_time`
(exclusive), at most seven days. Boundaries must match source intervals, currently
15 minutes; older hourly data requires whole-hour boundaries. The provider fetches
complete Europe/Berlin calendar days in one request and selects the requested
interval locally. A cached covering date range also serves smaller requests.
Missing intervals fail explicitly; missing values for individual generation
sources remain null and prevent a complete energy total for that source.

The shared in-memory cache holds at most 32 responses. TTL is one hour for current
prices, ten minutes for current generation, and one day for historical ranges.
Incomplete/unpublished price responses expire after one minute. Concurrent
identical requests share a fetch. Cache misses are serialized per endpoint and
spaced by at least 31 seconds; HTTP 429 establishes a cooldown using `Retry-After`.
Calls with a long remaining cooldown return a readable error. Expired data is not
silently served after failures. No extra scheduled job is needed for these reads.

The cache is cleared on shutdown. Rate limits belong to the outbound IP and
endpoint, so multiple MCP processes require shared coordination before scaling.

## Configuration and startup

`app/settings.py` resolves `PROJECT_ROOT` to the repository root using its own
file location and loads `env/mcp.env`. Environment variables override this file.
Create the ignored `env/mcp.env` with all eight required settings. Settings have
no defaults; paths can be absolute or relative to the repository root. Env files
contain assignments without comments. A local configuration is:

```dotenv
MCP_NAME="Energy Forecasting MCP"
MCP_HOST=127.0.0.1
MLFLOW_TRACKING_URI=http://127.0.0.1:5001
SOLAR_MODEL_NAME=solar_generation
SOLAR_MODEL_ALIAS=champion
SOLAR_CAPACITY_FILE=data/raw/2022-01-01_2026-10-18/mastr.parquet
SOLAR_CAPACITY_SNAPSHOT_DATE=2026-10-18
SOLAR_CACHE_DIR=data/predictions/solar
```

Run from the repository root:

```sh
uv run python -m services.mcp.app.main
```

FastMCP uses its default port `8000` and endpoint `/mcp`, giving the local address
`http://127.0.0.1:8000/mcp`. Stop the process with Ctrl+C.

## Tool layout and registration

Each tool owns a directory under `app/tools/`:

```text
app/
  main.py
  mcp.py
  settings.py
  tools/
    __init__.py
    <tool_name>/
      __init__.py
      tool.py
```

Define the tool in `tools/<tool_name>/tool.py`, import the shared server with
`from ...mcp import mcp`, and register the function with `@mcp.tool`.
Import each tool module explicitly in `tools/__init__.py` so startup registers
it. Keep supporting modules inside the tool's directory when its logic needs
splitting. No placeholder tools or automatic discovery are included.

Domain prediction logic lives under `app/solar`; tool-specific interfaces live
under `app/tools`. Pydantic solar models implement deterministic `render()`
methods. Tools return rendered text in `content` and separate numerical artifacts
in `structuredContent`; only text is forwarded to the LLM.
The three additional tools also use deterministic `render()` methods. Price
context is grouped by local delivery day, with average/minimum/maximum prices,
negative-price duration, and labelled delivery intervals. Generation context is
grouped by day and source, with energy, mean power, interval-average peaks, and
explicit missing-data coverage. Solar-window context separates search conditions,
the selected interval, and interpretation. Text omits licensing, URLs and internal
cache/schema identifiers. API license metadata is not serialized into tool results;
the data source is documented in the Energy-Charts provider section above.
The solar tool leads with total forecast energy in MWh for the requested interval;
hourly forecasts and SHAP explanations follow as details.

`readOnlyHint: true` describes the tools as read-only operations to MCP clients.
It is metadata, not an access-control mechanism. API calls may populate the local
response cache but do not modify source data.

## Verification

Run `uv run python -m pytest tests/test_energy_charts.py tests/test_solar.py tests/test_mcp.py`.
Tests call all three additional tools through an in-process MCP client. They check
repeated cached results and HTTP request counts, concurrent cache misses, TTL
expiry, rate-limit cooldown, DST days, incomplete data, malformed responses, and
solar-window selection against published Parquet forecasts. External API calls
are replaced with HTTPX mock transports.
