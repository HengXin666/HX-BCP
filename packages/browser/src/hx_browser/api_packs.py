"""Account packs, profile import, cookies, cache clearing and live fingerprint check.

Agent Note: .agents/notes/implemented/architecture/2026-11-01-api-split.md
"""

from __future__ import annotations

import json
import logging
from pathlib import Path

from fastapi import APIRouter, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel

from . import account_pack as ap
from . import cdp
from .api_common import fail, ok, store
from .browser import get_runtime, parse_cookies
from .config import settings_manager
from .envview import env_dict
from .models import Environment, Fingerprint

log = logging.getLogger("hx_browser.api")

router = APIRouter(prefix="/api/v1")


# ---------- 账号包 ----------
class ExportPackReq(BaseModel):
    password: str = ""


@router.post("/environments/{env_id}/account-pack/export")
async def export_pack(env_id: str, payload: ExportPackReq | None = None):
    env = store().get_env(env_id)
    if not env:
        raise HTTPException(404, "环境不存在")
    rt = get_runtime(env_id)
    if not rt or not rt.debug_port:
        return fail("环境未运行, 请先启动")
    pack = await ap.export_account_pack(env, rt.debug_port, (payload.password if payload else ""))
    store().add_log("account-pack.export", {"id": env_id, "size_bytes": ap.pack_size_bytes(pack)})
    return ok(pack)


class ImportPackReq(BaseModel):
    pack: dict
    password: str = ""
    target_env_id: str = ""
    name: str = ""


@router.post("/account-packs/import")
async def import_pack(payload: ImportPackReq):
    s = store()
    target = s.get_env(payload.target_env_id) if payload.target_env_id else None
    env = await ap.import_account_pack(payload.pack, payload.password, target)
    if payload.name:
        env.name = payload.name
    if target is None:
        if not env.id:
            env.id = "env_" + __import__("secrets").token_hex(5)
        s.add_env(env)
    else:
        s.update_env(env)
    s.add_log("account-pack.import", {"id": env.id})
    return ok(env_dict(env))


class ImportProfileReq(BaseModel):
    profile_dir: str
    mode: str = "copy"  # copy | move | reference
    name: str = ""
    fingerprint: dict | None = None


@router.post("/environments/import-profile")
async def import_profile(payload: ImportProfileReq):
    """导入批量注册/现有 Firefox userdir 为环境 (Firefox 登录态 100% 保留) . """
    s = store()
    src = Path(payload.profile_dir).expanduser()
    if not src.exists():
        return fail(f"profile 目录不存在: {src}")
    env = Environment(
        name=payload.name or f"Profile-{src.name}",
        platform="custom",
        fingerprint=Fingerprint(browser="flower"),
    )
    if payload.fingerprint:
        env.fingerprint = Fingerprint(**{**env.fingerprint.model_dump(), **payload.fingerprint})
    env.id = "env_" + __import__("secrets").token_hex(5)
    s.add_env(env)
    dst = settings_manager.profiles_dir() / env.id
    n = ap.import_firefox_profile(src, dst, payload.mode)
    # 提取 Cookie 摘要写入 account 字段 (便于 UI 查看/导出)
    try:
        real = ap.effective_profile_dir(dst)
        cookies = ap.read_firefox_cookies(real)
        if cookies:
            env.account.cookies = json.dumps(cookies, ensure_ascii=False)
            s.update_env(env)
    except Exception as e:  # noqa: BLE001
        log.warning("profile cookie scan failed: %s", e)
    s.add_log("env.import-profile", {"id": env.id, "mode": payload.mode, "files": n})
    return ok(env_dict(env))


# ---------- Cookie ----------
class CookiesInjectReq(BaseModel):
    cookies: str | list | None = None
    domains: list[str] | None = None


# CDP 失败必须是**结构化 JSON**, 不能让它变成 HTTP 500 text/plain (2026-10-13, T2 实测报告).
# 真现场: 浏览器进程已死而 RUNTIME 未清 -> cdp.get_cookies 抛 CdpError
# ("no page target on port 43047") -> FastAPI 默认异常处理器回 500 + text/plain,
# 客户端只能把它归成 neterr, 面板拿到纯文本直接显示成解析错误.
# 判据: 这类失败是**业务态**(环境实际不可用), 不是服务端故障, 故回 200 + code=-1.
def _cdp_fail(env_id: str, action: str, exc: Exception) -> JSONResponse:
    log.warning("env=%s %s failed: %s", env_id, action, exc)
    return fail("环境 %s 的浏览器不可用 (%s 失败): %s" % (env_id, action, str(exc)[:200]))


