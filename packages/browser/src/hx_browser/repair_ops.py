"""修复与回滚: 按项或一键应用检测给出的修复, 修改前备份; 按记录恢复。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 备份两样东西

1. **偏好字段的旧值** (记录里的 before) —— 恢复时写回 env.prefs;
2. **浏览器配置文件的副本** (Default/Preferences、Local State、user.js 存在才备份) ——
   恢复时拷回 profile。只回滚字段不够: 某字段改回 inherit 时启动路径不再写该键,
   上次写进 Preferences 的值会留在文件里, 文件备份才能让回滚**完整**。

恢复只在副本未运行时进行 (运行中的浏览器退出时会把内存里的偏好写回文件, 覆盖恢复结果)。
字段在修复后又被人改过 (当前值 != after) 时跳过该字段并报告冲突, 不覆盖用户后来的改动。
"""

from __future__ import annotations

import shutil
from pathlib import Path

from .browser import get_runtime
from .chromium_prefs import LOCAL_STATE, PREFS_REL
from .firefox_prefs import USER_JS
from .profile_ops import OpError, backups_root, repair_store
from .profile_rules import validate_prefs
from .prefs import ProfilePrefs
from .repairs import new_id

BACKUP_FILES = (str(PREFS_REL), LOCAL_STATE, USER_JS)
SCOPE_PREFS = "prefs"


def plan_fix(report: dict, items: list[str] | None) -> list[tuple[str, dict]]:
    """检测报告 -> [(item_id, {字段: 新值})]。items 为空 = 一键修复全部可修复项。"""
    out = []
    for c in report.get("checks", []):
        if c.get("fixable") and c.get("fix") and (not items or c["id"] in items):
            out.append((c["id"], dict(c["fix"])))
    return out


def _backup_files(profile_dir: Path, dest: Path) -> list[str]:
    saved = []
    for rel in BACKUP_FILES:
        src = profile_dir / rel
        if src.is_file():
            target = dest / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, target)
            saved.append(rel)
    return saved


def apply_fixes(store, env, report: dict, items: list[str] | None = None) -> dict:
    """应用修复。返回 {record, applied, pending_restart}。无可修复项时 record=None。"""
    plans = plan_fix(report, items)
    if not plans:
        return {"record": None, "applied": [], "pending_restart": False}
    before_prefs = env.prefs.model_dump()
    merged = dict(before_prefs)
    for _, fields in plans:
        merged.update(fields)
    candidate = env.model_copy(deep=True)
    candidate.prefs = ProfilePrefs(**merged)
    errors = validate_prefs(candidate)
    if errors:
        raise OpError("修复后的设置不合法, 未做任何修改: " + "；".join(errors))
    changes = [
        {"scope": SCOPE_PREFS, "key": k, "before": before_prefs.get(k), "after": v}
        for k, v in merged.items()
        if before_prefs.get(k) != v
    ]
    rid = new_id()
    saved = _backup_files(
        store.profiles_dir / env.id, backups_root(store) / env.id / rid
    )  # 先备份, 再改
    record = repair_store(store).add(
        env.id, ",".join(i for i, _ in plans), changes, rid, saved
    )
    env.prefs = candidate.prefs
    store.update_env(env)
    store.add_log(
        "env.repair",
        {"id": env.id, "record": record["id"], "items": [i for i, _ in plans]},
    )
    return {
        "record": record,
        "applied": [i for i, _ in plans],
        "pending_restart": bool(get_runtime(env.id)),
    }


def restore(store, record_id: str) -> dict:
    rs = repair_store(store)
    rec = rs.get(record_id)
    if rec is None:
        raise OpError("修复记录不存在")
    if rec.get("restored"):
        raise OpError("该记录已恢复过")
    env = store.get_env(rec["env_id"])
    if env is None:
        raise OpError("记录对应的副本已不存在")
    if get_runtime(env.id):
        raise OpError("请先关闭该副本再恢复 (运行中的浏览器退出时会覆盖恢复结果)")
    current = env.prefs.model_dump()
    restored, conflicts = [], []
    for ch in rec.get("changes", []):
        if ch.get("scope") != SCOPE_PREFS:
            continue
        key = ch["key"]
        if current.get(key) != ch.get("after"):
            conflicts.append(key)
            continue
        current[key] = ch.get("before")
        restored.append(key)
    env.prefs = ProfilePrefs(**current)
    store.update_env(env)
    src = backups_root(store) / env.id / record_id
    files = []
    for rel in BACKUP_FILES:
        if (src / rel).is_file():
            target = store.profiles_dir / env.id / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src / rel, target)
            files.append(rel)
    rs.mark_restored(record_id)
    store.add_log("env.repair-restore", {"id": env.id, "record": record_id})
    return {
        "record_id": record_id,
        "restored": restored,
        "conflicts": conflicts,
        "files": files,
    }
