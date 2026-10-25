from . import tools  # noqa: F401
from .mcp import mcp
from .settings import settings


def main() -> None:
    mcp.run(transport="http", host=settings.mcp_host)


if __name__ == "__main__":
    main()
