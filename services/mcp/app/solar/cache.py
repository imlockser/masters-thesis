import json
import re
import shutil
from pathlib import Path
from tempfile import TemporaryDirectory
from threading import RLock

import pandas as pd

from ..settings import PROJECT_ROOT, settings
from .models import (
    Contribution,
    ForecastResult,
    ForecastWindow,
    HourForecast,
    ReleaseMetadata,
)


class ForecastCache:
    def __init__(self, directory: Path) -> None:
        self.directory = directory
        self._lock = RLock()

    def _current(self) -> Path:
        pointer = self.directory / "latest.json"
        if not pointer.exists():
            raise ValueError("No solar forecast has been published yet.")

        release_id = json.loads(pointer.read_text())["release_id"]
        if not isinstance(release_id, str) or not re.fullmatch(
            r"\d{8}T\d{12}Z", release_id
        ):
            raise ValueError("The solar forecast pointer is invalid.")

        return self.directory / release_id

    def publish(self, result: ForecastResult, weather: pd.DataFrame) -> None:
        if len(result.hours) != 48:
            raise ValueError("A published release must contain all 48 hours.")

        self.directory.mkdir(parents=True, exist_ok=True)

        with TemporaryDirectory(prefix=".pending-", dir=self.directory) as temporary:
            staging = Path(temporary)
            rows = []

            for hour in result.hours:
                row = hour.model_dump(exclude={"contributions"})

                for contribution in hour.contributions:
                    row[contribution.feature] = contribution.value
                    row[f"shap_{contribution.feature}"] = (
                        contribution.contribution_mw / result.metadata.capacity_mw
                    )

                rows.append(row)

            frame = pd.DataFrame(rows)
            frame.to_parquet(staging / "forecast.parquet", index=False)
            pd.testing.assert_frame_equal(
                pd.read_parquet(staging / "forecast.parquet"), frame
            )
            weather.to_parquet(staging / "weather.parquet", index=False)
            (staging / "metadata.json").write_text(
                result.metadata.model_dump_json(indent=2) + "\n"
            )

            with self._lock:
                old = (
                    self._current()
                    if (self.directory / "latest.json").exists()
                    else None
                )
                destination = self.directory / result.metadata.release_id
                if destination.exists():
                    raise ValueError("A forecast release with this ID already exists.")

                staging.rename(destination)
                pointer = self.directory / "latest.tmp"
                pointer.write_text(
                    json.dumps({"release_id": result.metadata.release_id}) + "\n"
                )
                pointer.replace(self.directory / "latest.json")

                if old is not None:
                    shutil.rmtree(old, ignore_errors=True)

    def read(self, window: ForecastWindow) -> ForecastResult:
        with self._lock:
            directory = self._current()
            metadata_values = json.loads((directory / "metadata.json").read_text())
            metadata_values.pop("warnings", None)
            metadata = ReleaseMetadata.model_validate(metadata_values)
            frame = pd.read_parquet(directory / "forecast.parquet")

            selected = frame.loc[
                frame.interval_start.ge(window.start_time)
                & frame.interval_start.lt(window.end_time)
            ]
            expected = pd.date_range(
                window.start_time, window.end_time, freq="h", inclusive="left"
            )
            if not pd.DatetimeIndex(selected.interval_start).equals(expected):
                raise ValueError(
                    f"Requested interval is not fully covered. Available UTC coverage: "
                    f"{frame.interval_start.min().isoformat()} to {frame.interval_end.max().isoformat()} (end exclusive)."
                )

            rows = []

            for _, row in selected.iterrows():
                values = {
                    key: row[key]
                    for key in HourForecast.model_fields
                    if key != "contributions"
                }
                contributions = [
                    Contribution(
                        feature=f.name,
                        label=f.label,
                        value=float(row[f.name]),
                        unit=f.unit,
                        contribution_mw=float(
                            row[f"shap_{f.name}"] * metadata.capacity_mw
                        ),
                    )
                    for f in metadata.contract.features
                ]
                rows.append(HourForecast(**values, contributions=contributions))

            return ForecastResult(metadata=metadata, hours=rows)


forecast_cache = ForecastCache(PROJECT_ROOT / settings.solar_cache_dir)
