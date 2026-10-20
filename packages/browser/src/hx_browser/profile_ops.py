"""副本管理动作: 搜索/标签筛选、代理链接、最近删除 (回收站) / 恢复 / 彻底删除。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 删除分两步 (与 nodecloak 一致)

    DELETE /environments/{id}            -> 移至最近删除 (deleted_at=now), 数据一字不动
    POST   /environments/{id}/restore    -> 从最近删除恢复
    POST   /environments/{id}/purge      -> 彻底删除 (只对已在最近删除里的副本)
    DELETE /environments/{id}?purge=true -> 旧调用方的一步硬删 (兼容改动前语义)

彻底删除清理: profile 目录 / 环境记录 (含代理密码、账号密码、2FA、Cookie 串) /
运行态 / 本地 relay / 该副本的修复记录与修复备份。**不**删共享代理库里的条目 ——
那是多个副本共用的, 删了会连带别的副本。
"""

from __future__ import annotations

import logging
import os
import shutil
from pathlib import Path

from . import bridge, hxcp_link, kernels, relay, sandbox
from .browser import RUNTIME, get_runtime
from .models import ProxyConfig, now_ms
from .profile_rules import validate_prefs
from .proxy_link import (
    ProxyLinkError,
    egress_upstream,
    parse_link,
    upstream_of,
    validate_config,
)
from .repairs import RepairStore

LINK_KEY = "link"
log = logging.getLogger("hx_browser.profile_ops")


class OpError(Exception):
    """给用户看的业务错误 (路由层转成 code=-1)。"""


def repair_store(store) -> RepairStore:
    return RepairStore(store.data_dir / "repairs.json")


def backups_root(store) -> Path:
    return store.data_dir / "repair-backups"


def matches(env, search: str = "", tag: str = "") -> bool:
    """搜索覆盖名称 / id / 备注 / 标签; tag 精确匹配 (逗号分隔 = 任一命中)。"""
    if tag:
        wanted = {t.strip() for t in tag.split(",") if t.strip()}
        if wanted and not wanted.intersection(env.tags or []):
            return False
    if search:
        q = search.lower()
        hay = [
            env.name.lower(),
            env.id.lower(),
            (env.notes or "").lower(),
            *[t.lower() for t in env.tags or []],
        ]
        return any(q in h for h in hay)
    return True


def tag_counts(envs) -> dict[str, int]:
    out: dict[str, int] = {}
    for e in envs:
        for t in e.tags or []:
            out[t] = out.get(t, 0) + 1
    return dict(sorted(out.items()))


def apply_extras(env, payload: dict) -> list[str]:
    """create/update 共用的补充处理: 代理链接、内核与旧 browser 字段同步、偏好校验。"""
    errors: list[str] = []
    proxy = payload.get("proxy") if isinstance(payload, dict) else None
    if isinstance(proxy, dict) and proxy.get(LINK_KEY):
        try:
            parsed = parse_link(str(proxy[LINK_KEY]))
            env.proxy = ProxyConfig(**{**env.proxy.model_dump(), **parsed})
        except ProxyLinkError as e:
            errors.append(str(e))
    try:
        validate_config(env.proxy.model_dump())
    except ProxyLinkError as e:
        errors.append(str(e))
    if env.fingerprint.kernel:
        if kernels.get(env.fingerprint.kernel) is None:
            errors.append("未知内核: %s" % env.fingerprint.kernel)
        else:
            env.fingerprint.kernel = env.fingerprint.kernel.lower()
            env.fingerprint.browser = kernels.legacy_browser_for(env.fingerprint.kernel)  # type: ignore[assignment]
    errors.extend(validate_prefs(env))
    return errors


def _profile_dir(store, env_id: str) -> Path:
    root = store.profiles_dir.resolve()
    target = (store.profiles_dir / env_id).resolve()
    # env_id 来自 URL: 防 "../" 把彻底删除引到 profiles 目录之外
    if target == root or root not in target.parents:
        raise OpError("删除目标超出副本目录, 已拒绝")
    return store.profiles_dir / env_id


