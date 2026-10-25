import asyncio
import signal

from dbos import DBOS

from .providers.backend import backend_provider
from .providers.llm import llm_provider
from .providers.mcp import mcp_provider
from .settings import Settings
from .workflow import QUEUE_NAME


async def main() -> None:
    settings = Settings()  # pyright: ignore[reportCallIssue]

    llm_provider.setup(
        settings.llm_base_url,
        settings.llm_api_key,
        settings.llm_model,
        schema_attempts=1,
        timeout=60,
    )
    mcp_provider.setup(settings.mcp_url, timeout=60)
    backend_provider.setup(settings.backend_rpc_url)

    DBOS(
        config={
            "name": "energy-agent",
            "system_database_url": settings.dbos_system_database_url,
            "use_listen_notify": True,
            "enable_otlp": False,
        }
    )

    stopped = asyncio.Event()
    loop = asyncio.get_running_loop()

    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stopped.set)

    try:
        DBOS.listen_queues([QUEUE_NAME])
        DBOS.launch()
        await DBOS.register_queue_async(QUEUE_NAME, worker_concurrency=10)
        await stopped.wait()
    finally:
        await asyncio.to_thread(DBOS.destroy, workflow_completion_timeout_sec=10)
        await backend_provider.close()
        await mcp_provider.close()
        await llm_provider.close()


if __name__ == "__main__":
    asyncio.run(main())
