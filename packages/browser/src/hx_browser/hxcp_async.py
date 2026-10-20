"""阻塞的 hxcp 进程操作放到线程里，避免卡住 API 事件循环。"""

from __future__ import annotations

import asyncio

from .hxcp_link import mount_if_enabled, start, stop, stop_all


async def astart(env_id: str, cdp_http: str, browser_pid: int = 0) -> tuple[int, str]:
    return await asyncio.to_thread(start, env_id, cdp_http, browser_pid)


async def astop(env_id: str) -> str:
    return await asyncio.to_thread(stop, env_id)


async def astop_all() -> None:
    await asyncio.to_thread(stop_all)


async def amount(env, info) -> None:
    await asyncio.to_thread(mount_if_enabled, env, info)
