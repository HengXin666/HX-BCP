"""Settings api_key check of 8325: which requests need x-api-key and which human proofs waive it.

Agent Note: .agents/notes/implemented/bug-fix/2026-11-15-console-api-key-401.md
Agent Note: .agents/notes/implemented/feature/2026-11-22-lane-convergence.md
"""

from __future__ import annotations

import secrets

import lane
from fastapi import Request
from starlette.requests import HTTPConnection

from . import lane_guard

KEY_HEADER = "x-api-key"
GUARDED_PREFIXES = ("/api/", "/mcp", "/docs", "/redoc", "/openapi.json")
OPEN_PATHS = {"/api/v1/lane/session", "/api/v1/status"}  # status is the health probe, same set as lane_guard.PUBLIC_PATHS
WAIVING_PROOFS = {"loopback", "session", "token"}
KEY_BYTES = 24


def guarded(path: str) -> bool:
    """True when path is an api, mcp or openapi docs endpoint, static console files are not guarded."""
    return path.startswith(GUARDED_PREFIXES) and path not in OPEN_PATHS


def key_ok(request: Request, api_key: str) -> bool:
    """True when the x-api-key header equals the configured key, compared in constant time."""
    sent = request.headers.get(KEY_HEADER, "")
    return bool(sent) and secrets.compare_digest(sent.encode(), api_key.encode())


def human_waiver(request: HTTPConnection) -> bool:
    """True when the request is admitted to the human lane by loopback, console session cookie or local token."""
    if request.headers.get(lane.HEADER_LANE, "").strip().lower() != lane.HUMAN:
        return False
    d = lane_guard.decide_from(request)
    return d.ok and d.lane == lane.HUMAN and d.audit.get("by", "") in WAIVING_PROOFS


def human_declared(request: Request) -> bool:
    """A /api/v1 request that says x-bcp-lane: human, leaving the proof to lane_guard which owns that path."""
    path = request.url.path
    return path.startswith(lane_guard.API_PREFIX + "/") and \
        request.headers.get(lane.HEADER_LANE, "").strip().lower() == lane.HUMAN


def admitted(request: Request, api_key: str) -> bool:
    """True when this request may pass the api_key check.

    Unguarded paths (console files, lane session) always pass. A human proof waives the key. Every
    other caller needs the key, and with no key configured nothing but the human lane gets in:
    ensure_key generates one at startup, so an empty key only happens if settings were edited by hand.
    """
    if not guarded(request.url.path):
        return True
    if human_declared(request):
        return True  # lane_guard admits or refuses it with the precise code (human_token_required, lane_forbidden)
    if human_waiver(request):
        return True
    return bool(api_key) and key_ok(request, api_key)


def ensure_key(manager) -> str:
    """Generate and persist an api_key when settings have none, return the key in effect."""
    s = manager.load()
    if not s.api_key:
        s.api_key = secrets.token_urlsafe(KEY_BYTES)
        manager.save(s)
    return s.api_key
