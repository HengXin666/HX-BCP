"""Browser list and create, scoped by lane (moved out of api.py, which may only shrink).

Agent Note: .agents/notes/implemented/feature/2026-11-01-lane-folders.md
"""

from __future__ import annotations

import secrets

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from pydantic import BaseModel

from . import folders, lane_guard, profile_ops
from .envview import env_dict
from .models import Environment
from .store import get_store

router = APIRouter(prefix="/api/v1")


def ok(data=None) -> JSONResponse:
    return JSONResponse({"code": 0, "msg": "success", "data": data})


def fail(msg: str) -> JSONResponse:
    return JSONResponse({"code": -1, "msg": msg, "data": None})


def store():
    return get_store()


@router.get("/environments")
async def list_envs(group: str = "", search: str = "", page: int = 1, page_size: int = 100, tag: str = "",
                    folder: str = ""):
    """Browsers the caller may read, agent sees its own project, human sees all with readonly marks."""
    envs = [e for e in store().load_envs() if not e.deleted_at and lane_guard.visible(e)]
    if group:
        envs = [e for e in envs if e.group == group]
    if folder:
        envs = [e for e in envs if e.folder == folder]
    if search or tag:
        envs = [e for e in envs if profile_ops.matches(e, search, tag)]
    total = len(envs)
    start = (page - 1) * page_size
    items = [env_dict(e) for e in envs[start:start + page_size]]
    return ok({"list": items, "total": total, "page": page, "page_size": page_size})


class EnvCreate(BaseModel):
    name: str = ""
    group: str = "默认"
    folder: str = ""
    platform: str = "custom"
    tags: list[str] = []
    country: str = ""
    region: str = ""
    city: str = ""
    location_mode: str = "match_ip"
    account: dict | None = None
    proxy: dict | None = None
    fingerprint: dict | None = None
    launch: dict | None = None
    storage: dict | None = None
    prefs: dict | None = None
    notes: str = ""


@router.post("/environments")
async def create_env(payload: EnvCreate):
    """Create a browser, lane and project come from the request scope through store.add_env."""
    s = store()
    env = Environment(
        name=payload.name or f"Env-{len(s.load_envs()) + 1}",
        group=payload.group or "默认",
        platform=payload.platform or "custom",
        tags=payload.tags or [],
        country=payload.country,
        region=payload.region,
        city=payload.city,
        location_mode=payload.location_mode or "match_ip",
        notes=payload.notes,
        folder=payload.folder,
    )
    if not env.id:
        env.id = "env_" + secrets.token_hex(5)
    if payload.fingerprint:
        env.fingerprint = type(env.fingerprint)(**{**env.fingerprint.model_dump(), **payload.fingerprint})
    if payload.proxy:
        env.proxy = type(env.proxy)(**{**env.proxy.model_dump(), **payload.proxy})
    if payload.account:
        env.account = type(env.account)(**{**env.account.model_dump(), **payload.account})
    if payload.launch:
        env.launch = type(env.launch)(**{**env.launch.model_dump(), **payload.launch})
    if payload.storage:
        env.storage = type(env.storage)(**{**env.storage.model_dump(), **payload.storage})
    if payload.prefs:
        env.prefs = type(env.prefs)(**{**env.prefs.model_dump(), **payload.prefs})
    if errors := profile_ops.apply_extras(env, payload.model_dump()):
        return fail(", ".join(errors))
    try:
        s.add_env(env)
    except folders.FolderError as e:
        return fail(str(e))
    s.add_log("env.create", {"id": env.id, "name": env.name, "lane": env.lane, "project": env.project})
    return ok(env_dict(env))

