"""副本管理 / 检测与修复 / 回收站 / 内核 的 REST 路由 (2026-10-16)。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

为什么不写进 api.py: api.py 是行数棘轮登记的存量 (只许降不许涨)。本文件只做
HTTP 形状转换, 判据全在 profile_ops / doctor / repair_ops / netcheck。
响应外形沿用 api.py 的 {code, msg, data} (code=0 成功), 前端与 MCP 不用区分新旧接口。

路径约定 (api.py 里记过的坑): 顶层固定段 (/trash /tags /repairs /kernels) 不挂在
/environments/ 下, 否则会被 /environments/{env_id} 抢先匹配成 env_id。
"""

from __future__ import annotations

import asyncio
import time
from pathlib import Path

import markdown as md  # type: ignore[import-untyped]
from fastapi import APIRouter, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel

from . import (
    display_iso,
    doctor,
    hxcp_link,
    kernels,
    netcheck,
    profile_ops,
    repair_ops,
    sandbox,
    window,
)
from .browser import get_runtime
from .config import settings_manager
from .envview import env_dict
from .prefs import ProfilePrefs
from .profile_rules import validate_prefs
from .proxy_link import ProxyLinkError, parse_link
from .store import get_store

NOT_FOUND = "环境不存在"
SKILL_DOC = Path(__file__).resolve().parent.parent.parent / "docs" / "AI-GUIDE.md"


def ok(data=None, msg: str = "success") -> JSONResponse:
    return JSONResponse({"code": 0, "msg": msg, "data": data})


def fail(msg: str, code: int = -1) -> JSONResponse:
    return JSONResponse({"code": code, "msg": msg, "data": None})


def _env_or_404(env_id: str):
    env = get_store().get_env(env_id)
    if env is None:
        raise HTTPException(404, NOT_FOUND)
    return env


class LinkReq(BaseModel):
    link: str = ""


class FixReq(BaseModel):
    items: list[str] = []  # 空 = 一键修复全部可修复项


