"""FastAPI 路由层（对应 PRD §4 API 规格）。"""

from __future__ import annotations

import contextlib
import json
import logging
from pathlib import Path

from fastapi import APIRouter, Depends, FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse, RedirectResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

from . import account_pack as ap
from . import api_fingerprint, api_hxcp, api_live, api_profiles, cdp, hxcp_link, mcp_http, profile_ops, relay
from .browser import (BrowserManager, RUNTIME, cleanup_orphans, get_runtime,
                      load_persisted_runtime, parse_cookies, scan_orphans,
                      wait_window_verdict)
from . import __version__
from .config import settings_manager
from .envview import apply_timezone_from_ip, env_dict
from .models import Environment, Extension, Fingerprint, Proxy, RuntimeInfo, Settings, Template, now_ms
from .proxy import check_proxy
from .store import get_store

log = logging.getLogger("hx_browser.api")

router = APIRouter(prefix="/api/v1")

# ---------- 依赖 ----------
_manager: BrowserManager | None = None


def manager() -> BrowserManager:
    global _manager
    s = settings_manager.load()
    _manager = _manager or BrowserManager(s, settings_manager.profiles_dir())
    _manager.settings = s  # 每次取用都刷新: PUT /settings 只写盘, 缓存快照会「设了没用」
    return _manager


def store():
    return get_store()


def ok(data=None, msg: str = "success") -> JSONResponse:
    return JSONResponse({"code": 0, "msg": msg, "data": data})


def fail(msg: str, code: int = -1, status: int = 200) -> JSONResponse:
    return JSONResponse({"code": code, "msg": msg, "data": None}, status_code=status)


# env_dict/apply_timezone_from_ip 在 envview.py (可见窗口契约唯一落点; 本文件行数只许降)。


