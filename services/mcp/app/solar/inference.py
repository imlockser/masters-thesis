from datetime import UTC, datetime
from hashlib import sha256
from tempfile import TemporaryDirectory
from typing import Any, cast

import mlflow
import numpy as np
import pandas as pd
import requests
import shap
from lightgbm import LGBMRegressor
from mlflow import sklearn as mlflow_sklearn
from mlflow.entities.model_registry import ModelVersion
from mlflow.environment_variables import MLFLOW_ENABLE_PROXY_MULTIPART_DOWNLOAD
from pydantic import BaseModel, ConfigDict

from ..data_sources.calendar_features import prepare_german_calendar_features
from ..data_sources.open_meteo import POINTS, VARIABLES
from ..data_sources.solar_features import prepare_pvlib_solar_features
from ..settings import PROJECT_ROOT, Settings, settings
from .contract import CONTRACT, WEATHER_UNITS, FeatureContract
from .models import Contribution, ForecastResult, HourForecast, ReleaseMetadata


class SolarModel(BaseModel):
    model_config = ConfigDict(frozen=True, arbitrary_types_allowed=True)

    name: str
    version: str
    source_run_id: str

    model: LGBMRegressor
    explainer: Any
    contract: FeatureContract


def control_inputs() -> pd.DataFrame:
    inputs = pd.DataFrame(0.0, index=range(2), columns=CONTRACT.names)
    inputs["installed_pv_capacity_mw"] = 100_000.0

    inputs["hour_local"] = [0, 12]
    inputs["day_of_year"] = 180
    inputs["month"] = 6
    inputs["hour_cos"] = [1, -1]

    inputs["solar_elevation_mean"] = [-20, 60]
    inputs["solar_elevation_max"] = [-15, 65]
    inputs["solar_apparent_zenith_mean"] = [110, 30]
    inputs["clear_sky_ghi_mean"] = [0, 800]
    inputs["clear_sky_ghi_max"] = [0, 900]
    inputs["clear_sky_dni_mean"] = [0, 700]
    inputs["clear_sky_dhi_mean"] = [0, 100]
    inputs["is_daylight_anywhere"] = [0, 1]

    inputs["weather_shortwave_radiation_mean"] = [0, 600]
    inputs["weather_direct_radiation_mean"] = [0, 500]
    inputs["weather_diffuse_radiation_mean"] = [0, 100]

    return inputs


def load_solar_model(version: ModelVersion) -> SolarModel:
    contract = FeatureContract.model_validate_json(
        version.tags.get("inference_contract", "{}")
    )
    if (
        "inference_contract" not in version.tags
        or contract != CONTRACT
        or contract.model_fields_set != set(FeatureContract.model_fields)
    ):
        raise ValueError("The model preprocessing contract is missing or unsupported.")
    if not version.run_id:
        raise ValueError("The model must identify its source training run.")

    mlflow.set_tracking_uri(settings.mlflow_tracking_uri)
    MLFLOW_ENABLE_PROXY_MULTIPART_DOWNLOAD.set(False)

    model = mlflow_sklearn.load_model(f"models:/{version.name}/{version.version}")
    if not isinstance(model, LGBMRegressor) or model.feature_name_ != contract.names:
        raise ValueError(
            "The model must be LightGBM with the exact ordered feature contract."
        )

    explainer = shap.TreeExplainer(
        model, feature_perturbation="tree_path_dependent", model_output="raw"
    )
    inputs = control_inputs()
    prediction = np.asarray(model.predict(inputs))
    explained = explainer(inputs)

    if prediction.shape != (len(inputs),) or not np.isfinite(prediction).all():
        raise ValueError("The model failed its control prediction.")

    np.testing.assert_allclose(
        np.asarray(explained.base_values) + np.asarray(explained.values).sum(axis=1),
        prediction,
        rtol=1e-5,
        atol=1e-7,
    )

    return SolarModel(
        name=version.name,
        version=version.version,
        source_run_id=version.run_id,
        model=model,
        explainer=explainer,
        contract=contract,
    )


