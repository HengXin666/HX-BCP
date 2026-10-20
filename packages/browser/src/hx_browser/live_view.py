"""实时预览会话: 一个副本的 CDP page target <-> 一条客户端 WebSocket。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md

## 为什么是 CDP screencast 而不是截屏

#98 之后副本的显示层可能是 Xvfb 专属 socket、Xephyr 嵌套、或**原生 Wayland**
(窗口无法被外部定位, `focus` 接口如实返回失败)。凡是"抓 X11 屏幕"的做法在这三档里
至少废两档。`Page.startScreencast` 的帧由**浏览器自己**产出, 走既有 CDP 通道 ——
沙箱副本经宿主 bridge (127.0.0.1:<cdp> -> cdp.sock) 到达, 端口与用法都不变。
所以本模块对 process / sandbox / headless / Wayland 一视同仁, 没有任何显示层特例。

## 帧必须 ack

Chromium 按**未确认帧数**限流: 不回 `Page.screencastFrameAck` 时它推几帧就停。
症状是"画面卡住但页面其实在动"。ack 在收到帧时立刻发, 不等客户端渲染完
(客户端慢时宁可丢画质也不要停流 —— 停流看起来像后端挂了)。

## Firefox 不支持, 如实报错

Firefox 没有 CDP 的 `Page.startScreencast` (其远程协议是 WebDriver BiDi, 截屏能力与
此不同构)。本模块**不做**"降级成定时整页截图"的假装: 那会给出一个每秒一帧、点不动
的"实时预览", 用户分不清是网卡还是功能本就不存在。gecko 族一律显式报错。
"""

from __future__ import annotations

import asyncio
import contextlib
import functools
import json
import logging
from collections.abc import Awaitable, Callable
from typing import Any

from . import cdp, kernels
from . import live_nav as nav
from . import live_protocol as lp
from .live_frames import FrameRelay

log = logging.getLogger("hx_browser.live_view")

# 画质档: 栅格平铺要省带宽, 聚焦单屏要清楚。两档的真实参数只在这里定义。
GRID_QUALITY = {
    "format": "jpeg",
    "quality": 40,
    "maxWidth": 640,
    "maxHeight": 400,
    "everyNthFrame": 2,
}
FOCUS_QUALITY = {
    "format": "jpeg",
    "quality": 70,
    "maxWidth": 1600,
    "maxHeight": 1000,
    "everyNthFrame": 1,
}
QUALITY_PRESETS = {"grid": GRID_QUALITY, "focus": FOCUS_QUALITY}
PRESET_FOCUS = "focus"

GECKO_UNSUPPORTED = (
    "该副本是 Firefox 系内核 (gecko): 它没有 CDP Page.startScreencast, "
    "实时预览与远程操作暂不支持。请用 Chromium 系内核 (cloak/chrome/chromium/edge/brave) 的副本。"
)
NOT_RUNNING = "副本未运行: 没有可预览的页面 (先启动副本)"

_META_POLL_S = 2.0
_META_FAIL_MAX = 3
_ACK = "Page.screencastFrameAck"
_CMD_TIMEOUT_S = 8  # 单条输入/求值类 CDP 命令的超时
_PAGE_ENABLE = ("Page.enable", "Runtime.enable")
_STOP_CAST = "Page.stopScreencast"
_META_JS = "({u:location.href,t:document.title})"


def unsupported_reason(env) -> str:
    """这个副本能不能做实时预览; 空串 = 能。判据只有这一处。"""
    if kernels.family_of(env) == kernels.GECKO:
        return GECKO_UNSUPPORTED
    return ""


