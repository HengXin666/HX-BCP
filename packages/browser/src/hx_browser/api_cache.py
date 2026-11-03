"""Read-only pass-through of GET <cache proxy>/stats for the console status page.

Agent Note: .agents/notes/implemented/feature/2026-11-01-console-on-8325.md
"""

from __future__ import annotations

import os

import httpx
from fastapi import APIRouter
from fastapi.responses import JSONResponse

router = APIRouter(prefix="/api/v1")
CACHE_URL = os.environ.get("HXCP_ADMIN_URL", "http://127.0.0.1:8898")


@router.get("/cache/stats")
async def cache_stats():
    """GET <cache proxy>/stats, 502 with a reason when it is not running."""
    try:
        async with httpx.AsyncClient(timeout=3.0, trust_env=False) as client:
            r = await client.get(CACHE_URL + "/stats")
    except httpx.HTTPError as e:
        return JSONResponse({"code": "cache_unreachable", "msg": f"{CACHE_URL} {e.__class__.__name__}"},
                            status_code=502)
    try:
        return JSONResponse(r.json(), status_code=r.status_code)
    except ValueError:
        return JSONResponse({"code": "cache_bad_body", "msg": f"HTTP {r.status_code}"}, status_code=502)