def fetch_weather(
    hours: pd.DatetimeIndex,
) -> tuple[pd.DataFrame, dict[str, str], list[dict[str, str]], datetime]:
    start_hour = cast(pd.Timestamp, hours[0])
    end_hour = cast(pd.Timestamp, hours[-1])

    params = {
        "latitude": ",".join(str(point[0]) for point in POINTS.values()),
        "longitude": ",".join(str(point[1]) for point in POINTS.values()),
        "hourly": ",".join(VARIABLES),
        "timezone": "GMT",
        "start_hour": start_hour.strftime("%Y-%m-%dT%H:%M"),
        "end_hour": end_hour.strftime("%Y-%m-%dT%H:%M"),
        "wind_speed_unit": "kmh",
        "temperature_unit": "celsius",
        "precipitation_unit": "mm",
    }

    response = requests.get(
        "https://api.open-meteo.com/v1/forecast", params=params, timeout=60
    )
    response.raise_for_status()
    payload = response.json()
    retrieved_at = datetime.now(UTC)

    if not isinstance(payload, list) or len(payload) != len(POINTS):
        raise ValueError("Weather must contain all ten locations.")

    frames, units = [], []

    for (name, (latitude, longitude)), item in zip(
        POINTS.items(), payload, strict=True
    ):
        if item.get("error") or item.get("utc_offset_seconds") != 0:
            raise ValueError(f"Invalid weather response for {name}.")

        actual_units = item["hourly_units"]
        if any(
            actual_units.get(variable) != unit
            for variable, unit in WEATHER_UNITS.items()
        ):
            raise ValueError(f"Weather units changed for {name}.")

        frame = pd.DataFrame(item["hourly"])
        frame["timestamp_utc"] = pd.to_datetime(frame.pop("time"), utc=True).dt.as_unit(
            "ns"
        )
        frame["location"], frame["latitude"], frame["longitude"] = (
            name,
            latitude,
            longitude,
        )
        frames.append(frame)
        units.append(actual_units)

    weather = pd.concat(frames, ignore_index=True)
    validate_weather(weather, hours)

    return weather, params, units, retrieved_at


def validate_weather(weather: pd.DataFrame, hours: pd.DatetimeIndex) -> None:
    if (
        set(weather.location) != set(POINTS)
        or weather.duplicated(["timestamp_utc", "location"]).any()
    ):
        raise ValueError("Weather has missing locations or duplicate hours.")

    for name, frame in weather.groupby("location"):
        if not pd.DatetimeIndex(frame.timestamp_utc).equals(hours):
            raise ValueError(
                f"Weather does not cover the exact forecast grid for {name}."
            )

    if not np.isfinite(weather[VARIABLES].to_numpy()).all():
        raise ValueError("Weather contains missing or non-finite values.")

    nonnegative = [v for v in VARIABLES if v != "temperature_2m"]
    clouds = [v for v in VARIABLES if v.startswith("cloud_cover")]
    if np.any(weather[nonnegative].to_numpy() < 0) or np.any(
        weather[clouds].to_numpy() > 100
    ):
        raise ValueError("Weather contains invalid physical values.")


def prepare_features(
    weather: pd.DataFrame, hours: pd.DatetimeIndex, capacity_mw: float
) -> pd.DataFrame:
    validate_weather(weather, hours)

    with TemporaryDirectory() as folder:
        start, end = (
            cast(pd.Timestamp, hours[0]).date().isoformat(),
            cast(pd.Timestamp, hours[-1]).date().isoformat(),
        )
        calendar = prepare_german_calendar_features(
            start, end, folder, "calendar.parquet"
        )
        solar = prepare_pvlib_solar_features(start, end, folder, "solar.parquet")

    grouped = weather.groupby("timestamp_utc")
    mean = (
        cast(pd.DataFrame, grouped[sorted(VARIABLES)].mean())
        .add_prefix("weather_")
        .add_suffix("_mean")
    )
    clouds = sorted(v for v in VARIABLES if v.startswith("cloud_cover"))
    maximum = (
        cast(pd.DataFrame, grouped[clouds].max())
        .add_prefix("weather_")
        .add_suffix("_max")
    )

    features = pd.DataFrame(
        {"timestamp_utc": hours, "installed_pv_capacity_mw": capacity_mw}
    )
    numeric_calendar = calendar[
        ["timestamp_utc", *calendar.select_dtypes(include="number").columns]
    ]

    for frame in [numeric_calendar, solar, mean.join(maximum).reset_index()]:
        features = features.merge(
            frame, on="timestamp_utc", how="left", validate="one_to_one"
        )

    if set(features.columns) != {"timestamp_utc", *CONTRACT.names}:
        raise ValueError("Prepared features differ from the supported contract.")

    features = cast(pd.DataFrame, features[["timestamp_utc", *CONTRACT.names]])
    if not np.isfinite(features[CONTRACT.names].to_numpy()).all():
        raise ValueError("Prepared features contain missing or non-finite values.")

    return features