# ---------- 状态 ----------
@router.get("/status")
async def status():
    from .browser import refresh_runtime

    refresh_runtime()
    s = settings_manager.load()
    return ok({
        "status": "ok",
        "app": "hx-browser",
        "version": __version__,
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
    kernel_paths: dict[str, str] | None = None


@router.put("/settings")
async def update_settings(payload: SettingsUpdate):
    s = settings_manager.load()
    for k, v in payload.model_dump(exclude_none=True).items():
        setattr(s, k, v)
    settings_manager.save(s)
    return ok(s.model_dump())


# ---------- 环境 CRUD ----------
@router.get("/environments")
async def list_envs(group: str = "", search: str = "", page: int = 1, page_size: int = 100, tag: str = ""):
    envs = [e for e in store().load_envs() if not e.deleted_at]  # 最近删除的不出现在列表里
    if group:
        envs = [e for e in envs if e.group == group]
    if search or tag:  # 搜索覆盖名称/id/备注/标签; tag=逗号分隔任一命中
        envs = [e for e in envs if profile_ops.matches(e, search, tag)]
    total = len(envs)
    start = (page - 1) * page_size
    items = [env_dict(e) for e in envs[start:start + page_size]]
    return ok({"list": items, "total": total, "page": page, "page_size": page_size})


@router.get("/environments/login-states")
async def env_login_states():
    """批量扫描全部环境的已登录账号 (前端展示用)。

    离线 (环境未运行): 读 cookie 库名称+过期时间, 按平台关键 cookie 判定 ok/expired/empty。
    运行中: 额外用 CDP 明文提取账号身份 (如 github 用户名)。
    """
    s = store()
    out: dict[str, dict] = {}
    for env in s.load_envs():
        env_dir = s.profiles_dir / env.id
        e: dict = {"running": False, "platforms": {}}
        rt = get_runtime(env.id)
        if rt and rt.debug_port:
            try:
                cookies = await cdp.get_cookies(rt.debug_port)
                states = ap.detect_login_states(cookies)
                ids = ap.detect_login_identities(cookies)
                for plat, det in states.items():
                    det["identity"] = ids.get(plat, "")
                e["platforms"] = states
                e["running"] = True
            except Exception as ex:  # noqa: BLE001
                e["error"] = str(ex)
        if not e["platforms"]:
            cookies = ap.scan_env_cookies(env_dir)
            e["platforms"] = ap.detect_login_states(cookies)
            e["offline"] = True
        out[env.id] = e
    return ok(out)


class EnvCreate(BaseModel):
    name: str = ""
    group: str = "默认"
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
    )
    if not env.id:
        env.id = "env_" + __import__("secrets").token_hex(5)
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
        return fail("；".join(errors))
    s.add_env(env)
    s.add_log("env.create", {"id": env.id, "name": env.name})
    return ok(env_dict(env))


@router.get("/environments/{env_id}")
async def get_env(env_id: str):
    env = store().get_env(env_id)
    if not env:
        raise HTTPException(404, "环境不存在")
    return ok(env_dict(env))


@router.put("/environments/{env_id}")
async def update_env(env_id: str, payload: dict):
    s = store()
    env = s.get_env(env_id)
    if not env:
        raise HTTPException(404, "环境不存在")
    if "name" in payload:
        env.name = str(payload["name"])
    if "group" in payload:
        env.group = str(payload["group"])
    if "platform" in payload:
        env.platform = str(payload["platform"])
    if "tags" in payload:
        env.tags = payload["tags"]
    if "location_mode" in payload:
        env.location_mode = str(payload["location_mode"])
    if "country" in payload:
        env.country = str(payload["country"])
    if "notes" in payload:
        env.notes = str(payload["notes"])
    for field in ("fingerprint", "proxy", "account", "launch", "storage", "prefs"):
        if field in payload and payload[field]:
            model = getattr(env, field)
            setattr(env, field, type(model)(**{**model.model_dump(), **payload[field]}))
    if errors := profile_ops.apply_extras(env, payload):
        return fail("；".join(errors))
    s.update_env(env)
    s.add_log("env.update", {"id": env.id})
    return ok(env_dict(env))


@router.delete("/environments/{env_id}")
async def delete_env(env_id: str, purge: bool = False):
    """默认移至最近删除 (可恢复); ?purge=true 为旧语义的一步彻底删除。见 profile_ops。"""
    try:
        return ok(await profile_ops.delete(store(), manager(), env_id, purge))
    except profile_ops.OpError as e:
        return fail(str(e))


@router.post("/environments/batch")
async def batch_create(payload: dict):
    count = max(0, min(int(payload.get("count", 0)), 200))
    base = payload.get("base") or {}
    s = store()
    created_ids = []
    for i in range(count):
        p = {**base, "name": f"{base.get('name', 'Env')}-{i + 1}"}
        env = Environment(
            name=p.get("name") or f"Env-{len(s.load_envs()) + 1}",
            group=p.get("group", "默认"),
            platform=p.get("platform", "custom"),
            location_mode=p.get("location_mode", "match_ip"),
        )
        env.id = "env_" + __import__("secrets").token_hex(5)
        if p.get("fingerprint"):
            env.fingerprint = type(env.fingerprint)(**{**env.fingerprint.model_dump(), **p["fingerprint"]})
        if p.get("proxy"):
            env.proxy = type(env.proxy)(**{**env.proxy.model_dump(), **p["proxy"]})
        s.add_env(env)
        created_ids.append(env.id)
    s.add_log("env.batch-create", {"count": len(created_ids)})
    return ok({"count": len(created_ids), "ids": created_ids})


@router.post("/environments/{env_id}/duplicate")
async def duplicate_env(env_id: str):
    s = store()
    src = s.get_env(env_id)
    if not src:
        raise HTTPException(404, "环境不存在")
    new_env = src.model_copy(deep=True)
    new_env.id = "env_" + __import__("secrets").token_hex(5)
    new_env.name = f"{src.name}-copy"
    new_env.runtime = {}
    s.add_env(new_env)
    s.add_log("env.duplicate", {"src": env_id, "id": new_env.id})
    return ok(env_dict(new_env))


# ---------- 启动/停止 ----------
@router.post("/environments/{env_id}/start")
async def start_env(env_id: str):
    s = store()
    env = s.get_env(env_id)
    if not env or env.deleted_at:
        raise HTTPException(404, "环境不存在" if not env else "环境在最近删除中, 请先恢复")
    m = manager()
    # match_ip + 代理：检测出口 IP 并对齐时区
    if env.proxy.type != "no_proxy" and env.proxy.host:
        geo = await check_proxy(env.proxy.model_dump())
        if not geo.get("ok") and settings_manager.load().proxy_check_before_launch:
            return fail(f"代理不可用: {geo.get('error')}")
        if geo.get("ok"):
            apply_timezone_from_ip(env, geo)
            s.update_env(env)
    try:
        info = await m.start(env)
    except Exception as e:  # noqa: BLE001
        return fail(str(e))
    # 使用痕迹 (2026-09-19 修): 这两个字段此前**全仓无人写入** ——
    # 43 个环境 open_count 恒为 0, 于是"哪个环境该清理"没有任何依据。
    # 清理必须依据最后使用时间: 删错 = 账号身份断裂 (legacy 骨架不能动)。
    env.open_count = int(env.open_count or 0) + 1
    env.last_open_at = now_ms()
    s.update_env(env)
    # 窗口断言在后台跑, 这里**等它出真值**再回响应 —— 否则 start 一返回就报
    # visible=true, 而窗口其实还没映射 (2026-10-02 gh-pool 栽过: 父进程拿不到真值,
    # 用户看到的是"点了没反应")。等不到就如实回落成 false + reason。
    await wait_window_verdict(env.id)
    info = get_runtime(env.id) or info
    await hxcp_link.amount(env, info)
    s.add_log("env.start", {"id": env.id, "port": info.debug_port,
                            "visible": info.visible, "display": info.display})
    d = env_dict(env)
    d["runtime"] = info.model_dump()
    return ok(d)


# 注意路径用顶层 /orphans 而**不是** /environments/orphans:
# 后者会被 /environments/{env_id} 抢先匹配, 把 "orphans" 当成 env_id -> 404 "环境不存在"
# (实测踩到)。FastAPI 按注册顺序匹配, 子路径里再挂固定段必然被通配吃掉。
@router.get("/orphans")
async def list_orphans():
    """在跑但无人认领的浏览器进程 (服务重启后 RUNTIME 失联的遗留)。

    为什么需要它: RUNTIME 是进程内存, 重启即失联 —— 实测重启后 11 个 chrome
    主进程无人记录、也停不掉, 白占内存与账号 profile, 面板上还看不到它们。
    """
    o = scan_orphans()
    return ok({"count": len(o), "orphans": o})


@router.post("/orphans/cleanup")
async def cleanup_orphans_api():
    """杀掉全部孤儿浏览器进程。显式动作, 不在启动路径里自动执行。"""
    killed = cleanup_orphans()
    store().add_log("orphans.cleanup", {"killed": killed})
    return ok({"killed": killed, "count": len(killed)})


@router.post("/environments/{env_id}/stop")
async def stop_env(env_id: str):
    env = store().get_env(env_id)
    if not env:
        raise HTTPException(404, "环境不存在")
    await hxcp_link.astop(env_id)
    await manager().stop(env)
    store().add_log("env.stop", {"id": env_id})
    return ok()


@router.get("/environments/{env_id}/status")
async def env_status(env_id: str):
    env = store().get_env(env_id)
    if not env:
        raise HTTPException(404, "环境不存在")
    return ok(env_dict(env))


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
        return fail("环境未运行，请先启动")
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
    """导入批量注册/现有 Firefox userdir 为环境（Firefox 登录态 100% 保留）。"""
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
    # 提取 Cookie 摘要写入 account 字段（便于 UI 查看/导出）
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


# CDP 失败必须是**结构化 JSON**, 不能让它变成 HTTP 500 text/plain (2026-10-13, T2 实测报告)。
# 真现场: 浏览器进程已死而 RUNTIME 未清 -> cdp.get_cookies 抛 CdpError
# ("no page target on port 43047") -> FastAPI 默认异常处理器回 500 + text/plain,
# 客户端只能把它归成 neterr, 面板拿到纯文本直接显示成解析错误。
# 判据: 这类失败是**业务态**(环境实际不可用), 不是服务端故障, 故回 200 + code=-1。
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
        return fail("环境未运行，请先启动")
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


# ---------- 代理 ----------
class ProxyCreate(BaseModel):
    label: str = ""
    type: str = "http"
    host: str = ""
    port: int = 0
    username: str = ""
    password: str = ""


@router.get("/proxies")
async def list_proxies():
    return ok({"list": [p.model_dump() for p in store().load_proxies()]})


@router.post("/proxies")
async def create_proxy(payload: ProxyCreate):
    p = Proxy(id="px_" + __import__("secrets").token_hex(4), **payload.model_dump())
    proxies = store().load_proxies()
    proxies.insert(0, p)
    store().save_proxies(proxies)
    return ok(p.model_dump())


@router.delete("/proxies/{proxy_id}")
async def delete_proxy(proxy_id: str):
    proxies = [p for p in store().load_proxies() if p.id != proxy_id]
    store().save_proxies(proxies)
    return ok()


class ProxyCheckReq(BaseModel):
    type: str = "http"
    host: str = ""
    port: int = 0
    username: str = ""
    password: str = ""


@router.post("/proxies/check")
async def proxy_check(payload: ProxyCheckReq):
    geo = await check_proxy(payload.model_dump())
    return ok(geo)


# ---------- 模板 ----------
class TemplateCreate(BaseModel):
    name: str = ""
    description: str = ""
    fingerprint: dict = {}
    proxy_strategy: dict = {}
    extensions: list[str] = []
    launch: dict = {}


@router.get("/templates")
async def list_templates():
    return ok({"list": [t.model_dump() for t in store().load_templates()]})


@router.post("/templates")
async def create_template(payload: TemplateCreate):
    t = Template(id="tpl_" + __import__("secrets").token_hex(4), created_at=now_ms(), **payload.model_dump())
    templates = store().load_templates()
    templates.insert(0, t)
    store().save_templates(templates)
    return ok(t.model_dump())


@router.delete("/templates/{tpl_id}")
async def delete_template(tpl_id: str):
    templates = [t for t in store().load_templates() if t.id != tpl_id]
    store().save_templates(templates)
    return ok()


@router.post("/templates/{tpl_id}/apply")
async def apply_template(tpl_id: str, count: int = 1):
    t = next((x for x in store().load_templates() if x.id == tpl_id), None)
    if not t:
        raise HTTPException(404, "模板不存在")
    created = []
    for i in range(max(0, min(count, 50))):
        env = Environment(
            name=f"{t.name}-{i + 1}",
            fingerprint=Fingerprint(**{**Fingerprint().model_dump(), **t.fingerprint}),
            launch=type(Environment().launch)(**{**Environment().launch.model_dump(), **t.launch}),
        )
        env.id = "env_" + __import__("secrets").token_hex(5)
        store().add_env(env)
        created.append(env)
    return ok({"count": len(created)})


# ---------- 扩展 ----------
class ExtensionCreate(BaseModel):
    name: str = ""
    path: str = ""
    categories: list[str] = []
    platforms: list[str] = []
    bind_env_ids: list[str] = []


@router.get("/extensions")
async def list_extensions():
    return ok({"list": [e.model_dump() for e in store().load_extensions()]})


@router.post("/extensions")
async def create_extension(payload: ExtensionCreate):
    e = Extension(id="ext_" + __import__("secrets").token_hex(4), created_at=now_ms(), **payload.model_dump())
    exts = store().load_extensions()
    exts.insert(0, e)
    store().save_extensions(exts)
    return ok(e.model_dump())


@router.delete("/extensions/{ext_id}")
async def delete_extension(ext_id: str):
    exts = [e for e in store().load_extensions() if e.id != ext_id]
    store().save_extensions(exts)
    return ok()


@router.post("/extensions/{ext_id}/bind")
async def bind_extension(ext_id: str, env_ids: list[str]):
    exts = store().load_extensions()
    for e in exts:
        if e.id == ext_id:
            e.bind_env_ids = env_ids
            # 同步到环境 launch.extensions
            for env in store().load_envs():
                if env.id in env_ids and e.path not in env.launch.extensions:
                    env.launch.extensions.append(e.path)
                if env.id not in env_ids and e.path in env.launch.extensions:
                    env.launch.extensions.remove(e.path)
                store().update_env(env)
    store().save_extensions(exts)
    return ok()


# GET /skill 已迁到 api_profiles.py (路径与返回形状不变)


# ---------- 日志 / 分组 ----------
@router.get("/logs")
async def list_logs(limit: int = 100):
    return ok({"list": store().load_logs(limit)})


@router.get("/groups")
async def list_groups():
    envs = store().load_envs()
    groups = store().load_groups()
    return ok({"groups": groups, "counts": {g: sum(1 for e in envs if e.group == g) for g in groups}})


def create_app() -> FastAPI:
    @contextlib.asynccontextmanager
    async def _lifespan(_app: FastAPI):
        await profile_ops.resume_relays(store())  # 接管的在跑副本: 原端口重建 relay, 否则代理全断
        yield
        await hxcp_link.astop_all()
        manager().stop_all()
        await relay.stop_all()

    app = FastAPI(title="HX-Browser", version=__version__, lifespan=_lifespan)
    try:
        n = load_persisted_runtime()
        hxcp_link.adopt_from_runtime()
        if n:
            log.info("接管 %d 个在跑的浏览器环境 (runtime.json)", n)
    except Exception as e:  # noqa: BLE001
        log.warning("接管运行态失败: %s", e)
    app.include_router(router)
    app.include_router(api_profiles.build_router(start_env, manager))  # 副本管理/检测修复/回收站
    app.include_router(api_live.build_router())  # 实时预览: 在跑副本列表 + WS 帧流/远程操作
    app.include_router(api_hxcp.router)  # hxcp 缓存代理挂载
    app.include_router(api_fingerprint.router)  # 指纹自检结论
    mcp_http.mount(app)  # MCP Streamable HTTP: POST /mcp (stdio 版见 mcp_server.py)

    @app.middleware("http")
    async def api_key_middleware(request: Request, call_next):
        s = settings_manager.load()
        if s.api_key:
            key = request.headers.get("x-api-key", "")
            if key != s.api_key and request.url.path != "/":
                return JSONResponse({"code": -1, "msg": "invalid api key"}, status_code=401)
        return await call_next(request)

    # 静态管理页
    web_dir = Path(__file__).parent / "web"
    if web_dir.exists():
        app.mount("/", StaticFiles(directory=str(web_dir), html=True), name="web")

    return app
