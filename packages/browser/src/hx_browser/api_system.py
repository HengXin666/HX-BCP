"""Service status, settings, orphan processes, logs and groups.

Agent Note: .agents/notes/implemented/architecture/2026-11-01-api-split.md
"""

from __future__ import annotations

from fastapi import APIRouter
from pydantic import BaseModel

from .api_common import ok, store
from .browser import RUNTIME, cleanup_orphans, scan_orphans
from .config import settings_manager

router = APIRouter(prefix="/api/v1")


# ---------- 状态 ----------
@router.get("/status")
async def status():
    from .browser import refresh_runtime

    refresh_runtime()
    s = settings_manager.load()
    return ok({
        "status": "ok",
        "app": "hx-browser",
        "version": "0.1.0",
        "api_port": s.api_port,
        "cdp_base_port": s.cdp_base_port,
        "running": len(RUNTIME),
        "chromium": s.chromium_path or None,
        "firefox": s.firefox_path or None,
    })


# ---------- 设置 ----------
@router.get("/settings")
async def get_settings():
    return ok(settings_manager.load().model_dump())


class SettingsUpdate(BaseModel):
    chromium_path: str | None = None
    firefox_path: str | None = None
    data_dir: str | None = None
    cdp_base_port: int | None = None
    api_key: str | None = None
    proxy_check_before_launch: bool | None = None
    headless_default: bool | None = None
    default_homepage: str | None = None
    auto_match_timezone: bool | None = None
    dark_mode: bool | None = None


@router.put("/settings")
async def update_settings(payload: SettingsUpdate):
    s = settings_manager.load()
    for k, v in payload.model_dump(exclude_none=True).items():
        setattr(s, k, v)
    settings_manager.save(s)
    return ok(s.model_dump())


# 注意路径用顶层 /orphans 而**不是** /environments/orphans:
# 后者会被 /environments/{env_id} 抢先匹配, 把 "orphans" 当成 env_id -> 404 "环境不存在"
# (实测踩到). FastAPI 按注册顺序匹配, 子路径里再挂固定段必然被通配吃掉.
@router.get("/orphans")
async def list_orphans():
    """在跑但无人认领的浏览器进程 (服务重启后 RUNTIME 失联的遗留).

    为什么需要它: RUNTIME 是进程内存, 重启即失联 -- 实测重启后 11 个 chrome
    主进程无人记录, 也停不掉, 白占内存与账号 profile, 面板上还看不到它们.
    """
    o = scan_orphans()
    return ok({"count": len(o), "orphans": o})


@router.post("/orphans/cleanup")
async def cleanup_orphans_api():
    """杀掉全部孤儿浏览器进程. 显式动作, 不在启动路径里自动执行. """
    killed = cleanup_orphans()
    store().add_log("orphans.cleanup", {"killed": killed})
    return ok({"killed": killed, "count": len(killed)})


# ---------- 日志 / 分组 ----------
@router.get("/logs")
async def list_logs(limit: int = 100):
    return ok({"list": store().load_logs(limit)})


@router.get("/groups")
async def list_groups():
    envs = store().load_envs()
    groups = store().load_groups()
    return ok({"groups": groups, "counts": {g: sum(1 for e in envs if e.group == g) for g in groups}})
