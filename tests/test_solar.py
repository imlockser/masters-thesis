import asyncio
import importlib
import json
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from threading import Event
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import numpy as np
import pandas as pd
import pytest
from fastmcp import Client
from lightgbm import LGBMRegressor
from mlflow.entities.model_registry import ModelVersion, ModelVersionTag
from pydantic import ValidationError

from services.mcp.app.providers.registry import ActiveModel, RegistryProvider
from services.mcp.app.settings import Settings
from services.mcp.app.solar import inference, jobs
from services.mcp.app.solar.cache import ForecastCache
from services.mcp.app.solar.contract import CONTRACT
from services.mcp.app.solar.models import (
    Contribution,
    ForecastResult,
    ForecastWindow,
    HourForecast,
    ReleaseMetadata,
)


@pytest.fixture
def release():
    start = datetime(2024, 6, 1, tzinfo=UTC)
    values = [100, -80, 60, -40, 20, -10, 5, -2] + [0] * 25
    rows = []

    for i in range(48):
        rows.append(
            HourForecast(
                interval_start=start + timedelta(hours=i),
                interval_end=start + timedelta(hours=i + 1),
                lead_hours=i + 1,
                raw_capacity_factor=0.01053,
                raw_generation_mw=1053,
                generation_mw=1053,
                energy_mwh=1053,
                base_mw=1000,
                output_adjustment_mw=0,
                contributions=[
                    Contribution(
                        feature=f.name,
                        label=f.label,
                        value=j,
                        unit=f.unit,
                        contribution_mw=values[j],
                    )
                    for j, f in enumerate(CONTRACT.features)
                ],
            )
        )

    metadata = ReleaseMetadata(
        release_id="20240531T230000000000Z",
        issued_at=start - timedelta(hours=1),
        completed_at=start,
        model_name="solar_generation",
        model_version="1",
        source_run_id="test-run",
        contract=CONTRACT,
        weather_retrieved_at=start,
        weather_parameters={},
        weather_units=[],
        capacity_snapshot_date=start.date(),
        capacity_value_timestamp=start,
        capacity_file_sha256="test",
        capacity_mw=100_000,
    )

    return ForecastResult(metadata=metadata, hours=rows)


def weather_for(hours):
    frames = []

    for name, (latitude, longitude) in inference.POINTS.items():
        frame = pd.DataFrame(
            {v: 20.0 if v.startswith("cloud") else 0.0 for v in inference.VARIABLES},
            index=range(len(hours)),
        )
        frame["timestamp_utc"] = hours
        frame["location"], frame["latitude"], frame["longitude"] = (
            name,
            latitude,
            longitude,
        )
        frames.append(frame)

    return pd.concat(frames, ignore_index=True)


def test_render_selects_three_per_sign_and_artifact_keeps_all(release):
    hour = release.hours[0]
    text = hour.render()
    assert "+100.00 MW" in text and "+60.00 MW" in text and "+20.00 MW" in text
    assert "-80.00 MW" in text and "-40.00 MW" in text and "-10.00 MW" in text
    assert "+5.00 MW" not in text and "-2.00 MW" not in text
    assert "Other contributions: +3.00 MW." in text
    rendered = release.render()
    assert rendered.count("Forecast:") == 48
    assert rendered.count("Germany — solar") == 1
    assert "Total forecast energy: 50,544.00 MWh." in rendered
    assert "test-run" not in rendered and "release_id" not in rendered
    assert "Warning:" not in rendered
    assert "warnings" not in release.model_dump(mode="json")["metadata"]
    assert "warnings" not in ReleaseMetadata.model_json_schema()["properties"]
    assert len(release.model_dump(mode="json")["hours"][0]["contributions"]) == 33


