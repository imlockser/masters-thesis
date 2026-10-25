import argparse

import mlflow
from mlflow.artifacts import load_dict
from mlflow.environment_variables import MLFLOW_ENABLE_PROXY_MULTIPART_DOWNLOAD
from mlflow.models import get_model_info

from ..providers.registry import registry_provider
from ..settings import settings
from .contract import CONTRACT
from .inference import load_solar_model


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Register and validate the selected solar model."
    )
    parser.add_argument(
        "model_uri", help="The pinned logged-model URI selected in notebook 04."
    )
    args = parser.parse_args()
    mlflow.set_tracking_uri(settings.mlflow_tracking_uri)
    MLFLOW_ENABLE_PROXY_MULTIPART_DOWNLOAD.set(False)
    info = get_model_info(args.model_uri)
    if not info.run_id:
        raise ValueError("The selected model has no source run.")

    original = load_dict(f"runs:/{info.run_id}/features.json")
    if original != {"features": CONTRACT.names, "target": CONTRACT.target}:
        raise ValueError(
            "The training feature contract does not match the solar pipeline."
        )

    registry_provider.setup(settings.mlflow_tracking_uri)

    try:
        version = mlflow.register_model(
            args.model_uri,
            settings.solar_model_name,
            tags={"inference_contract": CONTRACT.model_dump_json()},
        )
        load_solar_model(version)
        client = mlflow.MlflowClient()
        client.set_registered_model_alias(
            settings.solar_model_name, settings.solar_model_alias, version.version
        )
        print(
            f"Validated {settings.solar_model_name}@{settings.solar_model_alias} -> {version.version}"
        )
    finally:
        registry_provider.close()


if __name__ == "__main__":
    main()
