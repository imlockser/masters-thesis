import asyncio
from collections import defaultdict
from datetime import UTC, date, datetime, timedelta
from email.utils import parsedate_to_datetime
from math import fsum, isfinite
from time import monotonic
from typing import Annotated, Literal, Self
from zoneinfo import ZoneInfo

import httpx
from pydantic import (
    AwareDatetime,
    BaseModel,
    Field,
    field_validator,
    model_validator,
)

BERLIN = ZoneInfo("Europe/Berlin")
Endpoint = Literal["price", "public_power"]
Number = Annotated[float, Field(strict=True, allow_inf_nan=False)]


class EnergyWindow(BaseModel):
    start_time: AwareDatetime
    end_time: AwareDatetime

    @field_validator("start_time", "end_time")
    @classmethod
    def utc_quarter_hour(cls, value: datetime) -> datetime:
        value = value.astimezone(UTC)
        if value.minute % 15 or value.second or value.microsecond:
            raise ValueError("Time boundaries must be whole UTC quarter-hours.")
        return value

    @model_validator(mode="after")
    def positive_week(self) -> Self:
        if not timedelta(0) < self.end_time - self.start_time <= timedelta(days=7):
            raise ValueError("The interval must be positive and at most seven days.")
        return self


class EnergySeries(BaseModel):
    id: str = Field(min_length=1)
    name: str = Field(min_length=1)
    unit: str | None = None


class EnergyPoint(BaseModel):
    timestamp: AwareDatetime
    values: dict[str, Number | None]


class EnergyResponse(BaseModel):
    schema_version: Literal["2.0"]
    endpoint: Endpoint
    country: Literal["de"]
    bidding_zone: str | None = None
    timezone: Literal["Europe/Berlin"]
    unit: str
    interval_minutes: Annotated[int, Field(strict=True, gt=0, le=1440)]
    generated_at: AwareDatetime
    license: str = Field(min_length=1)
    series: list[EnergySeries] = Field(min_length=1, max_length=64)
    data: list[EnergyPoint] = Field(min_length=1, max_length=1000)

    @model_validator(mode="after")
    def consistent_series(self) -> Self:
        ids = {series.id for series in self.series}
        if len(ids) != len(self.series) or any(
            set(row.values) != ids for row in self.data
        ):
            raise ValueError("Energy-Charts returned inconsistent series.")
        step = timedelta(minutes=self.interval_minutes)
        for left, right in zip(self.data, self.data[1:]):
            if right.timestamp.astimezone(UTC) - left.timestamp.astimezone(UTC) != step:
                raise ValueError(
                    "Energy-Charts returned missing or unordered intervals."
                )
        return self


