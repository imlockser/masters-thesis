from datetime import UTC, date, datetime, timedelta
from math import fsum, isclose
from typing import Self

from pydantic import (
    AwareDatetime,
    BaseModel,
    ConfigDict,
    Field,
    computed_field,
    field_validator,
    model_validator,
)

from .contract import FeatureContract


class NumericModel(BaseModel):
    model_config = ConfigDict(extra="forbid", allow_inf_nan=False)


class ForecastWindow(NumericModel):
    start_time: AwareDatetime
    end_time: AwareDatetime

    @field_validator("start_time", "end_time")
    @classmethod
    def whole_utc_hour(cls, value: datetime) -> datetime:
        value = value.astimezone(UTC)
        if value.minute or value.second or value.microsecond:
            raise ValueError("Time boundaries must be whole UTC hours.")

        return value

    @model_validator(mode="after")
    def valid_duration(self) -> Self:
        if not timedelta(0) < self.end_time - self.start_time <= timedelta(hours=48):
            raise ValueError("The interval must be positive and at most 48 hours.")

        return self


class Contribution(NumericModel):
    feature: str
    label: str
    value: float
    unit: str
    contribution_mw: float

    def render(self) -> str:
        return (
            f"{self.contribution_mw:+,.2f} MW — {self.label}: "
            f"{self.value:,.3f} {self.unit}"
        )


class HourForecast(NumericModel):
    interval_start: AwareDatetime
    interval_end: AwareDatetime
    lead_hours: float

    raw_capacity_factor: float
    raw_generation_mw: float
    generation_mw: float = Field(ge=0)
    energy_mwh: float = Field(ge=0)

    base_mw: float
    output_adjustment_mw: float
    contributions: list[Contribution]

    @model_validator(mode="after")
    def validate_balance(self) -> Self:
        if self.interval_end - self.interval_start != timedelta(hours=1):
            raise ValueError("Each forecast must cover exactly one hour.")

        reconstructed = self.base_mw + sum(
            c.contribution_mw for c in self.contributions
        )
        if not isclose(
            reconstructed, self.raw_generation_mw, rel_tol=1e-5, abs_tol=1e-5
        ):
            raise ValueError("SHAP contributions do not reconstruct the raw forecast.")
        if not isclose(
            self.raw_generation_mw + self.output_adjustment_mw,
            self.generation_mw,
            rel_tol=1e-5,
            abs_tol=1e-5,
        ):
            raise ValueError("Output adjustment does not reconstruct the forecast.")
        if not isclose(self.energy_mwh, self.generation_mw, rel_tol=1e-9, abs_tol=1e-6):
            raise ValueError("Hourly energy must equal average power times one hour.")

        return self

    def render(self) -> str:
        positive = sorted(
            (c for c in self.contributions if c.contribution_mw > 0),
            key=lambda c: -c.contribution_mw,
        )[:3]
        negative = sorted(
            (c for c in self.contributions if c.contribution_mw < 0),
            key=lambda c: c.contribution_mw,
        )[:3]
        other = sum(c.contribution_mw for c in self.contributions) - sum(
            c.contribution_mw for c in [*positive, *negative]
        )

        return "\n".join(
            [
                (
                    f"{self.interval_start.astimezone(UTC):%Y-%m-%d %H:%M}–"
                    f"{self.interval_end.astimezone(UTC):%Y-%m-%d %H:%M} UTC"
                ),
                f"Forecast: {self.generation_mw:,.2f} MW; energy: {self.energy_mwh:,.2f} MWh.",
                f"Model baseline: {self.base_mw:,.2f} MW.",
                "Positive SHAP contributions:",
                *(c.render() for c in positive),
                *(["None."] if not positive else []),
                "Negative SHAP contributions:",
                *(c.render() for c in negative),
                *(["None."] if not negative else []),
                f"Other contributions: {other:+,.2f} MW.",
                f"Output adjustment: {self.output_adjustment_mw:+,.2f} MW.",
            ]
        )


class ReleaseMetadata(NumericModel):
    release_id: str = Field(pattern=r"^\d{8}T\d{12}Z$")
    issued_at: AwareDatetime
    completed_at: AwareDatetime

    model_name: str
    model_version: str
    source_run_id: str
    contract: FeatureContract

    weather_retrieved_at: AwareDatetime
    weather_parameters: dict[str, str]
    weather_units: list[dict[str, str]]

    capacity_snapshot_date: date
    capacity_value_timestamp: AwareDatetime
    capacity_file_sha256: str
    capacity_mw: float = Field(gt=0)

    output_policy: str = "lower_bound_zero_v1"


class ForecastResult(NumericModel):
    metadata: ReleaseMetadata
    hours: list[HourForecast] = Field(min_length=1, max_length=48)

    @computed_field(
        description="Total forecast energy in MWh for the returned interval."
    )
    @property
    def total_energy_mwh(self) -> float:
        return fsum(hour.energy_mwh for hour in self.hours)

    @model_validator(mode="after")
    def continuous_hours(self) -> Self:
        for left, right in zip(self.hours, self.hours[1:]):
            if left.interval_end != right.interval_start:
                raise ValueError("Forecast hours must be ordered and contiguous.")

        names = self.metadata.contract.names

        for hour in self.hours:
            if [c.feature for c in hour.contributions] != names:
                raise ValueError(
                    "Contributions must match the complete feature contract."
                )
            if not isclose(
                hour.raw_capacity_factor * self.metadata.capacity_mw,
                hour.raw_generation_mw,
                rel_tol=1e-5,
                abs_tol=1e-5,
            ):
                raise ValueError("Raw MW must equal raw CF times installed capacity.")
            if not isclose(
                hour.generation_mw,
                max(hour.raw_generation_mw, 0),
                rel_tol=1e-5,
                abs_tol=1e-5,
            ):
                raise ValueError("The forecast must use the declared zero lower bound.")

        return self

    def render(self) -> str:
        header = "\n".join(
            [
                "Germany — solar generation forecast",
                f"Total forecast energy: {self.total_energy_mwh:,.2f} MWh.",
                (
                    f"Period: {self.hours[0].interval_start.astimezone(UTC):%Y-%m-%d %H:%M}–"
                    f"{self.hours[-1].interval_end.astimezone(UTC):%Y-%m-%d %H:%M} UTC."
                ),
                (
                    f"Issued: {self.metadata.issued_at.astimezone(UTC):%Y-%m-%d %H:%M} UTC. "
                    f"{len(self.hours)} hourly intervals."
                ),
                (
                    f"Installed PV capacity: {self.metadata.capacity_mw:,.2f} MW; "
                    f"MaStR snapshot: {self.metadata.capacity_snapshot_date.isoformat()}."
                ),
                "Hourly power is an average in MW; hourly energy covers one hour in MWh.",
                "Weather and solar means/maxima use ten sampled locations; means have equal weights.",
                (
                    "SHAP explains raw CF scaled by fixed installed capacity relative to the model baseline; "
                    "it does not establish causal effects or explain the capacity assumption."
                ),
            ]
        )
        return (
            header
            + "\n\nHourly forecast and explanation details:\n\n"
            + "\n\n".join(hour.render() for hour in self.hours)
        )
