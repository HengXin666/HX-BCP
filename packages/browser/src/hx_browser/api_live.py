"""实时预览的路由: 列在跑副本 + 每副本一条 WebSocket (帧下行 / 输入上行)。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md

为什么不写进 api.py: api.py 是行数棘轮登记的存量 (只许降不许涨)。本文件只做
「HTTP/WS 形状 <-> LiveSession」的转换, 帧流与输入换算在 live_view / live_protocol。

## 路径

    GET /api/v1/live/envs                      在跑副本的预览视图 (顶层固定段, 不挂在
                                               /environments/ 下, 免得被 {env_id} 抢匹配)
    WS  /api/v1/environments/{env_id}/live     预览会话 (?preset=grid|focus)

## 鉴权与既有中间件的关系

api.py 的 api_key 中间件是 `@app.middleware("http")`, Starlette 的 http 中间件**不经过**
websocket scope —— 不在这里补一次, 设了 api_key 的服务会对 WS 完全不设防。所以本模块
自己按同一口径校验 (header `x-api-key`, 或浏览器 WebSocket 无法设头时用 `?key=`)。

## Origin 校验 (跨站 WebSocket 劫持)

浏览器的 WebSocket 不受同源策略限制: 任意网页都能连 `ws://127.0.0.1:8325/...`, 默认
api_key 为空时它就能看画面、发输入。所以带 `Origin` 头的握手 (只有浏览器会带) 必须来自
可信来源: 回环地址 (本机的控制台 / 门户), 与本服务同 host, 或 `HXBR_LIVE_ORIGINS`
(逗号分隔) 显式列出的来源。不带 `Origin` 的是脚本类客户端, 仍按 api_key 判定。
"""

from __future__ import annotations

import asyncio
import contextlib
import os
from urllib.parse import urlsplit

from fastapi import APIRouter, WebSocket, WebSocketDisconnect
from fastapi.responses import JSONResponse

from . import live_protocol as lp
from . import live_view as lv
from .browser import get_runtime
from .config import settings_manager
from .envview import env_dict
from .store import get_store

# WebSocket 关闭码 (RFC 6455 应用私有段 4000-4999): 前端据此区分"别重连"与"可重试"
WS_CLOSE_UNSUPPORTED = 4400
WS_CLOSE_AUTH = 4401
WS_CLOSE_NOT_FOUND = 4404
WS_CLOSE_UPSTREAM = 4502
AUTH_REFUSED = "invalid api key or untrusted origin"


def _live_view(env) -> dict:
    """一个副本的预览视图: 平铺墙要的字段, 判据仍全部来自 env_dict / RuntimeInfo。"""
    d = env_dict(env)
    rt = d.get("runtime") or {}
    reason = lv.unsupported_reason(env)
    return {
        "id": env.id,
        "name": env.name,
        "group": env.group,
        "kernel": rt.get("kernel", "") or env.fingerprint.kernel or "",
        "kernel_family": d["kernel_family"],
        "isolation": rt.get("isolation", ""),
        "window_system": env.prefs.window_system,
        "display": d["display"],
        "display_source": d["display_source"],
        "visible": d["visible"],
        "running": bool(rt),
        "supported": reason == "",
        "unsupported_reason": reason,
    }


ORIGINS_ENV = "HXBR_LIVE_ORIGINS"
_LOOPBACK_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})


def origin_allowed(origin: str, host: str, extra: str = "") -> bool:
    """握手来源是否可信 (判据只此一处, 见模块头)。空 origin = 非浏览器客户端。"""
    if origin == "":
        return True
    allowed = {o.strip().rstrip("/") for o in extra.split(",") if o.strip()}
    if origin.rstrip("/") in allowed:
        return True
    parts = urlsplit(origin)
    return parts.hostname in _LOOPBACK_HOSTS or (host != "" and parts.netloc == host)


def _authorized(ws: WebSocket) -> bool:
    origin = ws.headers.get("origin", "")
    if not origin_allowed(
        origin, ws.headers.get("host", ""), os.environ.get(ORIGINS_ENV, "")
    ):
        return False
    key = settings_manager.load().api_key
    if not key:
        return True
    given = ws.headers.get("x-api-key", "") or ws.query_params.get("key", "")
    return given == key


async def _refuse(ws: WebSocket, message: str, code: int) -> None:
    """先发一条 fatal error 再关: 关闭原因字段有 123 字节上限, 中文原因放不下。"""
    await ws.accept()
    with contextlib.suppress(Exception):
        await ws.send_json(lp.error_event(message, fatal=True))
    await ws.close(code=code)


def build_router() -> APIRouter:
    r = APIRouter(prefix="/api/v1")

    @r.get("/live/envs")
    async def live_envs():
        envs = [e for e in get_store().load_envs() if not e.deleted_at]
        views = [_live_view(e) for e in envs if get_runtime(e.id) is not None]
        return JSONResponse({"code": 0, "msg": "success", "data": {"envs": views}})

    @r.websocket("/environments/{env_id}/live")
    async def live_ws(ws: WebSocket, env_id: str):
        if not _authorized(ws):
            await _refuse(ws, AUTH_REFUSED, WS_CLOSE_AUTH)
            return
        env = get_store().get_env(env_id)
        if env is None or env.deleted_at:
            await _refuse(ws, "环境不存在", WS_CLOSE_NOT_FOUND)
            return
        reason = lv.unsupported_reason(env)
        if reason:
            await _refuse(ws, reason, WS_CLOSE_UNSUPPORTED)
            return
        rt = get_runtime(env_id)
        if rt is None or not rt.debug_port:
            await _refuse(ws, lv.NOT_RUNNING, WS_CLOSE_NOT_FOUND)
            return
        await ws.accept()
        preset = ws.query_params.get("preset", lv.PRESET_FOCUS)

        async def close_upstream() -> None:
            with contextlib.suppress(Exception):
                await ws.close(code=WS_CLOSE_UPSTREAM)

        session = lv.LiveSession(rt.debug_port, ws.send_json, preset, close_upstream)
        try:
            await session.start()
        except asyncio.CancelledError:
            await session.close()
            raise
        except Exception as e:  # noqa: BLE001 — CDP/网络/握手失败都要显式报错并清理
            with contextlib.suppress(Exception):
                await ws.send_json(
                    lp.error_event(f"连接副本 CDP 失败: {e}", fatal=True)
                )
            await close_upstream()
            await session.close()
            return
        try:
            await lv.pump(session, ws.receive_text)
        except (WebSocketDisconnect, RuntimeError):
            # RuntimeError: 上游断开时 on_fatal 已关掉 WS, 再 receive 会报 "not connected"
            pass
        finally:
            await session.close()

    return r
