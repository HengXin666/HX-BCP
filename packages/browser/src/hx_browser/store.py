"""JSON 持久化（环境/代理/扩展/模板/日志），对应 PRD §6 持久化。"""

from __future__ import annotations

import json
import os
import threading
from pathlib import Path

from .models import Environment, Extension, Proxy, Template, now_ms

_lock = threading.Lock()


def _atomic_write(path: Path, data: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(".tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2), "utf-8")
    os.chmod(tmp, 0o600)
    tmp.replace(path)


class Store:
    def __init__(self, data_dir: Path):
        self.data_dir = data_dir
        self.profiles_dir = data_dir / "profiles"
        self.envs_path = data_dir / "environments.json"
        self.proxies_path = data_dir / "proxies.json"
        self.templates_path = data_dir / "templates.json"
        self.extensions_path = data_dir / "extensions.json"
        self.logs_path = data_dir / "logs.json"
        self.groups_path = data_dir / "groups.json"
        self.data_dir.mkdir(parents=True, exist_ok=True)
        self.profiles_dir.mkdir(parents=True, exist_ok=True)

    # ---------- 环境 ----------
    def load_envs(self) -> list[Environment]:
        with _lock:
            if not self.envs_path.exists():
                return []
            raw = json.loads(self.envs_path.read_text("utf-8"))
            return [Environment(**e) for e in raw]

    def save_envs(self, envs: list[Environment]) -> None:
        with _lock:
            _atomic_write(self.envs_path, [e.model_dump(exclude={"runtime"}) for e in envs])

    def add_env(self, env: Environment) -> Environment:
        env.serial_no = len(self.load_envs()) + 1
        env.created_at = now_ms()
        env.updated_at = now_ms()
        envs = self.load_envs()
        envs.insert(0, env)
        self.save_envs(envs)
        return env

    def update_env(self, env: Environment) -> Environment:
        env.updated_at = now_ms()
        envs = self.load_envs()
        for i, e in enumerate(envs):
            if e.id == env.id:
                envs[i] = env
                break
        self.save_envs(envs)
        return env

    def get_env(self, env_id: str) -> Environment | None:
        for e in self.load_envs():
            if e.id == env_id:
                return e
        return None

    def delete_env(self, env_id: str) -> bool:
        envs = self.load_envs()
        remain = [e for e in envs if e.id != env_id]
        if len(remain) == len(envs):
            return False
        self.save_envs(remain)
        return True

    # ---------- 代理 ----------
    def load_proxies(self) -> list[Proxy]:
        if not self.proxies_path.exists():
            return []
        return [Proxy(**p) for p in json.loads(self.proxies_path.read_text("utf-8"))]

    def save_proxies(self, proxies: list[Proxy]) -> None:
        _atomic_write(self.proxies_path, [p.model_dump() for p in proxies])

    # ---------- 模板 ----------
    def load_templates(self) -> list[Template]:
        if not self.templates_path.exists():
            return []
        return [Template(**t) for t in json.loads(self.templates_path.read_text("utf-8"))]

    def save_templates(self, templates: list[Template]) -> None:
        _atomic_write(self.templates_path, [t.model_dump() for t in templates])

    # ---------- 扩展 ----------
    def load_extensions(self) -> list[Extension]:
        if not self.extensions_path.exists():
            return []
        return [Extension(**e) for e in json.loads(self.extensions_path.read_text("utf-8"))]

    def save_extensions(self, extensions: list[Extension]) -> None:
        _atomic_write(self.extensions_path, [e.model_dump() for e in extensions])

    # ---------- 分组 ----------
    def load_groups(self) -> list[str]:
        if not self.groups_path.exists():
            return ["默认"]
        return json.loads(self.groups_path.read_text("utf-8"))

    def save_groups(self, groups: list[str]) -> None:
        _atomic_write(self.groups_path, groups)

    # ---------- 日志 ----------
    def add_log(self, action: str, detail: dict | None = None) -> None:
        logs = []
        if self.logs_path.exists():
            try:
                logs = json.loads(self.logs_path.read_text("utf-8"))
            except Exception:
                logs = []
        logs.insert(0, {"at": now_ms(), "action": action, "detail": detail or {}})
        _atomic_write(self.logs_path, logs[:500])

    def load_logs(self, limit: int = 100) -> list[dict]:
        if not self.logs_path.exists():
            return []
        return json.loads(self.logs_path.read_text("utf-8"))[:limit]


def build_store(data_dir: Path | None = None) -> Store:
    from .config import settings_manager

    return Store(data_dir or Path(settings_manager.load().data_dir))


store_singleton: Store | None = None


def get_store() -> Store:
    global store_singleton
    if store_singleton is None:
        store_singleton = build_store()
    return store_singleton
