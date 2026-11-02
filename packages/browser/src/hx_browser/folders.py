"""Folders inside one lane group, stored in data/folders.json as {group_key: [folder names]}.

Agent Note: .agents/notes/implemented/feature/2026-11-01-lane-folders.md
"""

from __future__ import annotations

import json
import threading

import lane

from .store import _atomic_write, get_store

FILE_NAME = "folders.json"
MAX_LEN = 40
_lock = threading.Lock()


class FolderError(Exception):
    """Folder operation refused, status is the HTTP status to answer with."""

    def __init__(self, message: str, status: int = 400):
        super().__init__(message)
        self.status = status


def normalize_name(raw: str) -> str:
    """Folder name as stored, empty string when invalid (blank, too long or containing a slash)."""
    name = " ".join((raw or "").split())
    if not name or len(name) > MAX_LEN or "/" in name or "\\" in name:
        return ""
    return name


def _path():
    return get_store().data_dir / FILE_NAME


def _load() -> dict[str, list[str]]:
    p = _path()
    if not p.exists():
        return {}
    try:
        data = json.loads(p.read_text("utf-8"))
    except ValueError:
        return {}
    return {str(k): [str(x) for x in v] for k, v in data.items() if isinstance(v, list)}


def _env_key(env) -> str:
    return lane.group_key(env.lane, env.project)


def _live_envs():
    return [e for e in get_store().load_envs() if not e.deleted_at]


def snapshot() -> dict[str, list[str]]:
    """Every group with its folders, stored ones plus folders still referenced by environments."""
    data = _load()
    for env in _live_envs():
        names = data.setdefault(_env_key(env), [])
        if env.folder and env.folder not in names:
            names.append(env.folder)
    return {k: sorted(v) for k, v in data.items()}


def ensure(key: str, raw: str) -> str:
    """Make sure folder raw exists in group key and return its stored name."""
    name = normalize_name(raw)
    if not name:
        raise FolderError("invalid folder name, 1..40 chars, no slash")
    with _lock:
        data = _load()
        names = data.setdefault(key, [])
        if name not in names:
            names.append(name)
            _atomic_write(_path(), data)
    return name


def create(key: str, raw: str) -> str:
    """Create folder raw in group key, refuse a duplicate with 409."""
    name = normalize_name(raw)
    if not name:
        raise FolderError("invalid folder name, 1..40 chars, no slash")
    if name in snapshot().get(key, []):
        raise FolderError("folder already exists", 409)
    return ensure(key, name)


def rename(key: str, old: str, raw: str) -> str:
    """Rename folder old to raw inside group key and move its environments along."""
    new = normalize_name(raw)
    if not new:
        raise FolderError("invalid folder name, 1..40 chars, no slash")
    names = snapshot().get(key, [])
    if old not in names:
        raise FolderError("folder not found", 404)
    if new != old and new in names:
        raise FolderError("folder already exists", 409)
    s = get_store()
    for env in s.load_envs():
        if _env_key(env) == key and env.folder == old:
            env.folder = new
            s.update_env(env)
    with _lock:
        data = _load()
        data[key] = sorted({*(n for n in data.get(key, []) if n != old), new})
        _atomic_write(_path(), data)
    return new


def delete(key: str, name: str) -> None:
    """Delete an empty folder from group key, refuse with 409 while environments are inside."""
    if name not in snapshot().get(key, []):
        raise FolderError("folder not found", 404)
    if any(_env_key(e) == key and e.folder == name for e in _live_envs()):
        raise FolderError("folder is not empty, move or delete its browsers first", 409)
    with _lock:
        data = _load()
        data[key] = [n for n in data.get(key, []) if n != name]
        _atomic_write(_path(), data)
