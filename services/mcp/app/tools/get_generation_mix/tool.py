from fastmcp.tools import ToolResult
from pydantic import AwareDatetime, ValidationError

from ...mcp import mcp
from ...providers.energy_charts import EnergyData, energy_charts_provider


@mcp.tool(
    output_schema=EnergyData.model_json_schema(mode="serialization"),
    annotations={"readOnlyHint": True},
)
async def get_generation_mix(
    start_time: AwareDatetime,
    end_time: AwareDatetime,
) -> ToolResult:
    """Get Germany's reported public net generation by energy source.

    HISTORY: German API records begin on 2015-01-01; coverage differs by source
    and some intervals are missing. The seven-day limit is per call, not the
    depth of history. Older available dates may be requested in separate calls.
    LATEST DATA: only already reported past intervals are available. Reporting
    can lag the current time by hours. Today can therefore be incomplete.
    Never request future hours or tomorrow: this tool has no forecast data.
    For a complete daily comparison, use completed past days. For future solar
    generation use forecast_solar_generation or find_best_solar_window instead.

    Use timezone-aware times, start inclusive and end exclusive, at most seven
    days per call, aligned to the source intervals (currently 15 minutes).
    This is public grid generation, excluding industrial self-supply. Text gives
    energy and average power per source; structured content keeps interval values.
    Missing source values stay null and never count as zero. Data is cached
    across users, and recent reports may be revised by the source.
    """
    try:
        result = await energy_charts_provider.generation_mix(start_time, end_time)
    except ValidationError:
        return ToolResult(
            content="Invalid generation interval or Energy-Charts response.",
            is_error=True,
        )
    except ValueError as error:
        return ToolResult(content=f"Generation mix unavailable: {error}", is_error=True)

    return ToolResult(
        content=result.render(), structured_content=result.model_dump(mode="json")
    )
