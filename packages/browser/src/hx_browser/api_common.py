"""Shared helpers for the api_* routers: browser manager, store, response envelope.

Agent Note: .agents/notes/implemented/architecture/2026-11-01-api-split.md
"""

from __future__ import annotations

from fastapi.responses import JSONResponse

from .browser import BrowserManager
from .config import settings_manager
from .store import get_store

_manager: BrowserManager | None = None


def manager() -> BrowserManager:
    global _manager
    s = settings_manager.load()
    _manager = _manager or BrowserManager(s, settings_manager.profiles_dir())
    _manager.settings = s  # 每次取用都刷新: PUT /settings 只写盘, 缓存快照会"设了没用"
    return _manager


def store():
    return get_store()


def ok(data=None, msg: str = "success") -> JSONResponse:
    return JSONResponse({"code": 0, "msg": msg, "data": data})


def fail(msg: str, code: int = -1, status: int = 200) -> JSONResponse:
    return JSONResponse({"code": code, "msg": msg, "data": None}, status_code=status)
