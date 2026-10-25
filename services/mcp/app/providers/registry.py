from collections.abc import Callable
from threading import Lock
from typing import Any

from mlflow import MlflowClient
from mlflow.entities.model_registry import ModelVersion
from mlflow.environment_variables import (
    MLFLOW_HTTP_REQUEST_MAX_RETRIES,
    MLFLOW_HTTP_REQUEST_TIMEOUT,
)
from pydantic import BaseModel, ConfigDict


class ActiveModel(BaseModel):
    model_config = ConfigDict(frozen=True)

    version: str
    value: Any


class RegistryProvider:
    def __init__(self) -> None:
        self._client: MlflowClient | None = None
        self._active: dict[str, ActiveModel] = {}
        self._refresh_lock = Lock()

    def setup(self, tracking_uri: str) -> None:
        if self._client is not None:
            raise RuntimeError("RegistryProvider is already initialized.")

        MLFLOW_HTTP_REQUEST_TIMEOUT.set(30)
        MLFLOW_HTTP_REQUEST_MAX_RETRIES.set(0)

        self._client = MlflowClient(
            tracking_uri=tracking_uri, registry_uri=tracking_uri
        )

    def get(self, name: str) -> Any:
        if name not in self._active:
            raise RuntimeError(f"No validated model is loaded for {name}.")

        return self._active[name].value

    def refresh(
        self, name: str, alias: str, prepare: Callable[[ModelVersion], Any]
    ) -> bool:
        if self._client is None:
            raise RuntimeError("RegistryProvider is not initialized.")

        with self._refresh_lock:
            version = self._client.get_model_version_by_alias(name, alias)
            current = self._active.get(name)
            if current is not None and current.version == version.version:
                return False

            prepared = prepare(version)
            self._active[name] = ActiveModel(version=version.version, value=prepared)

            return True

    def close(self) -> None:
        with self._refresh_lock:
            self._active.clear()
            self._client = None


registry_provider = RegistryProvider()
