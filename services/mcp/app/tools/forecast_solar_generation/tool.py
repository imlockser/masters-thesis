import asyncio
from datetime import UTC, datetime, timedelta

from fastmcp.tools import ToolResult
from pydantic import AwareDatetime, ValidationError

from ...mcp import mcp
from ...solar.cache import forecast_cache
from ...solar.models import ForecastResult, ForecastWindow


@mcp.tool(
    output_schema=ForecastResult.model_json_schema(mode="serialization"),
    annotations={"readOnlyHint": True},
)
async def forecast_solar_generation(
    start_time: AwareDatetime, end_time: AwareDatetime
) -> ToolResult:
    """Return Germany's total forecast solar energy for the requested interval.

    The primary result is the interval's total energy in MWh, followed by hourly
    power, energy, and SHAP explanation details.

    Times must include a timezone and map to whole UTC hours. Start is inclusive,
    end exclusive, with at most 48 hours and complete coverage. Before :05 UTC,
    start may be the current hour; from :05 it must be the next hour or later.
    The tool reads only the current release and never triggers inference. Release
    age does not reject a covered interval. Text content is the complete model-facing result;
    structured content is a separate numerical artifact.
    """

    try:
        window = ForecastWindow(start_time=start_time, end_time=end_time)
    except ValidationError as error:
        message = "; ".join(item["msg"] for item in error.errors())
        return ToolResult(
            content=f"Invalid forecast interval: {message}", is_error=True
        )

    now = datetime.now(UTC)
    earliest_start = now.replace(minute=0, second=0, microsecond=0)
    if now.minute >= 5:
        earliest_start += timedelta(hours=1)

    if window.start_time < earliest_start:
        return ToolResult(
            content=(
                "Invalid forecast interval: start_time must be at or after "
                f"{earliest_start:%Y-%m-%dT%H:%M:%SZ}. "
                "The earliest allowed hour advances at :05 UTC."
            ),
            is_error=True,
        )

    try:
        result = await asyncio.to_thread(forecast_cache.read, window)
    except ValidationError:
        return ToolResult(
            content="The current solar forecast contains invalid data.", is_error=True
        )
    except ValueError as error:
        return ToolResult(content=f"Solar forecast unavailable: {error}", is_error=True)
    except (OSError, KeyError):
        return ToolResult(
            content="The current solar forecast could not be read.", is_error=True
        )

    return ToolResult(
        content=result.render(), structured_content=result.model_dump(mode="json")
    )
