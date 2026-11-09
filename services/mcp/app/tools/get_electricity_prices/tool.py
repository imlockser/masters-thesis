from fastmcp.tools import ToolResult
from pydantic import AwareDatetime, ValidationError

from ...mcp import mcp
from ...providers.energy_charts import EnergyData, energy_charts_provider


@mcp.tool(
    output_schema=EnergyData.model_json_schema(mode="serialization"),
    annotations={"readOnlyHint": True},
)
async def get_electricity_prices(
    start_time: AwareDatetime,
    end_time: AwareDatetime,
) -> ToolResult:
    """Get published day-ahead wholesale prices for Germany-Luxembourg.

    HISTORY: DE-LU records start on 2018-10-01 in Europe/Berlin. The seven-day
    limit is per call, not the depth of history. Older prices can be hourly.
    LATEST DATA: published prices normally cover today in Europe/Berlin.
    Tomorrow is available only after the next-day auction results are published,
    usually in the early afternoon CET. This is not a rolling 48-hour forecast.
    Never assume tomorrow's prices exist in the morning, or request later days.
    If a future period is unavailable, report that fact instead of repeatedly
    shifting the interval by an hour. Do not substitute historical prices.

    Use timezone-aware times, start inclusive and end exclusive, at most seven
    days per call. Boundaries must match the source intervals: currently 15
    minutes; use whole-hour boundaries for older hourly records.
    Prices are EUR/MWh, may be negative, and are not a household retail tariff.
    Whole days are cached across users; smaller intervals reuse a cached
    covering response. Individual historical periods can still have missing data.
    """
    try:
        result = await energy_charts_provider.prices(start_time, end_time)
    except ValidationError:
        return ToolResult(
            content="Invalid price interval or Energy-Charts response.", is_error=True
        )
    except ValueError as error:
        return ToolResult(
            content=f"Electricity prices unavailable: {error}", is_error=True
        )

    return ToolResult(
        content=result.render(), structured_content=result.model_dump(mode="json")
    )