def test_total_energy_sums_distinct_hours_before_rendering_details(release):
    second_hour = release.hours[1].model_copy(
        update={
            "raw_capacity_factor": 0.011,
            "raw_generation_mw": 1100.0,
            "generation_mw": 1100.0,
            "energy_mwh": 1100.0,
            "base_mw": 1047.0,
        }
    )
    result = ForecastResult(
        metadata=release.metadata,
        hours=[release.hours[0], second_hour, release.hours[2]],
    )

    assert result.total_energy_mwh == 3206.0
    assert result.model_dump(mode="json")["total_energy_mwh"] == 3206.0

    rendered = result.render()
    assert rendered.index("Total forecast energy: 3,206.00 MWh.") < rendered.index(
        "Hourly forecast and explanation details:"
    )


@pytest.mark.parametrize(
    "start,end",
    [
        ("2024-06-01T00:00:00", "2024-06-01T01:00:00Z"),
        ("2024-06-01T00:30:00Z", "2024-06-01T01:00:00Z"),
        ("2024-06-01T01:00:00Z", "2024-06-01T00:00:00Z"),
        ("2024-06-01T00:00:00Z", "2024-06-03T01:00:00Z"),
    ],
)
def test_window_rejects_invalid_boundaries(start, end):
    with pytest.raises(ValidationError):
        ForecastWindow(start_time=start, end_time=end)


def test_window_normalizes_dst_offsets():
    window = ForecastWindow.model_validate(
        {
            "start_time": "2026-10-25T02:00:00+02:00",
            "end_time": "2026-10-25T02:00:00+01:00",
        }
    )
    assert window.end_time - window.start_time == timedelta(hours=1)


def test_cache_discards_stored_warnings(tmp_path, release):
    cache = ForecastCache(tmp_path)
    cache.publish(release, pd.DataFrame({"temperature": [10.0]}))
    metadata_path = tmp_path / release.metadata.release_id / "metadata.json"
    metadata = json.loads(metadata_path.read_text())
    assert "warnings" not in metadata
    metadata["warnings"] = ["Internal annotation"]
    metadata_path.write_text(json.dumps(metadata))

    result = cache.read(
        ForecastWindow(
            start_time=release.hours[0].interval_start,
            end_time=release.hours[0].interval_end,
        )
    )

    assert "warnings" not in result.model_dump(mode="json")["metadata"]
    assert "Internal annotation" not in result.render()


def test_cache_replacement_coverage_and_age(tmp_path, release, monkeypatch):
    cache = ForecastCache(tmp_path)
    weather = pd.DataFrame({"temperature": [10.0]})
    window = ForecastWindow(
        start_time=release.hours[0].interval_start,
        end_time=release.hours[2].interval_end,
    )
    cache.publish(release, weather)
    selected = cache.read(window)
    assert len(selected.hours) == 3
    assert selected.total_energy_mwh == 3159.0
    assert selected.total_energy_mwh != release.total_energy_mwh
    assert selected.hours[0].generation_mw == release.hours[0].generation_mw
    np.testing.assert_allclose(
        [c.contribution_mw for c in selected.hours[0].contributions],
        [c.contribution_mw for c in release.hours[0].contributions],
    )
    assert selected.metadata.issued_at.year == 2024  # No release-age rejection.

    with pytest.raises(ValueError, match="not fully covered"):
        cache.read(
            ForecastWindow(
                start_time=release.hours[-1].interval_end,
                end_time=release.hours[-1].interval_end + timedelta(hours=1),
            )
        )

    original = release.metadata.release_id
    newer = release.model_copy(deep=True)
    newer.metadata.release_id = "20240601T000000000000Z"

    with (
        patch.object(pd.DataFrame, "to_parquet", side_effect=OSError("disk full")),
        pytest.raises(OSError),
    ):
        cache.publish(newer, weather)

    assert cache.read(window).metadata.release_id == original
    cache.publish(newer, weather)
    assert cache.read(window).metadata.release_id == newer.metadata.release_id
    assert not (tmp_path / original).exists()
    assert not list(tmp_path.glob(".pending-*"))


