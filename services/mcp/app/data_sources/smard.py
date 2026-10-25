from pathlib import Path

import pandas as pd
import requests


def fetch_smard_solar_generation(
    start_date: str, end_date: str, output_dir: str, file_name: str
) -> pd.DataFrame:
    start = pd.Timestamp(start_date, tz="UTC")
    end = pd.Timestamp(end_date, tz="UTC") + pd.Timedelta(days=1)

    if start >= end:
        raise ValueError("start_date must not be after end_date")

    path = Path(__file__).resolve().parents[4] / output_dir / file_name
    url = "https://www.smard.de/app/chart_data/4068/DE"

    response = requests.get(f"{url}/index_hour.json", timeout=60)
    response.raise_for_status()
    blocks = sorted(response.json()["timestamps"])

    frames = []

    for i, block in enumerate(blocks):
        left = pd.Timestamp(block, unit="ms", tz="UTC")
        right = (
            pd.Timestamp(blocks[i + 1], unit="ms", tz="UTC")
            if i + 1 < len(blocks)
            else end
        )

        if left < end and right > start:
            response = requests.get(f"{url}/4068_DE_hour_{block}.json", timeout=60)
            response.raise_for_status()
            frames.append(
                pd.DataFrame(
                    response.json()["series"],
                    columns=["time", "target_solar_generation_mwh"],
                )
            )

    if not frames:
        raise ValueError("No SMARD blocks cover the requested interval")

    frame = pd.concat(frames, ignore_index=True)
    frame["timestamp_utc"] = pd.to_datetime(frame.pop("time"), unit="ms", utc=True)
    frame = frame.loc[
        frame.timestamp_utc.ge(start) & frame.timestamp_utc.lt(end)
    ].copy()

    if frame.empty:
        raise ValueError("SMARD returned no records for the requested interval")

    frame = (
        frame.drop_duplicates("timestamp_utc")
        .sort_values("timestamp_utc")
        .reset_index(drop=True)
    )
    frame["target_solar_generation_mw"] = frame.target_solar_generation_mwh

    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    frame.to_parquet(temporary, index=False)
    temporary.replace(path)

    return frame
