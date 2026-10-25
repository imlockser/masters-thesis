from typing import Literal, cast

from pydantic import BaseModel, ConfigDict, Field

from ..data_sources.open_meteo import POINTS, VARIABLES


class Feature(BaseModel):
    model_config = ConfigDict(frozen=True, extra="forbid")
    name: str
    label: str
    unit: str
    dtype: Literal["integer", "number"] = "number"


FEATURES = [
    Feature(
        name=name,
        label=label,
        unit=unit,
        dtype=cast(Literal["integer", "number"], dtype),
    )
    for name, label, unit, dtype in [
        ("installed_pv_capacity_mw", "installed PV capacity", "MW", "number"),
        ("hour_local", "Berlin local hour", "hour", "integer"),
        ("day_of_year", "local day of year", "day", "integer"),
        ("month", "local month", "month", "integer"),
        ("weekday", "local weekday (Monday=0)", "index", "integer"),
        ("is_weekend", "weekend flag", "0/1", "integer"),
        ("is_holiday_de", "German nationwide holiday flag", "0/1", "integer"),
        ("hour_sin", "local hour sine", "dimensionless", "number"),
        ("hour_cos", "local hour cosine", "dimensionless", "number"),
        ("day_of_year_sin", "local day-of-year sine", "dimensionless", "number"),
        ("day_of_year_cos", "local day-of-year cosine", "dimensionless", "number"),
        ("solar_elevation_mean", "mean solar elevation", "degrees", "number"),
        (
            "solar_apparent_zenith_mean",
            "mean apparent solar zenith",
            "degrees",
            "number",
        ),
        ("clear_sky_ghi_mean", "mean clear-sky GHI", "W/m²", "number"),
        ("clear_sky_dni_mean", "mean clear-sky DNI", "W/m²", "number"),
        ("clear_sky_dhi_mean", "mean clear-sky DHI", "W/m²", "number"),
        ("solar_elevation_max", "maximum solar elevation", "degrees", "number"),
        ("clear_sky_ghi_max", "maximum clear-sky GHI", "W/m²", "number"),
        ("is_daylight_anywhere", "daylight at any sampled location", "0/1", "integer"),
    ]
]
WEATHER_UNITS = {
    "shortwave_radiation": "W/m²",
    "direct_radiation": "W/m²",
    "diffuse_radiation": "W/m²",
    "cloud_cover": "%",
    "cloud_cover_low": "%",
    "cloud_cover_mid": "%",
    "cloud_cover_high": "%",
    "temperature_2m": "°C",
    "precipitation": "mm",
    "wind_speed_10m": "km/h",
}
for aggregation, variables in [
    ("mean", sorted(VARIABLES)),
    ("max", sorted(name for name in VARIABLES if name.startswith("cloud_cover"))),
]:
    for variable in variables:
        FEATURES.append(
            Feature(
                name=f"weather_{variable}_{aggregation}",
                label=f"{'mean' if aggregation == 'mean' else 'maximum'} {variable.replace('_', ' ')}",
                unit=WEATHER_UNITS[variable],
            )
        )


class FeatureContract(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    preprocessing_version: str = "solar_numeric_v1"
    target: str = "target_solar_capacity_factor"
    output_unit: str = "capacity_factor"
    calendar_timezone: str = "Europe/Berlin"
    day_of_year_period: float = 365.25
    solar_method: str = "pvlib_location_defaults_ineichen"
    timestamp_policy: str = "unshifted_training_labels_alignment_unverified"
    capacity_policy: str = "hold_last_known_operating_fleet_snapshot"
    weather_aggregation: str = "equal_weight_mean_and_cloud_max"
    points: dict[str, tuple[float, float]] = Field(default_factory=lambda: dict(POINTS))
    weather_variables: list[str] = Field(default_factory=lambda: list(VARIABLES))
    features: list[Feature] = Field(default_factory=lambda: list(FEATURES))

    @property
    def names(self) -> list[str]:
        return [feature.name for feature in self.features]


CONTRACT = FeatureContract()
