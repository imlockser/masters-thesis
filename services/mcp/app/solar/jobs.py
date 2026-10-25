import asyncio
import logging
from collections.abc import Callable
from contextlib import asynccontextmanager
from datetime import UTC, datetime, timedelta
from typing import cast

from fastmcp import FastMCP

from ..providers.registry import registry_provider
from ..settings import settings
from .cache import forecast_cache
from .inference import SolarModel, forecast, load_solar_model

logger = logging.getLogger(__name__)


def refresh_model() -> None:
    registry_provider.refresh(
        settings.solar_model_name, settings.solar_model_alias, load_solar_model
    )


def refresh_forecast() -> None:
    bundle = cast(SolarModel, registry_provider.get(settings.solar_model_name))
    result, weather = forecast(bundle)
    forecast_cache.publish(result, weather)


def next_run(now: datetime, minute: int) -> datetime:
    now = now.astimezone(UTC)
    target = now.replace(minute=minute, second=0, microsecond=0)

    return target if target > now else target + timedelta(hours=1)


async def hourly(minute: int, job: Callable[[], None]) -> None:
    while True:
        target = next_run(datetime.now(UTC), minute)
        await asyncio.sleep(max(0, (target - datetime.now(UTC)).total_seconds()))
        work = asyncio.create_task(asyncio.to_thread(job))

        try:
            await asyncio.shield(work)
        except asyncio.CancelledError:
            await work
            raise
        except Exception:
            logger.exception(
                "Scheduled %s failed; keeping the previous active result.", job.__name__
            )


@asynccontextmanager
async def lifespan(server: FastMCP):
    registry_provider.setup(settings.mlflow_tracking_uri)

    try:
        try:
            await asyncio.to_thread(refresh_model)
        except Exception:
            logger.exception(
                "Initial model load failed; cached forecast reads remain available."
            )

        tasks = [
            asyncio.create_task(hourly(0, refresh_model)),
            asyncio.create_task(hourly(5, refresh_forecast)),
        ]

        try:
            yield {}
        finally:
            for task in tasks:
                task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
    finally:
        registry_provider.close()
