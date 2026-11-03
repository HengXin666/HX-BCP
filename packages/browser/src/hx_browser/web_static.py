"""Static console page with cache headers: index.html is never cached, hashed assets are cached for a year.

Agent Note: .agents/notes/implemented/bug-fix/2026-11-01-console-subpath.md
"""

from __future__ import annotations

from fastapi.staticfiles import StaticFiles
from starlette.responses import Response
from starlette.types import Scope

NO_CACHE = "no-cache"
LONG_CACHE = "public, max-age=31536000, immutable"


def cache_control(path: str, media_type: str | None) -> str | None:
    """Cache-Control for one static path: vite hashed assets/ long, html no-cache, anything else untouched."""
    p = path[2:] if path.startswith("./") else path
    p = "" if p == "." else p.lstrip("/")
    if p.startswith("assets/"):
        return LONG_CACHE
    if (media_type or "").startswith("text/html") or p in ("", "index.html") or p.endswith(".html"):
        return NO_CACHE
    return None


class ConsoleStatic(StaticFiles):
    """StaticFiles that stamps Cache-Control on 200 and 304 answers."""

    async def get_response(self, path: str, scope: Scope) -> Response:
        res = await super().get_response(path, scope)
        if res.status_code in (200, 304):
            cc = cache_control(path, res.media_type or res.headers.get("content-type"))
            if cc:
                res.headers["cache-control"] = cc
        return res
