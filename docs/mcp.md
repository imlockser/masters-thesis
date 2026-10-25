# MCP service

The [MCP service](../services/mcp/app/main.py) provides a FastMCP server over
Streamable HTTP. It registers `forecast_solar_generation` and is not part of Compose.
The agent's [MCP client provider](agent.md#mcp-provider) discovers and executes
tools from the [DBOS workflow](agent-runtime.md).

The service also owns [solar forecasting](solar-forecast.md): data sources,
a shared Registry provider, and lifespan tasks at `x:00` for models and `x:05`
for cached predictions. Data-source modules live under `app/data_sources`.

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
The solar tool leads with total forecast energy in MWh for the requested interval;
hourly forecasts and SHAP explanations follow as details.

`readOnlyHint: true` describes the forecast tool as a read-only operation to MCP
clients. It is metadata, not an access-control mechanism; the implementation reads
the cache without changing it.