def test_registry_keeps_old_model_until_validated_and_loads_once():
    provider = RegistryProvider()
    client = MagicMock()
    provider._client = client
    client.get_model_version_by_alias.return_value = SimpleNamespace(version="1")
    first = object()
    provider.refresh("solar", "champion", lambda _: first)
    client.get_model_version_by_alias.return_value = SimpleNamespace(version="2")
    started, finish = Event(), Event()
    second = object()
    calls = []

    def load(version):
        calls.append(version.version)
        started.set()
        assert finish.wait(timeout=5)

        return second

    with ThreadPoolExecutor(max_workers=2) as executor:
        a = executor.submit(provider.refresh, "solar", "champion", load)
        assert started.wait(timeout=5)
        assert provider.get("solar") is first
        b = executor.submit(provider.refresh, "solar", "champion", load)
        finish.set()
        assert a.result() is True and b.result() is False

    assert provider.get("solar") is second and first is not second
    assert calls == ["2"]
    client.get_model_version_by_alias.return_value = SimpleNamespace(version="3")

    def failed(_):
        raise ValueError("incompatible")

    with pytest.raises(ValueError):
        provider.refresh("solar", "champion", failed)

    assert provider.get("solar") is second


def test_active_model_is_validated_and_frozen():
    value = object()
    active = ActiveModel(version="1", value=value)
    assert active.value is value

    with pytest.raises(ValidationError):
        active.version = "2"

    with pytest.raises(ValidationError):
        ActiveModel.model_validate({"version": 1, "value": value})


def test_hourly_utc_schedule():
    now = datetime(2026, 10, 25, 1, 0, tzinfo=UTC)
    assert jobs.next_run(now, 0) == now + timedelta(hours=1)
    assert jobs.next_run(now, 5) == now + timedelta(minutes=5)
    assert jobs.next_run(now.replace(minute=6), 5) == now + timedelta(
        hours=1, minutes=5
    )


def test_hourly_does_not_overlap_and_waits_for_running_work(monkeypatch):
    started, finish = Event(), Event()
    calls = []

    def job():
        calls.append(1)
        started.set()
        assert finish.wait(timeout=5)

    monkeypatch.setattr(
        jobs, "next_run", lambda now, minute: now + timedelta(milliseconds=1)
    )

    async def run():
        task = asyncio.create_task(jobs.hourly(5, job))

        try:
            assert await asyncio.to_thread(started.wait, 5)
            await asyncio.sleep(0.02)
            assert len(calls) == 1
            task.cancel()
            await asyncio.sleep(0)
            assert not task.done()
        finally:
            finish.set()
            if not task.cancelling():
                task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task


def test_failed_pointer_swap_preserves_current_cache(tmp_path, release, monkeypatch):
    cache = ForecastCache(tmp_path)
    weather = pd.DataFrame({"temperature": [10.0]})
    cache.publish(release, weather)
    newer = release.model_copy(deep=True)
    newer.metadata.release_id = "20240601T010000000000Z"
    replace = Path.replace

    def fail_pointer(path, target):
        if path.name == "latest.tmp":
            raise OSError("Cannot publish pointer")

        return replace(path, target)

    monkeypatch.setattr(Path, "replace", fail_pointer)

    with pytest.raises(OSError):
        cache.publish(newer, weather)

    window = ForecastWindow(
        start_time=release.hours[0].interval_start,
        end_time=release.hours[0].interval_end,
    )
    assert cache.read(window).metadata.release_id == release.metadata.release_id


@pytest.mark.parametrize("fault", ["missing", "duplicate", "nan", "cloud", "negative"])
def test_weather_validation(fault):
    hours = pd.date_range("2024-06-01", periods=48, freq="h", tz="UTC").as_unit("ns")
    weather = weather_for(hours)
    if fault == "missing":
        weather = weather.iloc[1:]
    elif fault == "duplicate":
        weather = pd.concat([weather, weather.iloc[:1]])
    elif fault == "nan":
        weather.loc[0, "temperature_2m"] = np.nan
    elif fault == "cloud":
        weather.loc[0, "cloud_cover"] = 101
    else:
        weather.loc[0, "shortwave_radiation"] = -1

    with pytest.raises(ValueError):
        inference.validate_weather(weather, hours)


