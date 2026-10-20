"""实时预览的帧下行: 只保留最新一帧的单任务发送器。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md

## 为什么要合并帧

CdpConnection 的读循环为每个 CDP 事件起一个任务。帧事件如果直接 `await ws.send`,
客户端网络慢时这些任务会排队, 每个都攥着一张 JPEG, 内存随时间线性涨。
预览只关心**最新**画面: 发送中又来了新帧就覆盖待发的那张, 旧帧直接丢。
Chromium 侧的限流靠 ack (在 live_view 里收到即回), 与这里的合并互不影响。
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import Awaitable, Callable


class FrameRelay:
    """`push` 不阻塞; 同一时刻最多一个发送任务, 待发队列长度恒 <= 1。"""

    def __init__(self, send: Callable[[dict], Awaitable[None]]):
        self._send = send
        self._pending: dict | None = None
        self._task: asyncio.Task | None = None
        self.dropped = 0

    def push(self, event: dict) -> None:
        if self._pending is not None:
            self.dropped += 1
        self._pending = event
        if self._task is None or self._task.done():
            self._task = asyncio.create_task(self._flush())

    async def _flush(self) -> None:
        while self._pending is not None:
            event, self._pending = self._pending, None
            with contextlib.suppress(Exception):
                await self._send(event)

    async def drain(self) -> None:
        """等当前发送任务结束 (测试与关闭前用)。"""
        if self._task is not None:
            with contextlib.suppress(asyncio.CancelledError):
                await self._task

    def cancel(self) -> None:
        if self._task is not None:
            self._task.cancel()
        self._task = None
        self._pending = None