def build_router(start_env, manager) -> APIRouter:
    """start_env/manager 由 api.create_app 注入 (复用既有启动路由的全部逻辑, 不复制一份)。"""
    r = APIRouter(prefix="/api/v1")

    @r.get("/kernels")
    async def list_kernels():
        s = settings_manager.load()
        return ok(
            {
                "kernels": kernels.list_kernels(s),
                "sandbox": sandbox.status(),
                "display": display_iso.status(),  # 窗口系统: X11 / Wayland / Xephyr
            }
        )

    @r.get("/profiles")
    async def list_profiles(search: str = "", tag: str = "", deleted: bool = False):
        envs = [e for e in get_store().load_envs() if bool(e.deleted_at) == deleted]
        items = [env_dict(e) for e in envs if profile_ops.matches(e, search, tag)]
        return ok(
            {"list": items, "total": len(items), "tags": profile_ops.tag_counts(envs)}
        )

    @r.get("/tags")
    async def list_tags():
        envs = [e for e in get_store().load_envs() if not e.deleted_at]
        return ok(profile_ops.tag_counts(envs))

    @r.get("/trash")
    async def list_trash():
        envs = [env_dict(e) for e in get_store().load_envs() if e.deleted_at]
        return ok({"list": envs, "total": len(envs)})

    @r.post("/environments/{env_id}/restore")
    async def restore_env(env_id: str):
        try:
            return ok(profile_ops.restore(get_store(), env_id))
        except profile_ops.OpError as e:
            return fail(str(e))

    @r.post("/environments/{env_id}/purge")
    async def purge_env(env_id: str):
        try:
            return ok(await profile_ops.purge(get_store(), manager(), env_id))
        except profile_ops.OpError as e:
            return fail(str(e))

    @r.post("/environments/{env_id}/restart")
    async def restart_env(env_id: str):
        env = _env_or_404(env_id)
        await hxcp_link.astop(env_id)
        if get_runtime(env_id):
            await manager().stop(env)
        return await start_env(env_id)

    @r.post("/environments/{env_id}/window/focus")
    async def focus_window(env_id: str):
        _env_or_404(env_id)
        rt = get_runtime(env_id)
        if rt is None:
            return fail("环境未运行")
        if (rt.display_source_detail or "").startswith("wayland:"):
            # Wayland 不允许客户端抢焦点/查看他人窗口: 如实告知, 不假装成功
            return fail("原生 Wayland 窗口无法由外部程序定位, 请在桌面任务栏中切换")
        res = await asyncio.to_thread(
            window.activate_window, rt.pid, rt.window_id, rt.display, rt.xauth
        )
        res["visible_to_user"] = rt.display_source == "desktop"
        return ok(res) if res["ok"] else fail(res["err"])

    @r.get("/environments/{env_id}/prefs")
    async def get_prefs(env_id: str):
        return ok(_env_or_404(env_id).prefs.model_dump())

    @r.put("/environments/{env_id}/prefs")
    async def put_prefs(env_id: str, payload: dict):
        env = _env_or_404(env_id)
        try:
            env.prefs = ProfilePrefs(**{**env.prefs.model_dump(), **payload})
        except ValueError as e:
            return fail("设置格式无效: %s" % e)
        errors = validate_prefs(env)
        if errors:
            return fail("；".join(errors))
        get_store().update_env(env)
        get_store().add_log("env.prefs", {"id": env_id})
        return ok(env_dict(env))

    @r.post("/proxies/parse")
    async def parse_proxy(payload: LinkReq):
        try:
            cfg = parse_link(payload.link)
        except ProxyLinkError as e:
            return fail(str(e))
        cfg["has_password"] = bool(cfg.pop("password", ""))  # 回显不带密码
        return ok(cfg)

    @r.post("/environments/{env_id}/proxy/test")
    async def test_env_proxy(env_id: str):
        env = _env_or_404(env_id)
        start = time.monotonic()
        try:
            region = await netcheck.lookup_region(env)
        except Exception as e:  # noqa: BLE001
            return fail(str(e))
        region["latency_ms"] = int((time.monotonic() - start) * 1000)
        return ok(region)

    @r.get("/environments/{env_id}/doctor")
    async def run_doctor(env_id: str, network: bool = True):
        env = _env_or_404(env_id)
        return ok(await doctor.run(env, settings_manager.load(), with_network=network))

    @r.post("/environments/{env_id}/doctor/fix")
    async def fix_env(env_id: str, payload: FixReq | None = None):
        env = _env_or_404(env_id)
        report = await doctor.run(env, settings_manager.load(), with_network=True)
        try:
            res = repair_ops.apply_fixes(
                get_store(), env, report, (payload or FixReq()).items
            )
        except profile_ops.OpError as e:
            return fail(str(e))
        return ok(res)

    @r.get("/environments/{env_id}/repairs")
    async def env_repairs(env_id: str):
        return ok({"list": profile_ops.repair_store(get_store()).list(env_id)})

    @r.get("/repairs")
    async def all_repairs(env_id: str = ""):
        return ok({"list": profile_ops.repair_store(get_store()).list(env_id)})

    @r.post("/repairs/{record_id}/restore")
    async def restore_repair(record_id: str):
        try:
            return ok(repair_ops.restore(get_store(), record_id))
        except profile_ops.OpError as e:
            return fail(str(e))

    @r.get("/clock")
    async def clock(env_id: str = ""):
        if not env_id:
            return ok(
                netcheck.clock_unknown()
                | {"detail": "请指定 env_id: 时钟按该副本的网络路径比对"}
            )
        return ok(await netcheck.clock_check(_env_or_404(env_id)))

    @r.get("/skill")
    async def get_skill():
        """AI 操作手册（提示词页面）—— 自 api.py 迁来, 路径与返回形状不变。"""
        if not SKILL_DOC.exists():
            return fail("skill 文档不存在: docs/AI-GUIDE.md")
        raw = SKILL_DOC.read_text("utf-8")
        html = md.markdown(raw, extensions=["fenced_code", "tables", "toc"])
        return ok({"title": "HX-Browser AI 操作手册", "html": html, "markdown": raw,
                   "updated_at": int(SKILL_DOC.stat().st_mtime * 1000)})  # fmt: skip

    return r