def test_fetch_weather_formats_hour_boundaries_and_preserves_grid(monkeypatch):
    hours = pd.date_range("2026-10-25T14:00:00Z", periods=48, freq="h").as_unit("ns")
    payload = []

    for _, frame in weather_for(hours).groupby("location", sort=False):
        hourly = {name: frame[name].tolist() for name in inference.VARIABLES}
        hourly["time"] = pd.Series(hours).dt.strftime("%Y-%m-%dT%H:%M").tolist()
        payload.append(
            {
                "utc_offset_seconds": 0,
                "hourly": hourly,
                "hourly_units": {"time": "iso8601", **inference.WEATHER_UNITS},
            }
        )

    response = MagicMock()
    response.json.return_value = payload
    get = MagicMock(return_value=response)
    monkeypatch.setattr(inference.requests, "get", get)

    weather, params, _, _ = inference.fetch_weather(hours)

    assert params["start_hour"] == "2026-10-25T14:00"
    assert params["end_hour"] == "2026-10-27T13:00"
    assert get.call_args.kwargs["params"] == params
    assert len(weather) == 480
    inference.validate_weather(weather, hours)


def test_features_preserve_dst_grid_and_weather_aggregation():
    # Validate deterministic preprocessing on a DST transition without network calls.
    hours = pd.date_range("2026-10-25", periods=48, freq="h", tz="UTC").as_unit("ns")
    weather = weather_for(hours)
    features = inference.prepare_features(weather, hours, 100_000)
    assert features.columns.tolist() == ["timestamp_utc", *CONTRACT.names]
    assert features.hour_local.iloc[:3].tolist() == [2, 2, 3]
    assert features.weather_cloud_cover_mean.eq(20).all()
    assert features.weather_cloud_cover_max.eq(20).all()
    assert np.isfinite(features[CONTRACT.names].to_numpy()).all()


def test_real_model_prediction_shap_and_contract_validation(tmp_path, monkeypatch):
    inputs = pd.concat([inference.control_inputs()] * 30, ignore_index=True)
    model = LGBMRegressor(
        n_estimators=4, num_leaves=3, min_child_samples=2, verbosity=-1, n_jobs=1
    )
    model.fit(inputs, np.tile([0.0, 0.3], 30))
    version = ModelVersion(
        name="solar_generation",
        version="1",
        run_id="training",
        creation_timestamp=0,
        tags=[ModelVersionTag("inference_contract", CONTRACT.model_dump_json())],
    )
    monkeypatch.setattr(inference.mlflow_sklearn, "load_model", lambda _: model)
    bundle = inference.load_solar_model(version)
    assert bundle.model is model and bundle.contract == CONTRACT
    broken = json.loads(CONTRACT.model_dump_json())
    broken["output_unit"] = "MW"
    version.tags["inference_contract"] = json.dumps(broken)

    with pytest.raises(ValueError, match="unsupported"):
        inference.load_solar_model(version)

    version.tags["inference_contract"] = "{}"

    with pytest.raises(ValueError):
        inference.load_solar_model(version)

    capacity_path = tmp_path / "capacity.parquet"
    pd.DataFrame(
        {
            "timestamp_utc": [pd.Timestamp("2020-01-01", tz="UTC")],
            "installed_pv_capacity_mw": [100_000.0],
        }
    ).to_parquet(capacity_path)
    config = Settings(
        mcp_name="test",
        mcp_host="127.0.0.1",
        mlflow_tracking_uri="http://127.0.0.1:5001",
        solar_model_name="solar_generation",
        solar_model_alias="champion",
        solar_capacity_file=capacity_path,
        solar_capacity_snapshot_date=date(2020, 1, 1),
        solar_cache_dir=tmp_path / "cache",
    )

    def fetch(hours):
        return weather_for(hours), {}, [], datetime.now(UTC)

    monkeypatch.setattr(inference, "fetch_weather", fetch)
    result, weather = inference.forecast(bundle, config)
    assert len(result.hours) == 48 and len(weather) == 480
    assert all(len(hour.contributions) == 33 for hour in result.hours)
    assert all(hour.generation_mw >= 0 for hour in result.hours)


