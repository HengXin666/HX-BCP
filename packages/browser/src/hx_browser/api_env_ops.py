"""Single browser read, update, delete, batch, duplicate, start, stop and status.

Agent Note: .agents/notes/implemented/architecture/2026-11-01-api-split.md
"""

from __future__ import annotations

from fastapi import APIRouter, HTTPException

from . import account_pack as ap
from . import cdp, folders, hxcp_link, lane_guard, profile_ops
from .api_common import fail, manager, ok, store
from .browser import get_runtime, wait_window_verdict
from .config import settings_manager
from .envview import apply_timezone_from_ip, env_dict
from .models import Environment, now_ms
from .proxy import check_proxy

router = APIRouter(prefix="/api/v1")


@router.get("/environments/login-states")
async def env_login_states():
    """批量扫描全部环境的已登录账号 (前端展示用).

    离线 (环境未运行): 读 cookie 库名称+过期时间, 按平台关键 cookie 判定 ok/expired/empty.
    运行中: 额外用 CDP 明文提取账号身份 (如 github 用户名).
    """
    s = store()
    out: dict[str, dict] = {}
    for env in (e for e in s.load_envs() if lane_guard.visible(e)):
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
    if "folder" in payload:  # lane and project are never taken from the payload
        try:
            env.folder = folders.ensure(lane_guard.group_of(env), str(payload["folder"])) if payload["folder"] else ""
        except folders.FolderError as e:
            return fail(str(e))
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
        return fail("; ".join(errors))
    s.update_env(env)
    s.add_log("env.update", {"id": env.id})
    return ok(env_dict(env))


@router.delete("/environments/{env_id}")
async def delete_env(env_id: str, purge: bool = False):
    """默认移至最近删除 (可恢复); ?purge=true 为旧语义的一步彻底删除. 见 profile_ops. """
    try:
        return ok(await profile_ops.delete(store(), manager(), env_id, purge))
    except profile_ops.OpError as e:
        return fail(str(e))


@router.post("/environments/batch")
async def batch_create(payload: dict):
    """With "action": batch start/stop/move/tags/delete on existing ids (env_batch). Without: batch create."""
    if payload.get("action"):
        from . import env_batch

        return await env_batch.run(payload)
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
    # match_ip + 代理: 检测出口 IP 并对齐时区
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
    # 使用痕迹 (2026-09-19 修): 这两个字段此前**全仓无人写入** --
    # 43 个环境 open_count 恒为 0, 于是"哪个环境该清理"没有任何依据.
    # 清理必须依据最后使用时间: 删错 = 账号身份断裂 (legacy 骨架不能动).
    env.open_count = int(env.open_count or 0) + 1
    env.last_open_at = now_ms()
    s.update_env(env)
    # 窗口断言在后台跑, 这里**等它出真值**再回响应 -- 否则 start 一返回就报
    # visible=true, 而窗口其实还没映射 (2026-10-02 gh-pool 栽过: 父进程拿不到真值,
    # 用户看到的是"点了没反应"). 等不到就如实回落成 false + reason.
    await wait_window_verdict(env.id)
    info = get_runtime(env.id) or info
    await hxcp_link.amount(env, info)
    s.add_log("env.start", {"id": env.id, "port": info.debug_port,
                            "visible": info.visible, "display": info.display})
    d = env_dict(env)
    d["runtime"] = info.model_dump()
    return ok(d)


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
