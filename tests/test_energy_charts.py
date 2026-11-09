import asyncio
import json
from copy import deepcopy
from datetime import UTC, date, datetime, timedelta
from email.utils import format_datetime
from unittest.mock import AsyncMock

import httpx
import pytest
from fastmcp import Client
from pydantic import ValidationError

from services.mcp.app.providers import energy_charts as module
from services.mcp.app.providers.energy_charts import BERLIN, EnergyChartsProvider


def payload(endpoint="price", day=date(2024, 6, 1)):
    start = datetime.combine(day, datetime.min.time(), BERLIN).astimezone(UTC)
    end = datetime.combine(
        day + timedelta(days=1), datetime.min.time(), BERLIN
    ).astimezone(UTC)
    series = [{"id": "day_ahead_price", "name": "Day-ahead price"}]
    if endpoint == "public_power":
        series = [
            {"id": "solar", "name": "Solar"},
            {"id": "wind_onshore", "name": "Wind onshore"},
            {"id": "load", "name": "Load"},
            {"id": "renewable_share_of_load", "name": "Renewable share", "unit": "%"},
        ]
    rows = []
    while start < end:
        values = {"day_ahead_price": -10.0}
        if endpoint == "public_power":
            values = {
                "solar": 100.0,
                "wind_onshore": 200.0,
                "load": 400.0,
                "renewable_share_of_load": 75.0,
            }
        rows.append({"timestamp": start.isoformat(), "values": values})
        start += timedelta(minutes=15)
    return {
        "schema_version": "2.0",
        "endpoint": endpoint,
        "country": "de",
        "bidding_zone": "DE-LU" if endpoint == "price" else None,
        "timezone": "Europe/Berlin",
        "unit": "EUR / MWh" if endpoint == "price" else "MW",
        "interval_minutes": 15,
        "generated_at": "2024-06-01T12:00:00Z",
        "license": "CC BY 4.0; source Energy-Charts.info",
        "series": series,
        "data": rows,
    }


async def provider_with(respond):
    provider = EnergyChartsProvider()
    provider.setup()
    await provider.client.aclose()
    provider._client = httpx.AsyncClient(
        base_url="https://api.energy-charts.info",
        transport=httpx.MockTransport(respond),
    )
    return provider


def bounds(day=date(2024, 6, 1)):
    return (
        datetime.combine(day, datetime.min.time(), BERLIN),
        datetime.combine(day + timedelta(days=1), datetime.min.time(), BERLIN),
    )


def test_lifecycle():
    async def run():
        provider = EnergyChartsProvider()
        with pytest.raises(RuntimeError, match="setup"):
            _ = provider.client
        provider.setup()
        with pytest.raises(RuntimeError, match="already initialized"):
            provider.setup()
        await provider.close()
        await provider.close()
        provider.setup()
        await provider.close()

    asyncio.run(run())


@pytest.mark.parametrize("endpoint", ["price", "public_power"])
def test_concurrent_and_contained_intervals_share_one_request(endpoint):
    requests = []

    def respond(request):
        requests.append(request)
        assert request.url.path == f"/v2/{endpoint}"
        assert request.url.params["start"] == request.url.params["end"] == "2024-06-01"
        return httpx.Response(200, json=payload(endpoint))

    async def run():
        provider = await provider_with(respond)
        method = provider.prices if endpoint == "price" else provider.generation_mix
        start, end = bounds()
        try:
            results = await asyncio.gather(*(method(start, end) for _ in range(10)))
            assert sum(not result.cached for result in results) == 1
            part = await method(start + timedelta(hours=2), start + timedelta(hours=3))
            assert part.cached and len(part.data) == 4
            assert part.retrieved_at == results[0].retrieved_at
            assert len(requests) == 1
            if endpoint == "public_power":
                assert {s.id for s in part.series} == {"solar", "wind_onshore"}
                assert set(part.data[0].values) == {"solar", "wind_onshore"}
            else:
                assert part.data[0].values["day_ahead_price"] == -10
            part.data[0].values.clear()
            again = await method(start, end)
            assert again.data[8].values
        finally:
            await provider.close()

    asyncio.run(run())


@pytest.mark.parametrize(
    "endpoint,day,ttl",
    [
        ("price", datetime.now(BERLIN).date(), 3600),
        ("public_power", datetime.now(BERLIN).date(), 600),
        ("price", date(2024, 6, 1), 86400),
    ],
)
def test_expired_entries_refresh(endpoint, day, ttl, monkeypatch):
    clock = [0.0]
    monkeypatch.setattr(module, "monotonic", lambda: clock[0])
    requests = []

    def respond(request):
        requests.append(request)
        return httpx.Response(200, json=payload(endpoint, day))

    async def run():
        provider = await provider_with(respond)
        method = provider.prices if endpoint == "price" else provider.generation_mix
        try:
            assert not (await method(*bounds(day))).cached
            clock[0] = ttl - 1
            assert (await method(*bounds(day))).cached
            clock[0] = ttl + 1
            assert not (await method(*bounds(day))).cached
            assert len(requests) == 2
        finally:
            await provider.close()

    asyncio.run(run())