class EnergyData(EnergyResponse):
    license: str = Field(min_length=1, exclude=True)
    start_time: AwareDatetime
    end_time: AwareDatetime
    retrieved_at: AwareDatetime
    cached: bool

    def render(self) -> str:
        start = self.start_time.astimezone(BERLIN)
        end = self.end_time.astimezone(BERLIN)
        days: dict[date, list[EnergyPoint]] = defaultdict(list)
        for row in self.data:
            days[row.timestamp.astimezone(BERLIN).date()].append(row)

        title = (
            "Germany-Luxembourg — day-ahead wholesale electricity prices"
            if self.endpoint == "price"
            else "Germany — reported public net electricity generation"
        )
        text = [
            title,
            f"Period: {start:%Y-%m-%d %H:%M %z} to {end:%Y-%m-%d %H:%M %z} (end exclusive).",
            "Times use Europe/Berlin; UTC offsets distinguish daylight-saving transitions.",
        ]
        if self.endpoint == "price":
            text.append(
                "Prices are wholesale EUR/MWh, excluding household tariff charges and taxes."
            )
            for day, rows in days.items():
                prices = [(row, row.values["day_ahead_price"]) for row in rows]
                reported = [(row, value) for row, value in prices if value is not None]
                text.extend(["", f"{day.isoformat()} — electricity prices"])
                if len(reported) != len(rows):
                    text.append(
                        "Price data is incomplete; a complete daily summary is unavailable."
                    )
                elif reported:
                    lowest, low = min(reported, key=lambda item: item[1])
                    highest, high = max(reported, key=lambda item: item[1])
                    negative_hours = (
                        sum(value < 0 for _, value in reported)
                        * self.interval_minutes
                        / 60
                    )
                    text.extend(
                        [
                            f"Average price over the requested part of this day: {fsum(value for _, value in reported) / len(reported):,.2f} EUR/MWh.",
                            f"Lowest interval price: {low:,.2f} EUR/MWh, starting {lowest.timestamp.astimezone(BERLIN):%H:%M %z}.",
                            f"Highest interval price: {high:,.2f} EUR/MWh, starting {highest.timestamp.astimezone(BERLIN):%H:%M %z}.",
                            f"Time with negative prices: {negative_hours:,.2f} hours.",
                        ]
                    )
                text.append(f"{self.interval_minutes}-minute delivery intervals:")
                for row, value in prices:
                    left = row.timestamp.astimezone(BERLIN)
                    right = (
                        row.timestamp.astimezone(UTC)
                        + timedelta(minutes=self.interval_minutes)
                    ).astimezone(BERLIN)
                    finish = right.strftime("%H:%M %z")
                    if right.date() != left.date():
                        finish += f" on {right:%Y-%m-%d}"
                    price = (
                        f"{value:,.2f} EUR/MWh" if value is not None else "not reported"
                    )
                    text.append(f"  {left:%H:%M %z} to {finish}: {price}.")
        else:
            text.extend(
                [
                    "Scope: electricity delivered to the public grid; industrial self-supply is excluded.",
                    "Imports, load and storage consumption are excluded from the source breakdown.",
                    "These are reported observations, not a forecast. Energy is in MWh; power is in MW.",
                ]
            )
            groups = (
                [("Requested-period summary by source", self.data)]
                if len(days) > 1
                else []
            )
            groups.extend(
                (f"{day.isoformat()} — generation by source", rows)
                for day, rows in days.items()
            )
            for heading, rows in groups:
                text.extend(["", heading])
                for series in self.series:
                    points = [(row, row.values[series.id]) for row in rows]
                    reported = [
                        (row, value) for row, value in points if value is not None
                    ]
                    if len(reported) != len(rows):
                        text.append(
                            f"{series.name}: incomplete ({len(reported)}/{len(rows)} intervals reported). Total energy, mean power and peak are unavailable; missing values are not zero."
                        )
                        continue
                    values = [value for _, value in reported]
                    peak_row, peak = max(reported, key=lambda item: item[1])
                    energy = fsum(values) * self.interval_minutes / 60
                    text.append(
                        f"{series.name}: {energy:,.2f} MWh generated; mean power {fsum(values) / len(values):,.2f} MW; "
                        f"highest interval-average power {peak:,.2f} MW at {peak_row.timestamp.astimezone(BERLIN):%Y-%m-%d %H:%M %z}."
                    )
        return "\n".join(text)


