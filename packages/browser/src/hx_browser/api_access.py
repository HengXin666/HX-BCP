"""External access card of the console: how an agent connects, which endpoints it may call, key rotation.

Human lane only (lane_guard refuses agents on these paths). The key itself is returned so the owner
can copy it into an agent config; agents never reach this router.
Agent Note: .agents/notes/implemented/feature/2026-11-22-lane-convergence.md
"""

from __future__ import annotations

import secrets

import lane
from fastapi import APIRouter, Request

from . import api_key_gate, lane_guard, mcp_tools
from .api_common import ok
from .config import settings_manager

router = APIRouter(prefix="/api/v1")


def _base(request: Request) -> str:
    return str(request.base_url).rstrip("/")


@router.get("/access")
async def access_info(request: Request):
    """Everything an external agent needs: base url, headers, key, allowed endpoints, MCP tools."""
    s = settings_manager.load()
    base = _base(request)
    return ok({
        "base": base,
        "api_key": s.api_key,
        "headers": {api_key_gate.KEY_HEADER: "<api_key>", lane.HEADER_PROJECT: "<project>"},
        "project_rule": lane.PROJECT_PATTERN.pattern,
        "agent_paths": sorted(lane_guard.AGENT_PATHS) + [p + "/*" for p in lane_guard.AGENT_PREFIXES]
        + ["/api/v1/environments/{id}/* (only its own project)"],
        "mcp": {"url": base + "/mcp", "tools": [t.get("name", "") for t in mcp_tools.tool_list()]},
        "human_loopback": s.human_loopback,
        "rules": [
            "x-api-key present means agent lane, x-bcp-lane: human is refused (403 lane_forbidden)",
            "agent lane needs x-bcp-project, missing gives 400 project_required",
            "agent sees and changes only browsers of its own project",
            "human lane is the console session cookie or the local token, never the api key",
        ],
    })


@router.post("/access/rotate-key")
async def rotate_key():
    """Replace the api_key, every agent config must be updated afterwards."""
    s = settings_manager.load()
    s.api_key = secrets.token_urlsafe(api_key_gate.KEY_BYTES)
    settings_manager.save(s)
    return ok({"api_key": s.api_key})
