from pathlib import Path

import holidays
import numpy as np
import pandas as pd


def prepare_german_calendar_features(
    start_date: str, end_date: str, output_dir: str, file_name: str
) -> pd.DataFrame:
    start = pd.Timestamp(start_date, tz="UTC")
    end = pd.Timestamp(end_date, tz="UTC") + pd.Timedelta(days=1)

    if start >= end:
        raise ValueError("start_date must not be after end_date")

    path = Path(__file__).resolve().parents[4] / output_dir / file_name
    frame = pd.DataFrame(
        {"timestamp_utc": pd.date_range(start, end, freq="h", inclusive="left")}
    )
    frame["timestamp_local"] = frame.timestamp_utc.dt.tz_convert("Europe/Berlin")
    frame["date_local"] = frame.timestamp_local.dt.date

    frame["hour_local"] = frame.timestamp_local.dt.hour
    frame["day_of_year"] = frame.timestamp_local.dt.dayofyear
    frame["month"] = frame.timestamp_local.dt.month
    frame["weekday"] = frame.timestamp_local.dt.weekday
    frame["is_weekend"] = frame.weekday.isin([5, 6]).astype(int)

    calendar = holidays.Germany(years=sorted(frame.timestamp_local.dt.year.unique()))
    frame["is_holiday_de"] = frame.date_local.apply(lambda day: day in calendar).astype(
        int
    )
    frame["holiday_name_de"] = frame.date_local.apply(lambda day: calendar.get(day))

    frame["hour_sin"] = np.sin(2 * np.pi * frame.hour_local / 24)
    frame["hour_cos"] = np.cos(2 * np.pi * frame.hour_local / 24)
    frame["day_of_year_sin"] = np.sin(2 * np.pi * frame.day_of_year / 365.25)
    frame["day_of_year_cos"] = np.cos(2 * np.pi * frame.day_of_year / 365.25)

    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    frame.to_parquet(temporary, index=False)
    temporary.replace(path)

    return frame