class LiveSession:
    """一个副本的预览会话。send 由调用方注入 (WebSocket 的 send_json)。"""

    def __init__(
        self,
        debug_port: int,
        send: Callable[[dict], Awaitable[None]],
        preset: str = PRESET_FOCUS,
        on_fatal: Callable[[], Awaitable[None]] | None = None,
    ):
        self.debug_port = debug_port
        self.send = send
        # 上游 page 连接断开时调用 (api_live 注入: 以 4502 关 WS, 让客户端退避重连)
        self.on_fatal = on_fatal
        self.conn: cdp.CdpConnection | None = None
        self.quality = QUALITY_PRESETS.get(preset, FOCUS_QUALITY)
        self.page_w = 0.0
        self.page_h = 0.0
        self.client_w = 0.0
        self.streaming = False
        self._meta_task: asyncio.Task | None = None
        self.frames = FrameRelay(send)  # 客户端发不动时只保留最新一帧 (见 live_frames)
        self._fatal_sent = False

    # ---------- 生命周期 ----------

    async def start(self) -> None:
        page_ws = await cdp.find_page_ws(self.debug_port, timeout=10)
        await self._attach(page_ws)
        self._meta_task = asyncio.create_task(self._meta_loop())

    async def _attach(self, page_ws: str) -> None:
        old = self.conn
        conn = await cdp.CdpConnection(page_ws).connect(timeout=10)
        # 回调绑定**来源连接**: 切标签后旧连接里排队的帧既不能 ack 到新 target, 也不能发给客户端
        conn.on_event = functools.partial(self._on_event, conn)
        conn.on_close = functools.partial(self._upstream_closed, conn)
        self.conn = conn
        if old is not None:
            await old.close()
        for method in _PAGE_ENABLE:
            await conn.send(method)
        # 切标签时保持暂停态: 被暂停的瓦片换了标签也不该偷偷开始推帧
        resume = old is None or self.streaming
        self.streaming = False
        if resume:
            await self._start_cast()

    async def close(self) -> None:
        if self._meta_task is not None:
            self._meta_task.cancel()
            self._meta_task = None
        self.frames.cancel()
        if self.conn is not None:
            with contextlib.suppress(Exception):
                await self.conn.send(_STOP_CAST, timeout=3)
            await self.conn.close()
            self.conn = None

    # ---------- 下行: 帧与元数据 ----------

    async def _start_cast(self) -> None:
        await self._require_conn().send("Page.startScreencast", dict(self.quality))
        self.streaming = True

    def _require_conn(self) -> cdp.CdpConnection:
        if self.conn is None:
            raise cdp.CdpError("预览会话未连接")
        return self.conn

    async def _on_event(self, source: cdp.CdpConnection, msg: dict) -> None:
        """CDP 事件回调 (由 CdpConnection 的读循环逐条调度)。"""
        if source is not self.conn or msg.get("method") != "Page.screencastFrame":
            return
        params = msg.get("params", {})
        meta = params.get("metadata", {})
        session_id = int(params.get("sessionId", 0))
        # 先 ack 再转发, 且 ack 回到**来源连接**: ack 丢了 Chromium 会停推 (见模块头)。
        with contextlib.suppress(Exception):
            await source.send(_ACK, {"sessionId": session_id}, timeout=5)
        if source is not self.conn:
            return
        self.page_w = float(meta.get("deviceWidth", self.page_w))
        self.page_h = float(meta.get("deviceHeight", self.page_h))
        self.frames.push(lp.frame_event(params.get("data", ""), meta, session_id))

    async def _upstream_closed(self, source: cdp.CdpConnection) -> None:
        """当前 page 连接被对端断开 (标签被关 / 崩溃 / 副本停了): 显式报错并交给 on_fatal。"""
        if source is self.conn:
            await self.fatal("副本页面连接已断开 (标签被关闭或浏览器已退出)")

    async def fatal(self, message: str) -> None:
        """只报一次; 不报就会停在最后一帧却仍显示「实时」。"""
        if self._fatal_sent:
            return
        self._fatal_sent = True
        with contextlib.suppress(Exception):
            await self.send(lp.error_event(message, fatal=True))
        if self.on_fatal is not None:
            await self.on_fatal()

    async def _meta_loop(self) -> None:
        """url / title 不随帧来, 轮询读。连续失败 _META_FAIL_MAX 次视为上游已断。"""
        failures = 0
        while True:
            try:
                await self.send({"type": lp.EVT_META, **await self.page_meta()})
                failures = 0
            except cdp.ConnectionClosed as e:
                await self.fatal(f"副本页面连接已断开: {e}")
                return
            except Exception as e:  # noqa: BLE001 — 单次失败 (超时 / 页面忙) 不算断
                failures += 1
                if failures >= _META_FAIL_MAX:
                    await self.fatal(f"连续 {failures} 次读不到页面信息: {e}")
                    return
            await asyncio.sleep(_META_POLL_S)

    async def page_meta(self) -> dict[str, Any]:
        res = await self._require_conn().send(
            "Runtime.evaluate",
            {"expression": _META_JS, "returnByValue": True},
            timeout=_CMD_TIMEOUT_S,
        )
        val = res.get("result", {}).get("value", {}) or {}
        return {
            "url": val.get("u", ""),
            "title": val.get("t", ""),
            "pageWidth": self.page_w,
            "pageHeight": self.page_h,
            "streaming": self.streaming,
        }

    # ---------- 上行: 客户端消息 ----------

    async def handle(self, msg: dict) -> None:
        """一条客户端消息。协议错误回一条 error 事件, **不**关连接。"""
        kind = str(msg.get("type", ""))
        try:
            await self._dispatch(kind, msg)
        except lp.ProtocolError as e:
            await self.send(lp.error_event(str(e)))
        except cdp.ConnectionClosed as e:
            await self.fatal(f"副本页面连接已断开: {e}")
        except cdp.CdpError as e:
            await self.send(lp.error_event(f"CDP 调用失败: {e}"))
        except (OSError, TimeoutError, ValueError, TypeError, KeyError) as e:
            # 兜底: 一条坏消息或一次临时故障只回 error 事件, 不拖垮整条预览连接
            await self.send(lp.error_event(f"处理消息失败: {e}"))

    async def _dispatch(self, kind: str, msg: dict) -> None:
        conn = self._require_conn()
        if kind == lp.MSG_MOUSE:
            self.client_w = lp.num(msg, "clientWidth", self.client_w)
            params = lp.mouse_params(msg, self.client_w, self.page_w, self.page_h)
            await conn.send(lp.CDP_MOUSE, params, timeout=_CMD_TIMEOUT_S)
        elif kind == lp.MSG_KEY:
            await conn.send(lp.CDP_KEY, lp.key_params(msg), timeout=_CMD_TIMEOUT_S)
        elif kind == lp.MSG_TEXT:
            await conn.send(lp.CDP_TEXT, lp.text_params(msg), timeout=_CMD_TIMEOUT_S)
        elif kind == lp.MSG_NAVIGATE:
            await nav.navigate(conn, msg)
        elif kind == lp.MSG_QUALITY:
            await self.set_quality(str(msg.get("preset", PRESET_FOCUS)))
        elif kind == lp.MSG_PAUSE:
            await self.pause()
        elif kind == lp.MSG_RESUME:
            await self.resume()
        elif kind == lp.MSG_TABS:
            await self.send({"type": lp.EVT_TABS, "tabs": await self.tabs()})
        elif kind == lp.MSG_ACTIVATE:
            await self.activate(str(msg.get("targetId", "")))
        else:
            raise lp.ProtocolError(f"未知消息类型: {kind or '(空)'}")

    async def set_quality(self, preset: str) -> None:
        self.quality = QUALITY_PRESETS.get(preset, FOCUS_QUALITY)
        if self.streaming:
            await self.pause()
            await self.resume()

    async def pause(self) -> None:
        """平铺页里看不见的瓦片应当停流 —— 否则 N 个副本同时推帧, 带宽全花在没人看的画面上。"""
        if not self.streaming:
            return
        await self._require_conn().send(_STOP_CAST, {}, timeout=5)
        self.streaming = False

    async def resume(self) -> None:
        if self.streaming:
            return
        await self._start_cast()

    async def tabs(self) -> list[dict]:
        return await nav.list_tabs(
            self.debug_port, self.conn.ws_url if self.conn else ""
        )

    async def activate(self, target_id: str) -> None:
        """切标签 = 换一条 page 连接并重开 screencast (screencast 是 per-target 的)。"""
        await self._attach(await nav.tab_ws(self.debug_port, target_id))


async def pump(session: LiveSession, recv: Callable[[], Awaitable[str]]) -> None:
    """客户端消息循环。收到非 JSON 就回一条 error, 不中断会话。"""
    while True:
        raw = await recv()
        try:
            msg = json.loads(raw)
        except (TypeError, ValueError):
            await session.send(lp.error_event("消息不是合法 JSON"))
            continue
        if not isinstance(msg, dict):
            await session.send(lp.error_event("消息必须是 JSON 对象"))
            continue
        await session.handle(msg)