@pytest.mark.parametrize(
    "now,start,earliest,accepted",
    [
        ("15:00:00", "2024-06-01T15:00:00Z", "2024-06-01T15:00:00Z", True),
        ("15:04:59.999999", "2024-06-01T15:00:00Z", "2024-06-01T15:00:00Z", True),
        ("15:04:00", "2024-06-01T14:00:00Z", "2024-06-01T15:00:00Z", False),
        ("15:05:00", "2024-06-01T15:00:00Z", "2024-06-01T16:00:00Z", False),
        ("15:05:00", "2024-06-01T16:00:00Z", "2024-06-01T16:00:00Z", True),
        ("15:05:00", "2024-06-01T17:00:00+02:00", "2024-06-01T16:00:00Z", False),
        ("23:05:00", "2024-06-01T23:00:00Z", "2024-06-02T00:00:00Z", False),
        ("23:05:00", "2024-06-02T00:00:00Z", "2024-06-02T00:00:00Z", True),
    ],
)
def test_tool_earliest_hour_advances_at_five_minutes(
    now, start, earliest, accepted, release, monkeypatch
):
    module = importlib.import_module(
        "services.mcp.app.tools.forecast_solar_generation.tool"
    )
    clock = MagicMock(wraps=datetime)
    clock.now.return_value = datetime.fromisoformat(f"2024-06-01T{now}+00:00")
    monkeypatch.setattr(module, "datetime", clock)
    cache = MagicMock()
    cache.read.return_value = release
    monkeypatch.setattr(module, "forecast_cache", cache)
    start_time = datetime.fromisoformat(start)

    result = asyncio.run(
        module.forecast_solar_generation(start_time, start_time + timedelta(hours=1))
    )

    assert bool(result.is_error) is not accepted
    clock.now.assert_called_once_with(UTC)
    if accepted:
        cache.read.assert_called_once_with(
            ForecastWindow(start_time=start_time, end_time=start_time + timedelta(hours=1))
        )
    else:
        cache.read.assert_not_called()
        assert result.content[0].type == "text"
        assert earliest in result.content[0].text


def test_mcp_text_and_structured_output_are_separate(tmp_path, release, monkeypatch):
    from services.mcp.app.mcp import mcp

    module = importlib.import_module(
        "services.mcp.app.tools.forecast_solar_generation.tool"
    )
    cache = ForecastCache(tmp_path)
    cache.publish(release, pd.DataFrame({"temperature": [10.0]}))
    monkeypatch.setattr(module, "forecast_cache", cache)
    monkeypatch.setattr(jobs, "refresh_model", lambda: None)
    clock = MagicMock(wraps=datetime)
    clock.now.return_value = release.hours[0].interval_start
    monkeypatch.setattr(module, "datetime", clock)

    async def run():
        async with Client(mcp) as client:
            result = await client.call_tool(
                "forecast_solar_generation",
                {
                    "start_time": release.hours[0].interval_start.isoformat(),
                    "end_time": release.hours[0].interval_end.isoformat(),
                },
            )
            assert result.content[0].type == "text"
            assert "Positive SHAP" in result.content[0].text
            assert "Total forecast energy: 1,053.00 MWh." in result.content[0].text
            assert "source_run_id" not in result.content[0].text
            assert "Warning:" not in result.content[0].text
            assert result.structured_content is not None
            assert "warnings" not in result.structured_content["metadata"]
            assert result.structured_content["total_energy_mwh"] == 1053.0
            assert result.structured_content["metadata"]["source_run_id"] == "test-run"
            invalid = await client.call_tool(
                "forecast_solar_generation",
                {
                    "start_time": "2024-06-01T00:30:00Z",
                    "end_time": "2024-06-01T01:00:00Z",
                },
                raise_on_error=False,
            )
            assert invalid.is_error

    asyncio.run(run())