async def soft_delete(store, manager, env_id: str) -> dict:
    env = store.get_env(env_id)
    if env is None:
        raise OpError("环境不存在")
    await hxcp_link.astop(env_id)
    if get_runtime(env_id):
        await manager.stop(env)
    env.deleted_at = now_ms()
    store.update_env(env)
    store.add_log("env.trash", {"id": env_id})
    return {"id": env_id, "deleted_at": env.deleted_at}


def restore(store, env_id: str) -> dict:
    env = store.get_env(env_id)
    if env is None:
        raise OpError("环境不存在")
    if not env.deleted_at:
        raise OpError("该副本不在最近删除中")
    env.deleted_at = 0
    store.update_env(env)
    store.add_log("env.restore", {"id": env_id})
    return {"id": env_id}


async def purge(store, manager, env_id: str, require_trashed: bool = True) -> dict:
    env = store.get_env(env_id)
    if env is None:
        raise OpError("环境不存在")
    if require_trashed and not env.deleted_at:
        raise OpError("请先将副本移到最近删除, 再彻底删除")
    target = _profile_dir(store, env_id)
    await hxcp_link.astop(env_id)
    if get_runtime(env_id):
        await manager.stop(env)
    hxcp_link.drop_cache(env_id)
    await relay.stop(env_id)
    removed_repairs = repair_store(store).drop_for_env(env_id)
    shutil.rmtree(backups_root(store) / env_id, ignore_errors=True)
    if target.is_symlink():
        target.unlink()  # mode=reference 导入的 userdir: 只断开引用, 不删别人的原目录
    else:
        shutil.rmtree(target, ignore_errors=True)
    store.delete_env(
        env_id
    )  # 记录里的代理密码 / 账号密码 / 2FA / Cookie 随记录一起消失
    store.add_log("env.purge", {"id": env_id, "repairs": removed_repairs})
    return {"id": env_id, "purged": True, "profile_removed": not target.exists()}


async def delete(store, manager, env_id: str, purge_now: bool = False) -> dict:
    if purge_now:
        return await purge(store, manager, env_id, require_trashed=False)
    return await soft_delete(store, manager, env_id)


async def resume_relays(store) -> int:
    """服务重启后接管了在跑副本 (runtime.json), 但 relay 活在旧进程里已经没了 ——
    浏览器还连着原端口, 不重建它的所有请求都会失败。在**原端口**重建, 端口被占则记日志。
    """
    n = 0
    for env_id, rt in list(RUNTIME.items()):
        env = store.get_env(env_id) if rt.relay_port else None
        if env is not None and rt.isolation == "sandbox":
            n += await _resume_sandbox(env_id, env, rt)
            continue
        up = upstream_of(env.proxy) if env is not None else None
        if up is None:
            continue
        try:
            await relay.ensure(env_id, up, rt.relay_port)
            n += 1
        except OSError as e:
            log.warning(
                "env=%s relay port %d 无法重建: %s (请重启该副本)",
                env_id,
                rt.relay_port,
                e,
            )
    return n


async def _resume_sandbox(env_id: str, env, rt) -> int:
    """沙箱副本: 浏览器在自己的网络命名空间里, 宿主侧只需重建 Unix relay 与 CDP 桥。"""
    rd = sandbox.run_dir(env_id)
    try:
        up = egress_upstream(env.proxy, dict(os.environ))
        await relay.ensure(env_id, up, unix_path=sandbox.host_proxy_sock(rd))
        await bridge.ensure(env_id, rt.debug_port, sandbox.host_cdp_sock(rd))
        return 1
    except OSError as e:
        log.warning("env=%s 沙箱 relay/CDP 桥无法重建: %s (请重启该副本)", env_id, e)
        return 0
