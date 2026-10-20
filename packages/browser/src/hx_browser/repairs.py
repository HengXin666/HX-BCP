"""修复记录: 每次「检测与修复」改动前备份旧值, 记成一条可回滚的记录。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

一条记录 = 一次修复动作对某个副本做的全部键值变更 (before/after)。
恢复 = 把 after 处的键改回 before。记录存 data/repairs.json, 与环境/代理同级。
"""

from __future__ import annotations

import json
import os
import secrets
import threading
import time
from pathlib import Path

_lock = threading.Lock()
MAX_RECORDS = 500


def new_id() -> str:
    return "rp_" + secrets.token_hex(5)


def _now() -> int:
    return int(time.time() * 1000)


class RepairStore:
    def __init__(self, path: Path):
        self.path = path

    def _load(self) -> list[dict]:
        if not self.path.exists():
            return []
        try:
            return json.loads(self.path.read_text("utf-8"))
        except (OSError, ValueError):
            return []

    def _save(self, records: list[dict]) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_suffix(".tmp")
        tmp.write_text(json.dumps(records, ensure_ascii=False, indent=2), "utf-8")
        os.chmod(tmp, 0o600)
        tmp.replace(self.path)

    def add(
        self,
        env_id: str,
        item: str,
        changes: list[dict],
        record_id: str = "",
        backup_files: list[str] | None = None,
    ) -> dict:
        """changes: [{"scope","key","before","after"}]。"""
        rec = {
            "id": record_id or new_id(),
            "env_id": env_id,
            "item": item,
            "backup_files": backup_files or [],
            "changes": changes,
            "created_at": _now(),
            "restored": False,
        }
        with _lock:
            records = self._load()
            records.insert(0, rec)
            self._save(records[:MAX_RECORDS])
        return rec

    def list(self, env_id: str = "") -> list[dict]:
        records = self._load()
        return [r for r in records if not env_id or r.get("env_id") == env_id]

    def get(self, record_id: str) -> dict | None:
        return next((r for r in self._load() if r.get("id") == record_id), None)

    def mark_restored(self, record_id: str) -> None:
        with _lock:
            records = self._load()
            for r in records:
                if r.get("id") == record_id:
                    r["restored"] = True
                    r["restored_at"] = _now()
            self._save(records)

    def drop_for_env(self, env_id: str) -> int:
        with _lock:
            records = self._load()
            keep = [r for r in records if r.get("env_id") != env_id]
            removed = len(records) - len(keep)
            self._save(keep)
        return removed