class EnergyChartsProvider:
    def __init__(self) -> None:
        self._client: httpx.AsyncClient | None = None
        self._cache: dict[
            tuple[Endpoint, date, date], tuple[float, datetime, EnergyResponse]
        ] = {}
        self._locks: dict[Endpoint, asyncio.Lock] = {}
        self._next_request: dict[Endpoint, float] = {}

    def setup(self) -> None:
        if self._client is not None:
            raise RuntimeError("EnergyChartsProvider is already initialized.")
        self._client = httpx.AsyncClient(
            base_url="https://api.energy-charts.info",
            timeout=20,
        )
        self._locks = {
            endpoint: asyncio.Lock() for endpoint in ("price", "public_power")
        }

    @property
    def client(self) -> httpx.AsyncClient:
        if self._client is None:
            raise RuntimeError(
                "EnergyChartsProvider is not initialized. Call setup() first."
            )
        return self._client

    async def prices(self, start_time: datetime, end_time: datetime) -> EnergyData:
        return await self._read(
            "price", EnergyWindow(start_time=start_time, end_time=end_time)
        )

    async def generation_mix(
        self, start_time: datetime, end_time: datetime
    ) -> EnergyData:
        return await self._read(
            "public_power", EnergyWindow(start_time=start_time, end_time=end_time)
        )

    async def _read(self, endpoint: Endpoint, window: EnergyWindow) -> EnergyData:
        client = self.client
        first = window.start_time.astimezone(BERLIN).date()
        last = (window.end_time - timedelta(microseconds=1)).astimezone(BERLIN).date()

        async with self._locks[endpoint]:
            now = monotonic()
            self._cache = {
                key: entry for key, entry in self._cache.items() if entry[0] > now
            }
            for (kind, left, right), (_, retrieved, response) in self._cache.items():
                if kind == endpoint and left <= first and last <= right:
                    return self._select(response, window, retrieved, cached=True)

            delay = self._next_request.get(endpoint, now) - now
            if delay > 31:
                raise ValueError(
                    f"Energy-Charts rate limited; retry in {delay:.0f} seconds."
                )
            if delay > 0:
                await asyncio.sleep(delay)
            self._next_request[endpoint] = monotonic() + 31

            params = {"start": first.isoformat(), "end": last.isoformat()}
            params.update(
                {"bzn": "DE-LU"} if endpoint == "price" else {"country": "de"}
            )
            try:
                http_response = await client.get(f"/v2/{endpoint}", params=params)
                if http_response.status_code == 429:
                    retry = http_response.headers.get("Retry-After", "60")
                    try:
                        seconds = float(retry)
                    except ValueError:
                        try:
                            seconds = (
                                parsedate_to_datetime(retry) - datetime.now(UTC)
                            ).total_seconds()
                        except (TypeError, ValueError, OverflowError):
                            seconds = 60
                    seconds = max(31, seconds) if isfinite(seconds) else 60
                    self._next_request[endpoint] = monotonic() + seconds
                    raise ValueError(
                        f"Energy-Charts rate limited; retry in {seconds:.0f} seconds."
                    )
                http_response.raise_for_status()
            except httpx.HTTPError as error:
                raise ValueError(
                    "Energy-Charts is unavailable; try again later."
                ) from error

            response = EnergyResponse.model_validate(http_response.json())
            expected_unit = "EUR/MWh" if endpoint == "price" else "MW"
            if (
                response.endpoint != endpoint
                or response.unit.replace(" ", "") != expected_unit
                or (endpoint == "price" and response.bidding_zone != "DE-LU")
            ):
                raise ValueError("Energy-Charts returned an unexpected region or unit.")
            if endpoint == "price" and {s.id for s in response.series} != {
                "day_ahead_price"
            }:
                raise ValueError("Energy-Charts returned unexpected price series.")

            retrieved = datetime.now(UTC)
            ttl = (
                86400
                if last < retrieved.astimezone(BERLIN).date()
                else (3600 if endpoint == "price" else 600)
            )
            if endpoint == "price" and any(
                row.values["day_ahead_price"] is None for row in response.data
            ):
                ttl = 60
            # Missing future rows must not hide newly published prices for an hour.
            day_end = datetime.combine(
                last + timedelta(days=1), datetime.min.time(), BERLIN
            ).astimezone(UTC)
            if (
                endpoint == "price"
                and response.data[-1].timestamp
                + timedelta(minutes=response.interval_minutes)
                < day_end
            ):
                ttl = 60
            if len(self._cache) >= 32:
                self._cache.pop(next(iter(self._cache)))
            self._cache[endpoint, first, last] = (
                monotonic() + ttl,
                retrieved,
                response,
            )
            return self._select(response, window, retrieved, cached=False)

    def _select(
        self,
        response: EnergyResponse,
        window: EnergyWindow,
        retrieved: datetime,
        *,
        cached: bool,
    ) -> EnergyData:
        rows = [
            row
            for row in response.data
            if window.start_time <= row.timestamp < window.end_time
        ]
        step = timedelta(minutes=response.interval_minutes)
        if (
            not rows
            or rows[0].timestamp != window.start_time
            or rows[-1].timestamp + step != window.end_time
        ):
            raise ValueError(
                "The requested interval is not fully available or does not match the data resolution."
            )

        excluded = {
            "load",
            "residual_load",
            "hydro_pumped_storage_consumption",
            "cross_border_electricity_trading",
            "renewable_share_of_load",
            "renewable_share_of_generation",
        }
        series = [
            s
            for s in response.series
            if s.id not in excluded and (s.unit or response.unit) == response.unit
        ]
        if not series:
            raise ValueError("Energy-Charts returned no generation series.")
        if any(all(row.values[s.id] is None for s in series) for row in rows):
            raise ValueError(
                "Data for the requested interval has not been published yet."
            )

        return EnergyData(
            **response.model_dump(exclude={"series", "data"}),
            **window.model_dump(),
            retrieved_at=retrieved,
            cached=cached,
            series=series,
            data=[
                EnergyPoint(
                    timestamp=row.timestamp,
                    values={s.id: row.values[s.id] for s in series},
                )
                for row in rows
            ],
        ).model_copy(deep=True)

    async def close(self) -> None:
        if self._client is not None:
            await self._client.aclose()
            self._client = None
        self._cache.clear()
        self._locks.clear()
        self._next_request.clear()


energy_charts_provider = EnergyChartsProvider()
