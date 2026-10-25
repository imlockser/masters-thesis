from datetime import date
from pathlib import Path

from pydantic_settings import BaseSettings, SettingsConfigDict

PROJECT_ROOT = Path(__file__).resolve().parents[3]


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=PROJECT_ROOT / "env" / "mcp.env",
        env_file_encoding="utf-8",
    )

    mcp_name: str
    mcp_host: str

    mlflow_tracking_uri: str
    solar_model_name: str
    solar_model_alias: str

    solar_capacity_file: Path
    solar_capacity_snapshot_date: date
    solar_cache_dir: Path


settings = Settings()  # pyright: ignore[reportCallIssue]
