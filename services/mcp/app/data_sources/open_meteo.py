import time
from pathlib import Path

import pandas as pd
import requests

POINTS = {
    "Hamburg": (53.55, 10.00),
    "Munich": (48.14, 11.58),
    "Berlin": (52.52, 13.41),
    "Cologne": (50.94, 6.96),
    "Frankfurt": (50.11, 8.68),
    "Nuremberg": (49.45, 11.08),
    "Stuttgart": (48.78, 9.18),
    "Dortmund": (51.51, 7.47),
    "Hannover": (52.37, 9.73),
    "Potsdam": (52.39, 13.06),
}
VARIABLES = [
    "shortwave_radiation",
    "direct_radiation",
    "diffuse_radiation",
    "cloud_cover",
    "cloud_cover_low",
    "cloud_cover_mid",
    "cloud_cover_high",
    "temperature_2m",
    "precipitation",
    "wind_speed_10m",
]


def fetch_open_meteo_historical_forecast_weather(
    start_date: str, end_date: str, output_dir: str, file_name: str
) -> pd.DataFrame:
    if pd.Timestamp(start_date) > pd.Timestamp(end_date):
        raise ValueError("start_date must not be after end_date")

    path = Path(__file__).resolve().parents[4] / output_dir / file_name

    frames = []

    for name, (latitude, longitude) in POINTS.items():
        last_error = None

        for attempt in range(3):
            try:
                response = requests.get(
                    "https://historical-forecast-api.open-meteo.com/v1/forecast",
                    params={
                        "latitude": latitude,
                        "longitude": longitude,
                        "start_date": start_date,
                        "end_date": end_date,
                        "hourly": ",".join(VARIABLES),
                        "timezone": "GMT",
                    },
                    timeout=60,
                )
                data = response.json()

                if (
                    response.ok
                    and isinstance(data, dict)
                    and not data.get("error")
                    and "hourly" in data
                ):
                    break

                last_error = (
                    data.get("reason", response.text[:300])
                    if isinstance(data, dict)
                    else response.text[:300]
                )
            except (requests.RequestException, ValueError) as exc:
                last_error = str(exc)

            if attempt < 2:
                time.sleep(10)
        else:
            raise RuntimeError(
                f"Open-Meteo request failed after 3 attempts: {last_error}"
            )

        frame = pd.DataFrame(data["hourly"])
        frame["timestamp_utc"] = pd.to_datetime(frame.pop("time"), utc=True)
        frame["location"] = name
        frame["latitude"] = latitude
        frame["longitude"] = longitude
        frames.append(frame)

    frame = pd.concat(frames, ignore_index=True)

    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    frame.to_parquet(temporary, index=False)
    temporary.replace(path)

    return frame