def forecast(
    bundle: SolarModel, config: Settings = settings
) -> tuple[ForecastResult, pd.DataFrame]:
    issued_at = pd.Timestamp.now(tz="UTC")
    hours = pd.date_range(
        issued_at.floor("h") + pd.Timedelta(hours=1), periods=48, freq="h"
    ).as_unit("ns")

    capacity_path = PROJECT_ROOT / config.solar_capacity_file
    capacity = pd.read_parquet(capacity_path)
    capacity["timestamp_utc"] = pd.to_datetime(capacity.timestamp_utc, utc=True)
    if capacity.timestamp_utc.isna().any() or capacity.timestamp_utc.duplicated().any():
        raise ValueError("MaStR capacity timestamps are invalid.")

    known = capacity.loc[capacity.timestamp_utc.le(issued_at)].sort_values(
        "timestamp_utc"
    )
    if known.empty:
        raise ValueError("No installed capacity is known at release time.")

    latest = known.iloc[-1]
    capacity_mw = float(latest.installed_pv_capacity_mw)
    if not np.isfinite(capacity_mw) or capacity_mw <= 0:
        raise ValueError("Installed capacity must be finite and positive.")
    if (
        config.solar_capacity_snapshot_date
        > issued_at.tz_convert("Europe/Berlin").date()
    ):
        raise ValueError("The MaStR snapshot must not be from the future.")

    weather, params, units, retrieved_at = fetch_weather(hours)
    features = prepare_features(weather, hours, capacity_mw)
    inputs = cast(pd.DataFrame, features[bundle.contract.names])

    raw_cf = np.asarray(bundle.model.predict(inputs))
    explained = bundle.explainer(inputs)
    values, base = np.asarray(explained.values), np.asarray(explained.base_values)
    if raw_cf.shape != (48,) or values.shape != (48, len(CONTRACT.names)):
        raise ValueError("Model output dimensions do not match the forecast horizon.")
    if not all(np.isfinite(a).all() for a in [raw_cf, values, base]):
        raise ValueError("Model output contains non-finite values.")

    np.testing.assert_allclose(base + values.sum(axis=1), raw_cf, rtol=1e-5, atol=1e-7)
    rows = []

    for i, timestamp in enumerate(hours):
        raw_mw, output_mw = (
            float(raw_cf[i] * capacity_mw),
            float(max(raw_cf[i], 0) * capacity_mw),
        )
        rows.append(
            HourForecast(
                interval_start=timestamp.to_pydatetime(),
                interval_end=(timestamp + pd.Timedelta(hours=1)).to_pydatetime(),
                lead_hours=(timestamp - issued_at).total_seconds() / 3600,
                raw_capacity_factor=float(raw_cf[i]),
                raw_generation_mw=raw_mw,
                generation_mw=output_mw,
                energy_mwh=output_mw,
                base_mw=float(base[i] * capacity_mw),
                output_adjustment_mw=output_mw - raw_mw,
                contributions=[
                    Contribution(
                        feature=f.name,
                        label=f.label,
                        value=float(inputs.iloc[i][f.name]),
                        unit=f.unit,
                        contribution_mw=float(values[i, j] * capacity_mw),
                    )
                    for j, f in enumerate(bundle.contract.features)
                ],
            )
        )

    metadata = ReleaseMetadata(
        release_id=issued_at.strftime("%Y%m%dT%H%M%S%fZ"),
        issued_at=issued_at.to_pydatetime(),
        completed_at=datetime.now(UTC),
        model_name=bundle.name,
        model_version=bundle.version,
        source_run_id=bundle.source_run_id,
        contract=bundle.contract,
        weather_retrieved_at=retrieved_at,
        weather_parameters=params,
        weather_units=units,
        capacity_snapshot_date=config.solar_capacity_snapshot_date,
        capacity_value_timestamp=latest.timestamp_utc.to_pydatetime(),
        capacity_file_sha256=sha256(capacity_path.read_bytes()).hexdigest(),
        capacity_mw=capacity_mw,
    )

    return ForecastResult(metadata=metadata, hours=rows), weather
