from pathlib import Path

from pydantic_settings import BaseSettings, SettingsConfigDict

PROJECT_ROOT = Path(__file__).resolve().parents[3]


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=PROJECT_ROOT / "env" / "agent.env", env_file_encoding="utf-8"
    )

    dbos_system_database_url: str
    llm_base_url: str
    llm_api_key: str
    llm_model: str
    mcp_url: str
    backend_rpc_url: str
