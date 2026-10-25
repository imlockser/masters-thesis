from fastmcp import FastMCP

from .settings import settings
from .solar.jobs import lifespan

mcp = FastMCP(settings.mcp_name, lifespan=lifespan)
