"""Lane scope of each request: admission and ownership come from packages/lane only.

Agent Note: .agents/notes/implemented/feature/2026-11-01-lane-folders.md
"""

from __future__ import annotations

import contextvars
import re

import lane
from fastapi import Request
from fastapi.responses import JSONResponse

API_PREFIX = "/api/v1"
PUBLIC_PATHS = {"/api/v1/status", "/api/v1/lane/session"}
ENV_PATH = re.compile(r"^/api/v1/environments/([^/]+)(/.*)?$")
ENV_FIXED = {"batch", "import-profile", "login-states"}
REPAIR_PATH = re.compile(r"^/api/v1/repairs/([^/]+)/restore$")
ENV_QUERY_PATHS = {"/api/v1/clock", "/api/v1/repairs"}
AGENT_PATHS = {
    "/api/v1/environments",
    "/api/v1/environments/batch",
    "/api/v1/environments/import-profile",
    "/api/v1/environments/login-states",
    "/api/v1/account-packs/import",
    "/api/v1/profiles",
    "/api/v1/tags",
    "/api/v1/trash",
    "/api/v1/live/envs",
    "/api/v1/kernels",
    "/api/v1/proxies/parse",
    "/api/v1/clock",
    "/api/v1/skill",
}
AGENT_PREFIXES = ("/api/v1/folders",)
READ_METHODS = {"GET", "HEAD", "OPTIONS"}
HTTP_STATUS = {"project_required": 400, "human_token_required": 401, "lane_forbidden": 403}
FORBIDDEN_ENV = "this lane may not operate this browser, agent sees only its own project, human reads agent browsers only"
FORBIDDEN_PATH = "agent lane may not call this endpoint"

_scope: contextvars.ContextVar[lane.Decision | None] = contextvars.ContextVar("bcp_lane_scope", default=None)


def decide_from(conn) -> lane.Decision:
    """Admission decision for one request or websocket (an HTTPConnection).

    project from header or ?project=; human proof is a loopback socket peer, the console session
    cookie when the browser marks the request as coming from a page of this origin, or the token
    header (rules and order in lane.decide). The peer is conn.client, the socket address uvicorn
    reports; hxb serve runs uvicorn with proxy_headers off so no X-Forwarded-For can rewrite it.
    """
    headers, query = conn.headers, conn.query_params
    project = headers.get(lane.HEADER_PROJECT, "") or query.get("project", "")
    from_self = lane.same_origin(headers.get("host", ""), headers.get("origin", ""), headers.get("sec-fetch-site", ""))
    peer = conn.client.host if conn.client else ""
    return lane.decide(headers.get(lane.HEADER_LANE, ""), headers.get(lane.HEADER_HUMAN_TOKEN, ""), project,
                       session=conn.cookies.get(lane.SESSION_COOKIE, ""), from_self=from_self,
                       loopback=lane.loopback_admitted(peer, headers.get("host", "")))


def current() -> lane.Decision | None:
    """Decision of the request being served, None for internal callers outside a request."""
    return _scope.get()


def group_of(env) -> str:
    """Grouping key of a browser environment."""
    return lane.group_key(env.lane, env.project)


def own_group() -> str:
    """Grouping key the current caller writes into, human when there is no request scope."""
    d = _scope.get()
    return lane.group_key(d.lane, d.project) if d else lane.HUMAN


def visible(env) -> bool:
    """True when the current caller may read env, internal callers see everything."""
    d = _scope.get()
    return d is None or lane.may_operate(d, env.lane, env.project, write=False)


def writable(env) -> bool:
    """True when the current caller may change env, internal callers may change everything."""
    d = _scope.get()
    return d is None or lane.may_operate(d, env.lane, env.project, write=True)


def stamp(env) -> None:
    """Write lane and project of the current caller onto a new environment and register its folder."""
    d = _scope.get()
    if d is not None:
        env.lane = d.lane
        env.project = d.project if d.lane == lane.AGENT else ""
    if env.folder:
        from . import folders

        env.folder = folders.ensure(group_of(env), env.folder)


def _deny(code: str, reason: str) -> JSONResponse:
    return JSONResponse({"code": code, "msg": reason, "data": None}, status_code=HTTP_STATUS[code])


def _env_id(path: str, query) -> str:
    m = ENV_PATH.match(path)
    if m and m.group(1) not in ENV_FIXED:
        return m.group(1)
    m = REPAIR_PATH.match(path)
    if m:
        from .profile_ops import repair_store
        from .store import get_store

        rec = repair_store(get_store()).get(m.group(1)) or {}
        return str(rec.get("env_id", "")) or "-"
    return query.get("env_id", "") if path in ENV_QUERY_PATHS else ""


def _agent_path(path: str) -> bool:
    return path in AGENT_PATHS or path.startswith(AGENT_PREFIXES)


def refusal(d: lane.Decision, method: str, path: str, query) -> JSONResponse | None:
    """Refusal response for an admitted request touching a browser it does not own, else None."""
    env_id = _env_id(path, query)
    if env_id:
        from .store import get_store

        env = get_store().get_env(env_id)
        if env and not lane.may_operate(d, env.lane, env.project, write=method not in READ_METHODS):
            return _deny("lane_forbidden", FORBIDDEN_ENV)
        return None
    if d.lane == lane.AGENT and not _agent_path(path):
        return _deny("lane_forbidden", FORBIDDEN_PATH)
    return None


async def middleware(request: Request, call_next):
    """HTTP middleware: admit by lane, check ownership, keep the decision for the handler."""
    path = request.url.path
    if not path.startswith(API_PREFIX) or path in PUBLIC_PATHS:
        return await call_next(request)
    d = decide_from(request)
    if not d.ok:
        return _deny(d.code, d.reason)
    refused = refusal(d, request.method, path, request.query_params)
    if refused is not None:
        return refused
    token = _scope.set(d)
    try:
        return await call_next(request)
    finally:
        _scope.reset(token)


def ws_refusal(ws, env) -> str:
    """Reason to refuse a live websocket on env, empty when allowed; live input counts as a write."""
    d = decide_from(ws)
    if not d.ok:
        return d.reason
    if not lane.may_operate(d, env.lane, env.project, write=True):
        return FORBIDDEN_ENV
    return ""
