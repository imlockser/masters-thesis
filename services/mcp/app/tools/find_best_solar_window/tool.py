import asyncio
from datetime import UTC, datetime, timedelta
from math import fsum
from typing import Annotated

from fastmcp.tools import ToolResult
from pydantic import AwareDatetime, BaseModel, Field, ValidationError

from ...mcp import mcp
from ...solar.cache import forecast_cache
from ...solar.models import ForecastWindow


class BestSolarWindow(BaseModel):
    search_start_time: AwareDatetime
    search_end_time: AwareDatetime
    start_time: AwareDatetime
    end_time: AwareDatetime
    duration_hours: int
    total_energy_mwh: float
    average_power_mw: float
    forecast_issued_at: AwareDatetime
    release_id: str

    def render(self) -> str:
        return "\n".join(
            [
                "Germany — best consecutive solar generation window",
                "",
                "Search context:",
                f"Period: {self.search_start_time.astimezone(UTC):%Y-%m-%d %H:%M} to {self.search_end_time.astimezone(UTC):%Y-%m-%d %H:%M} UTC (end exclusive).",
                f"Required consecutive duration: {self.duration_hours} hours.",
                f"Forecast issued: {self.forecast_issued_at.astimezone(UTC):%Y-%m-%d %H:%M} UTC.",
                "",
                "Selected window:",
                f"Start: {self.start_time.astimezone(UTC):%Y-%m-%d %H:%M} UTC.",
                f"End: {self.end_time.astimezone(UTC):%Y-%m-%d %H:%M} UTC (exclusive).",
                f"Total forecast solar energy: {self.total_energy_mwh:,.2f} MWh.",
                f"Average forecast solar power: {self.average_power_mw:,.2f} MW.",
                "",
                "Interpretation:",
                "This window has the highest total forecast solar energy among the consecutive windows of the requested duration. Ties select the earliest window.",
                "The forecast covers nationwide generation, not household PV output, electricity prices or carbon intensity.",
            ]
        )


@mcp.tool(
    output_schema=BestSolarWindow.model_json_schema(mode="serialization"),
    annotations={"readOnlyHint": True},
)
async def find_best_solar_window(
    start_time: AwareDatetime,
    end_time: AwareDatetime,
    duration_hours: Annotated[int, Field(strict=True, ge=1, le=48)],
) -> ToolResult:
    """Find the consecutive hours with the most forecast solar energy in Germany.

    AVAILABILITY: future forecasts only, using the current cached release's 48
    hourly intervals. There is no historical archive. Both search boundaries
    must fit inside that release; the 48-hour search limit does not extend it.
    Before :05 UTC, the start may be the current whole UTC hour. From :05, start
    at the next whole UTC hour or later (05:26 UTC means 06:00 UTC or later).
    The release is refreshed hourly at :05 UTC if refresh succeeds.

    Search within the supplied start-inclusive, end-exclusive interval, at most
    48 hours, using timezone-aware whole-hour boundaries. The duration is an
    integer number of hours within that interval. Ties choose the earliest interval.
    Reads the current cached model forecast without API requests or inference.
    Maximizes nationwide solar generation, not electricity price or household PV.
    """
    try:
        window = ForecastWindow(start_time=start_time, end_time=end_time)
        if (
            isinstance(duration_hours, bool)
            or not isinstance(duration_hours, int)
            or not 1 <= duration_hours <= 48
        ):
            raise ValueError("duration_hours must be an integer from 1 to 48.")
        if timedelta(hours=duration_hours) > window.end_time - window.start_time:
            raise ValueError("The duration exceeds the search interval.")

        now = datetime.now(UTC)
        earliest = now.replace(minute=0, second=0, microsecond=0)
        if now.minute >= 5:
            earliest += timedelta(hours=1)
        if window.start_time < earliest:
            raise ValueError(f"start_time must be at or after {earliest.isoformat()}.")

        forecast = await asyncio.to_thread(forecast_cache.read, window)
        candidates = [
            forecast.hours[i : i + duration_hours]
            for i in range(len(forecast.hours) - duration_hours + 1)
        ]
        best = max(
            candidates, key=lambda hours: fsum(hour.energy_mwh for hour in hours)
        )
        energy = fsum(hour.energy_mwh for hour in best)
        result = BestSolarWindow(
            search_start_time=window.start_time,
            search_end_time=window.end_time,
            start_time=best[0].interval_start,
            end_time=best[-1].interval_end,
            duration_hours=duration_hours,
            total_energy_mwh=energy,
            average_power_mw=energy / duration_hours,
            forecast_issued_at=forecast.metadata.issued_at,
            release_id=forecast.metadata.release_id,
        )
    except ValidationError:
        return ToolResult(
            content="Invalid solar interval or cached forecast.", is_error=True
        )
    except ValueError as error:
        return ToolResult(
            content=f"Best solar window unavailable: {error}", is_error=True
        )
    except (OSError, KeyError):
        return ToolResult(
            content="The cached solar forecast could not be read.", is_error=True
        )

    return ToolResult(
        content=result.render(),
        structured_content=result.model_dump(mode="json"),
    )
