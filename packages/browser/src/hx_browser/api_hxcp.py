"""hxcp 控制端点 —— 阶段 1

POST /api/v1/environments/{env_id}/hxcp/start
POST /api/v1/environments/{env_id}/hxcp/stop
GET  /api/v1/environments/{env_id}/hxcp/status
"""

from __future__ import annotations

from fastapi import APIRouter, HTTPException
from fastapi.responses import JSONResponse

from . import hxcp_link
from .browser import get_runtime
from .store import get_store

router = APIRouter(prefix="/api/v1")


def ok(data=None, msg: str = "success") -> JSONResponse:
    return JSONResponse({"code": 0, "msg": msg, "data": data})


def fail(msg: str, code: int = -1) -> JSONResponse:
    return JSONResponse({"code": code, "msg": msg, "data": None})


@router.post("/environments/{env_id}/hxcp/start")
async def hxcp_start(env_id: str):
    env = get_store().get_env(env_id)
    if not env or env.deleted_at:
        raise HTTPException(404, "环境不存在")
    rt = get_runtime(env_id)
    if not rt or not rt.cdp_http:
        return fail("环境未运行，请先启动")
    pid, status = await hxcp_link.astart(env_id, rt.cdp_http, int(rt.pid or 0))
    if str(status).startswith("failed"):
        return fail(status)
    return ok({"pid": pid, "status": status, "endpoint": rt.cdp_http})


@router.post("/environments/{env_id}/hxcp/stop")
async def hxcp_stop(env_id: str):
    status = await hxcp_link.astop(env_id)
    return ok({"status": status})


@router.get("/environments/{env_id}/hxcp/status")
async def hxcp_status(env_id: str):
    return ok(hxcp_link.status(env_id))