@router.get("/environments/{env_id}/cookies")
async def get_env_cookies(env_id: str):
    rt = get_runtime(env_id)
    if not rt or not rt.debug_port:
        return fail("环境未运行")
    try:
        cookies = await cdp.get_cookies(rt.debug_port)
    except Exception as e:  # noqa: BLE001
        return _cdp_fail(env_id, "读取 cookie", e)
    return ok({"total": len(cookies), "cookies": cookies})


@router.post("/environments/{env_id}/cookies")
async def inject_cookies(env_id: str, payload: CookiesInjectReq):
    rt = get_runtime(env_id)
    if not rt or not rt.debug_port:
        return fail("环境未运行, 请先启动")
    raw = payload.cookies
    if isinstance(raw, list):
        cookies = raw
    elif isinstance(raw, str):
        cookies = parse_cookies(raw)
    else:
        env = store().get_env(env_id)
        cookies = parse_cookies(env.account.cookies if env else "")
    if payload.domains:
        cookies = [c for c in cookies if any(c.get("domain", "").endswith(d) for d in payload.domains)]
    try:
        n = await cdp.set_cookies(rt.debug_port, cookies)
    except Exception as e:  # noqa: BLE001
        return _cdp_fail(env_id, "注入 cookie", e)
    return ok({"injected": n})


# ---------- 缓存清理 ----------
class CacheClearReq(BaseModel):
    types: list[str] = ["cookies", "localstorage", "indexeddb", "cache", "history"]


@router.post("/environments/{env_id}/cache/clear")
async def clear_cache(env_id: str, payload: CacheClearReq | None = None):
    rt = get_runtime(env_id)
    if not rt:
        return fail("环境未运行")
    cleared = []
    for t in (payload.types if payload else ["cookies"]):
        if t == "cookies":
            try:
                await cdp.evaluate(rt.debug_port, "location.reload()")  # noop guard
            except Exception:
                pass
            page_ws = await cdp.find_page_ws(rt.debug_port, timeout=8)
            conn = await cdp.CdpConnection(page_ws).connect(timeout=6)
            try:
                await conn.send("Network.enable")
                await conn.send("Network.clearBrowserCookies")
            finally:
                await conn.close()
            cleared.append("cookies")
        elif t in ("localstorage", "indexeddb", "cache", "history"):
            expr = {
                "localstorage": "localStorage.clear()",
                "indexeddb": "indexedDB.databases().then(ds=>ds.map(d=>indexedDB.deleteDatabase(d.name)))",
                "cache": "caches.keys().then(ks=>ks.map(k=>caches.delete(k)))",
                "history": "history.replaceState(null,'','/')",
            }[t]
            try:
                await cdp.evaluate(rt.debug_port, expr)
                cleared.append(t)
            except Exception as e:  # noqa: BLE001
                log.warning("clear %s failed: %s", t, e)
    return ok({"cleared": cleared})


# ---------- 指纹检测 ----------
@router.get("/environments/{env_id}/fingerprint/check")
async def fingerprint_check(env_id: str):
    rt = get_runtime(env_id)
    if not rt:
        return fail("环境未运行")
    expr = """JSON.stringify({
      ua: navigator.userAgent,
      platform: navigator.platform,
      language: navigator.language,
      langs: navigator.languages,
      cores: navigator.hardwareConcurrency,
      memory: navigator.deviceMemory,
      tz: new Date().toString().match(/\\(([^)]+)\\)/)?.[1] || Intl.DateTimeFormat().resolvedOptions().timeZone,
      screen: screen.width + 'x' + screen.height,
      dpr: window.devicePixelRatio,
      webgl: (()=>{try{const c=document.createElement('canvas');const g=c.getContext('webgl');if(!g)return 'none';return g.getParameter(g.VENDOR)+'|'+g.getParameter(g.RENDERER)}catch(e){return 'error'}})(),
      webdriver: navigator.webdriver
    })"""
    try:
        val = await cdp.evaluate(rt.debug_port, expr)
        return ok(json.loads(val) if isinstance(val, str) else val)
    except Exception as e:  # noqa: BLE001
        return fail(f"检测失败: {e}")