def test_different_cache_misses_are_spaced(monkeypatch):
    sleep = AsyncMock()
    monkeypatch.setattr(module, "monotonic", lambda: 0)
    monkeypatch.setattr(module.asyncio, "sleep", sleep)

    def respond(request):
        return httpx.Response(
            200, json=payload(day=date.fromisoformat(request.url.params["start"]))
        )

    async def run():
        provider = await provider_with(respond)
        try:
            await provider.prices(*bounds())
            await provider.prices(*bounds(date(2024, 6, 2)))
            sleep.assert_awaited_once_with(31)
        finally:
            await provider.close()

    asyncio.run(run())


@pytest.mark.parametrize("retry", ["120", "date", "invalid"])
def test_retry_after_blocks_new_requests_but_keeps_valid_cache(retry, monkeypatch):
    clock = [0.0]
    monkeypatch.setattr(module, "monotonic", lambda: clock[0])
    requests = []
    if retry == "date":
        retry = format_datetime(datetime.now(UTC) + timedelta(seconds=120), usegmt=True)

    def respond(request):
        requests.append(request)
        if len(requests) == 1:
            return httpx.Response(200, json=payload())
        return httpx.Response(429, headers={"Retry-After": retry})

    async def run():
        provider = await provider_with(respond)
        try:
            await provider.prices(*bounds())
            clock[0] = 31
            for _ in range(2):
                with pytest.raises(ValueError, match="rate limited"):
                    await provider.prices(*bounds(date(2024, 6, 2)))
            assert len(requests) == 2
            assert (await provider.prices(*bounds())).cached
        finally:
            await provider.close()

    asyncio.run(run())


@pytest.mark.parametrize(
    "fault",
    ["unit", "zone", "country", "nan", "gap", "series", "json", "503", "network"],
)
def test_faults_do_not_poison_cache(fault, monkeypatch):
    clock = [0.0]
    monkeypatch.setattr(module, "monotonic", lambda: clock[0])
    requests = []

    def respond(request):
        requests.append(request)
        body = payload()
        if len(requests) > 1:
            return httpx.Response(200, json=body)
        if fault == "unit":
            body["unit"] = "kW"
        elif fault == "zone":
            body["bidding_zone"] = "AT"
        elif fault == "country":
            body["country"] = "at"
        elif fault == "nan":
            body["data"][0]["values"]["day_ahead_price"] = float("nan")
            return httpx.Response(200, text=json.dumps(body))
        elif fault == "gap":
            body["data"].pop(1)
        elif fault == "series":
            body["series"].append(deepcopy(body["series"][0]))
        elif fault == "json":
            return httpx.Response(200, text="not JSON")
        elif fault == "503":
            return httpx.Response(503)
        elif fault == "network":
            raise httpx.ConnectError("unavailable", request=request)
        return httpx.Response(200, json=body)

    async def run():
        provider = await provider_with(respond)
        try:
            with pytest.raises(ValueError):
                await provider.prices(*bounds())
            clock[0] = 32
            assert not (await provider.prices(*bounds())).cached
            assert len(requests) == 2
        finally:
            await provider.close()

    asyncio.run(run())


def test_unpublished_prices_use_short_cache(monkeypatch):
    clock = [0.0]
    monkeypatch.setattr(module, "monotonic", lambda: clock[0])
    requests = []

    def respond(request):
        requests.append(request)
        body = payload()
        if len(requests) == 1:
            for row in body["data"]:
                row["values"]["day_ahead_price"] = None
        return httpx.Response(200, json=body)

    async def run():
        provider = await provider_with(respond)
        try:
            for _ in range(2):
                with pytest.raises(ValueError, match="not been published"):
                    await provider.prices(*bounds())
            assert len(requests) == 1
            clock[0] = 61
            assert not (await provider.prices(*bounds())).cached
        finally:
            await provider.close()

    asyncio.run(run())


@pytest.mark.parametrize(
    "day,count", [(date(2024, 3, 31), 92), (date(2024, 10, 27), 100)]
)
def test_dst_days_keep_all_market_intervals(day, count):
    async def run():
        provider = await provider_with(
            lambda _: httpx.Response(200, json=payload(day=day))
        )
        try:
            result = await provider.prices(*bounds(day))
            assert len(result.data) == count
            text = result.render()
            assert f"Time with negative prices: {count / 4:.2f} hours" in text
            if count == 100:
                assert "02:00 +0200" in text and "02:00 +0100" in text
        finally:
            await provider.close()

    asyncio.run(run())


