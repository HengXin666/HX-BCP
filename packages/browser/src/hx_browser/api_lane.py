"""Human lane console session: POST the local token once, get an HttpOnly cookie holding its hmac.

Agent Note: .agents/notes/implemented/feature/2026-11-01-console-on-8325.md
"""

from __future__ import annotations

import re

import lane
from fastapi import APIRouter, Body, Request

from . import lane_guard
from .api_common import fail, ok

router = APIRouter(prefix="/api/v1")
COOKIE_PATH = "/api/v1"
_BASE_RE = re.compile(r"^/(?:[A-Za-z0-9._~-]+/)*$")


def cookie_path(request: Request) -> str:
    """Cookie Path for the console session: <page base>api/v1, so it still matches behind a sub path proxy.

    The page sends its own directory in x-bcp-base (/ when opened on 8325, /proxy/hx-browser/ behind HX-WebX).
    Anything not shaped like a plain directory path falls back to /api/v1. Path is not a security boundary,
    the cookie stays HttpOnly and SameSite strict.
    """
    base = request.headers.get("x-bcp-base", "")
    if not _BASE_RE.match(base) or "/../" in base or "/./" in base:
        return COOKIE_PATH
    return base + "api/v1"


def _from_self(request: Request) -> bool:
    h = request.headers
    return lane.same_origin(h.get("host", ""), h.get("origin", ""), h.get("sec-fetch-site", ""))


@router.get("/lane/session")
async def session_state(request: Request):
    """Whether this caller is admitted to the human lane right now, and by what (loopback, session, token)."""
    d = lane_guard.decide_from(request)
    human = d.ok and d.lane == lane.HUMAN
    return ok({"human": human, "by": d.audit.get("by", "") if human else "", "ttl_s": lane.TOKEN_TTL_S})


@router.post("/lane/session")
async def session_open(request: Request, token: str = Body("", embed=True)):
    """Trade the local human token for a console session cookie, same-origin pages only."""
    if not _from_self(request):
        return fail("console session only for pages of this origin", code="lane_forbidden", status=403)
    if not lane.human_token_valid(token):
        return fail("wrong or missing local human token, see bcp-lane issue", code="human_token_required",
                    status=401)
    res = ok({"human": True, "ttl_s": lane.TOKEN_TTL_S})
    res.set_cookie(lane.SESSION_COOKIE, lane.issue_session(), max_age=lane.TOKEN_TTL_S, path=cookie_path(request),
                   httponly=True, samesite="strict")
    return res


@router.delete("/lane/session")
async def session_close(request: Request):
    """Drop the console session cookie."""
    res = ok({"human": False})
    res.delete_cookie(lane.SESSION_COOKIE, path=cookie_path(request), httponly=True, samesite="strict")
    return res
