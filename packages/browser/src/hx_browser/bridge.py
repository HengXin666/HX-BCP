"""宿主侧 CDP 桥: 127.0.0.1:<cdp_port> -> 沙箱内 shim 的 cdp.sock。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

沙箱有独立网络命名空间, 浏览器监听的 CDP 端口在宿主上不存在。桥在宿主同一端口
号上监听, 把每条连接转进 cdp.sock —— 于是 RuntimeInfo.cdp_http、MCP、既有 CDP
客户端全部**无需感知**沙箱 (端口号、地址、协议都不变)。

桥先于浏览器起: 浏览器还没就绪时连接被立即关闭, 既有的「轮询 /json/version 直到
就绪」逻辑自然重试, 不需要额外的同步。
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import os

log = logging.getLogger("hx_browser.bridge")

LOOPBACK = "127.0.0.1"
CHUNK = 65536


async def _pipe(src: asyncio.StreamReader, dst: asyncio.StreamWriter) -> None:
    with contextlib.suppress(Exception):
        while chunk := await src.read(CHUNK):
            dst.write(chunk)
            await dst.drain()
    with contextlib.suppress(Exception):
        dst.close()


async def private_unix_server(
    cb, path: str, limit: int = CHUNK
) -> asyncio.AbstractServer:
    """建一个只有本用户可连的 Unix socket 服务 (umask 077); 旧 socket 文件先删。"""
    old = os.umask(0o077)
    try:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(path)
        return await asyncio.start_unix_server(cb, path, limit=limit)
    finally:
        os.umask(old)


class Bridge:
    def __init__(self, port: int, sock_path: str):
        self.port = port
        self.sock_path = sock_path
        self.server: asyncio.AbstractServer | None = None

    async def _handle(self, reader, writer) -> None:
        try:
            u_reader, u_writer = await asyncio.open_unix_connection(self.sock_path)
        except OSError:
            writer.close()
            return
        await asyncio.gather(_pipe(reader, u_writer), _pipe(u_reader, writer))

    async def start(self) -> None:
        self.server = await asyncio.start_server(self._handle, LOOPBACK, self.port)

    async def close(self) -> None:
        if self.server is not None:
            self.server.close()
            with contextlib.suppress(Exception):
                await asyncio.wait_for(self.server.wait_closed(), 2)
            self.server = None


BRIDGES: dict[str, Bridge] = {}


async def ensure(env_id: str, port: int, sock_path: str) -> None:
    cur = BRIDGES.get(env_id)
    if cur and cur.server and cur.port == port and cur.sock_path == sock_path:
        return
    await stop(env_id)
    b = Bridge(port, sock_path)
    await b.start()
    BRIDGES[env_id] = b
    log.info("env=%s cdp bridge 127.0.0.1:%d -> %s", env_id, port, sock_path)


async def stop(env_id: str) -> None:
    b = BRIDGES.pop(env_id, None)
    if b is not None:
        await b.close()


async def stop_all() -> None:
    for env_id in list(BRIDGES):
        await stop(env_id)