@pytest.mark.parametrize(
    "start,end",
    [
        ("2024-06-01T00:00:00", "2024-06-02T00:00:00Z"),
        ("2024-06-01T00:01:00Z", "2024-06-02T00:00:00Z"),
        ("2024-06-02T00:00:00Z", "2024-06-01T00:00:00Z"),
        ("2024-06-01T00:00:00Z", "2024-06-09T00:00:00Z"),
    ],
)
def test_bad_intervals_never_call_api(start, end):
    async def run():
        def respond(_):
            pytest.fail("invalid intervals must not call the API")

        provider = await provider_with(respond)
        try:
            with pytest.raises(ValidationError):
                await provider.prices(
                    datetime.fromisoformat(start), datetime.fromisoformat(end)
                )
        finally:
            await provider.close()

    asyncio.run(run())


def test_both_api_tools_through_mcp_return_cached_data(monkeypatch):
    import importlib

    from services.mcp.app.mcp import mcp
    from services.mcp.app.solar import jobs

    requests = []

    def respond(request):
        requests.append(request)
        endpoint = request.url.path.rsplit("/", 1)[-1]
        body = payload(endpoint)
        if endpoint == "public_power":
            body["data"][0]["values"]["solar"] = None
        return httpx.Response(200, json=body)

    async def run():
        provider = await provider_with(respond)
        monkeypatch.setattr(jobs, "refresh_model", lambda: None)
        for name in ("get_electricity_prices", "get_generation_mix"):
            tool = importlib.import_module(f"services.mcp.app.tools.{name}.tool")
            monkeypatch.setattr(tool, "energy_charts_provider", provider)
        start, end = bounds()
        args = {"start_time": start.isoformat(), "end_time": end.isoformat()}
        try:
            async with Client(mcp) as client:
                for name in ("get_electricity_prices", "get_generation_mix"):
                    first = await client.call_tool(name, args)
                    second = await client.call_tool(name, args)
                    assert not first.is_error and not second.is_error
                    assert first.structured_content is not None
                    assert second.structured_content is not None
                    assert first.content[0].type == "text"
                    assert first.structured_content["cached"] is False
                    assert second.structured_content["cached"] is True
                    assert (
                        first.structured_content["data"]
                        == second.structured_content["data"]
                    )
                    assert first.content == second.content
                    text = first.content[0].text
                    for technical in (
                        "License",
                        "CC BY",
                        "http",
                        "retrieved",
                        "generated_at",
                        "cached",
                        "schema_version",
                    ):
                        assert technical not in text
                    assert "license" not in first.structured_content
                    assert "source" not in first.structured_content
                    assert (
                        "license"
                        not in module.EnergyData.model_json_schema(
                            mode="serialization"
                        )["properties"]
                    )
                    if name == "get_generation_mix":
                        assert (
                            "Solar: incomplete (95/96 intervals reported)"
                            in first.content[0].text
                        )
                        assert "4,800.00 MWh" in first.content[0].text
                        assert "2024-06-01 — generation by source" in text
                        assert "highest interval-average power 200.00 MW" in text
                    else:
                        assert "-10.00" in first.content[0].text
                        assert "2024-06-01 — electricity prices" in text
                        assert (
                            "Average price over the requested part of this day: -10.00 EUR/MWh"
                            in text
                        )
                        assert "Time with negative prices: 24.00 hours" in text
                assert len(requests) == 2
        finally:
            await provider.close()

    asyncio.run(run())


@pytest.mark.parametrize("endpoint", ["price", "public_power"])
def test_context_groups_days_and_keeps_summary_units(endpoint):
    body = payload(endpoint)
    tomorrow = payload(endpoint, date(2024, 6, 2))
    if endpoint == "price":
        for row in body["data"][1:]:
            row["values"]["day_ahead_price"] = 20.0
        for row in tomorrow["data"]:
            row["values"]["day_ahead_price"] = 40.0
    else:
        for row in tomorrow["data"]:
            row["values"]["solar"] = 300.0
    body["data"].extend(tomorrow["data"])

    async def run():
        provider = await provider_with(lambda _: httpx.Response(200, json=body))
        method = provider.prices if endpoint == "price" else provider.generation_mix
        try:
            result = await method(bounds()[0], bounds(date(2024, 6, 2))[1])
            text = result.render()
            assert text.index("2024-06-01 —") < text.index("2024-06-02 —")
            if endpoint == "price":
                assert "19.69 EUR/MWh" in text
                assert "40.00 EUR/MWh" in text
                assert "Time with negative prices: 0.25 hours" in text
                assert (
                    "Lowest interval price: -10.00 EUR/MWh, starting 00:00 +0200"
                    in text
                )
                assert (
                    "Highest interval price: 20.00 EUR/MWh, starting 00:15 +0200"
                    in text
                )
            else:
                assert "Requested-period summary by source" in text
                assert "Solar: 9,600.00 MWh generated; mean power 200.00 MW" in text
                assert "Solar: 2,400.00 MWh generated; mean power 100.00 MW" in text
                assert "Solar: 7,200.00 MWh generated; mean power 300.00 MW" in text
        finally:
            await provider.close()

    asyncio.run(run())
