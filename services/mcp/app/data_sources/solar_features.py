from pathlib import Path
from typing import cast

import pandas as pd
from pvlib.location import Location

from .open_meteo import POINTS


def prepare_pvlib_solar_features(
    start_date: str, end_date: str, output_dir: str, file_name: str
) -> pd.DataFrame:
    start = pd.Timestamp(start_date, tz="UTC")
    end = pd.Timestamp(end_date, tz="UTC") + pd.Timedelta(days=1)

    if start >= end:
        raise ValueError("start_date must not be after end_date")

    path = Path(__file__).resolve().parents[4] / output_dir / file_name
    hours = pd.date_range(start, end, freq="h", inclusive="left")
    frames = []

    for name, (latitude, longitude) in POINTS.items():
        location = Location(latitude, longitude, tz="UTC", name=name)
        position = location.get_solarposition(hours)
        clear_sky = location.get_clearsky(hours, model="ineichen")

        frames.append(
            pd.DataFrame(
                {
                    "timestamp_utc": hours,
                    "solar_elevation": position["elevation"],
                    "solar_apparent_zenith": position["apparent_zenith"],
                    "clear_sky_ghi": clear_sky["ghi"],
                    "clear_sky_dni": clear_sky["dni"],
                    "clear_sky_dhi": clear_sky["dhi"],
                }
            )
        )

    grouped = pd.concat(frames, ignore_index=True).groupby("timestamp_utc")
    mean = cast(pd.DataFrame, grouped.mean()).add_suffix("_mean")
    maximum = cast(
        pd.DataFrame, grouped[["solar_elevation", "clear_sky_ghi"]].max()
    ).add_suffix("_max")

    frame = mean.join(maximum).reset_index()
    frame["is_daylight_anywhere"] = frame.solar_elevation_max.gt(0).astype(int)

    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    frame.to_parquet(temporary, index=False)
    temporary.replace(path)

    return frame
