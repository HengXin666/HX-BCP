"""CDP 客户端（websockets 实现, 不依赖 playwright）。"""

from __future__ import annotations

import asyncio
import json
import logging
import urllib.request
from collections.abc import Callable, Coroutine
from typing import Any

import websockets

log = logging.getLogger("hx_browser.cdp")
_LOCAL_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


class CdpError(Exception):
    pass


class ConnectionClosed(CdpError):
    """CDP 连接已断 (target 关闭 / 崩溃 / 浏览器退出), 区别于单条命令失败。"""


class CdpConnection:
    """一个 CDP target 连接。"""

    def __init__(self, ws_url: str):
        self.ws_url = ws_url
        self._ws: websockets.ClientConnection | None = None
        self._seq = 0
        self._pending: dict[int, asyncio.Future] = {}
        self._closed = False
        # CDP 事件 (无 id 的消息) 回调; 默认 None = 丢弃, 与改动前行为一致。
        # 以 create_task 调度而非在读循环里 await: 回调里常要再 send (如 screencast ack),
        # 在读循环里等自己的响应会死锁。
        self.on_event: Callable[[dict], Coroutine[Any, Any, None]] | None = None
        # 连接被**对端**断开时回调一次 (自己 close() 不触发); 默认 None 与改动前一致。
        self.on_close: Callable[[], Coroutine[Any, Any, None]] | None = None
        self._event_tasks: set[asyncio.Task[None]] = set()

    async def connect(self, timeout: float = 8.0) -> "CdpConnection":
        self._ws = await asyncio.wait_for(websockets.connect(self.ws_url, max_size=2**26, open_timeout=timeout, proxy=None), timeout=timeout + 2)
        self._task = asyncio.create_task(self._reader())
        return self

    async def _reader(self) -> None:
        try:
            async for raw in self._ws:
                msg = json.loads(raw)
                if "id" in msg and msg["id"] in self._pending:
                    fut = self._pending.pop(msg["id"])
                    if not fut.done():
                        fut.set_result(msg)
                elif "method" in msg and self.on_event is not None:
                    t: asyncio.Task[None] = asyncio.create_task(self.on_event(msg))
                    self._event_tasks.add(t)  # 持引用, 否则任务可能在跑完前被回收
                    t.add_done_callback(self._event_tasks.discard)
        except Exception as e:  # noqa: BLE001
            reason = str(e)
        else:
            reason = "peer closed"
        # 正常结束 (对端关 ws) 也要让等待中的命令立刻失败, 否则它们只能等到各自超时。
        if not self._closed:
            for fut in self._pending.values():
                if not fut.done():
                    fut.set_exception(ConnectionClosed(f"cdp closed: {reason}"))
            self._pending.clear()
            if self.on_close is not None:
                try:
                    await self.on_close()
                except Exception as e:  # noqa: BLE001 — 回调失败不该让读循环任务带着异常结束
                    log.warning("cdp on_close 回调失败: %s", e)

    async def send(self, method: str, params: dict | None = None, timeout: float = 30.0) -> Any:
        if self._ws is None:
            raise CdpError("not connected")
        self._seq += 1
        msg_id = self._seq
        fut: asyncio.Future = asyncio.get_running_loop().create_future()
        self._pending[msg_id] = fut
        try:
            await self._ws.send(json.dumps({"id": msg_id, "method": method, "params": params or {}}))
        except websockets.ConnectionClosed as e:
            self._pending.pop(msg_id, None)
            raise ConnectionClosed(f"cdp closed: {e}") from e
        try:
            resp = await asyncio.wait_for(fut, timeout=timeout)
        except asyncio.TimeoutError:
            self._pending.pop(msg_id, None)
            raise CdpError(f"cdp timeout: {method}")
        if "error" in resp:
            raise CdpError(f"cdp error {method}: {resp['error']}")
        return resp.get("result", {})

    async def close(self) -> None:
        self._closed = True
        if self._ws:
            try:
                await self._ws.close()
            except Exception:
                pass
        if hasattr(self, "_task"):
            self._task.cancel()


async def http_json(url: str, timeout: float = 5.0) -> Any:
    """GET 一个 HTTP JSON（如 /json/version//json）。"""
    loop = asyncio.get_running_loop()

    def _get():
        # CDP 只在本机回环口: 显式不走环境 HTTP(S)_PROXY, 否则请求被送去代理而失败
        with _LOCAL_OPENER.open(url, timeout=timeout) as r:
            return json.loads(r.read().decode("utf-8"))

    return await loop.run_in_executor(None, _get)


def _cdp_bases(debug_port: int) -> list[str]:
    """Chrome 默认只绑 [::1]; 部分构建忽略 --remote-debugging-address, 两个都试。"""
    return [f"http://127.0.0.1:{debug_port}", f"http://[::1]:{debug_port}"]


async def _get_json_any(debug_port: int, path: str):
    last: Exception | None = None
    for base in _cdp_bases(debug_port):
        try:
            return await http_json(base + path)
        except Exception as e:  # noqa: BLE001
            last = e
    raise last if last else RuntimeError("cdp unreachable")


async def reachable_base(debug_port: int, timeout: float = 15.0) -> str:
    """真正能连上的 CDP http 地址; 超时返回空串。"""
    deadline = loop_now() + timeout
    while loop_now() < deadline:
        for base in _cdp_bases(debug_port):
            try:
                await http_json(base + "/json/version", 2)
                return base
            except Exception:  # noqa: BLE001
                pass
        await asyncio.sleep(0.3)
    return ""


async def get_browser_ws(debug_port: int, timeout: float = 8.0) -> str:
    """等待 CDP 端口就绪并返回 browser-level ws 地址。"""
    deadline = loop_now() + timeout
    while loop_now() < deadline:
        try:
            ver = await _get_json_any(debug_port, "/json/version")
            ws = ver.get("webSocketDebuggerUrl")
            if ws:
                return ws
        except Exception:
            pass
        await asyncio.sleep(0.3)
    raise CdpError(f"cdp not ready on port {debug_port}")


def loop_now() -> float:
    return asyncio.get_running_loop().time()


async def find_page_ws(debug_port: int, timeout: float = 15.0) -> str:
    """等待并返回第一个 page target 的 ws 地址（Cookie 注入需要 page 级）。"""
    deadline = loop_now() + timeout
    while loop_now() < deadline:
        try:
            targets = await _get_json_any(debug_port, "/json")
            for t in targets:
                if t.get("type") == "page" and t.get("webSocketDebuggerUrl"):
                    return t["webSocketDebuggerUrl"]
            # 没有 page 时主动开一个
            await _open_blank(debug_port)
        except Exception:
            pass
        await asyncio.sleep(0.4)
    raise CdpError(f"no page target on port {debug_port}")


async def _open_blank(debug_port: int) -> None:
    try:
        ws_url = await get_browser_ws(debug_port, timeout=3)
        conn = await CdpConnection(ws_url).connect(timeout=5)
        try:
            await conn.send("Target.createTarget", {"url": "about:blank"})
        finally:
            await conn.close()
    except Exception:
        pass


async def set_cookies(debug_port: int, cookies: list[dict], timeout: float = 20.0) -> int:
    """通过 page 级 CDP 注入 Cookie, 返回注入条数。"""
    page_ws = await find_page_ws(debug_port, timeout=timeout)
    conn = await CdpConnection(page_ws).connect(timeout=10)
    try:
        await conn.send("Network.enable")
        normalized = []
        for c in cookies:
            item = {
                "name": c.get("name", ""),
                "value": c.get("value", ""),
                "domain": c.get("domain", ""),
                "path": c.get("path", "/"),
                "secure": bool(c.get("secure", False)),
                "httpOnly": bool(c.get("httpOnly", False)),
            }
            if c.get("expires"):
                item["expires"] = float(c["expires"])
            if c.get("sameSite"):
                item["sameSite"] = c["sameSite"]
            if item["name"] and item["domain"]:
                normalized.append(item)
        if not normalized:
            return 0
        await conn.send("Network.setCookies", {"cookies": normalized})
        return len(normalized)
    finally:
        await conn.close()


async def get_cookies(debug_port: int, timeout: float = 20.0) -> list[dict]:
    """拉取当前环境全部 Cookie。"""
    page_ws = await find_page_ws(debug_port, timeout=timeout)
    conn = await CdpConnection(page_ws).connect(timeout=10)
    try:
        await conn.send("Network.enable")
        res = await conn.send("Network.getAllCookies")
        return res.get("cookies", [])
    finally:
        await conn.close()


async def evaluate(debug_port: int, expression: str, timeout: float = 20.0) -> Any:
    """在 page 上下文执行 JS 并返回结果。"""
    page_ws = await find_page_ws(debug_port, timeout=timeout)
    conn = await CdpConnection(page_ws).connect(timeout=10)
    try:
        res = await conn.send("Runtime.evaluate", {"expression": expression, "returnByValue": True})
        return res.get("result", {}).get("value")
    finally:
        await conn.close()


async def close_browser(debug_port: int, timeout: float = 10.0) -> bool:
    """通过 CDP Browser.close 优雅关闭。"""
    try:
        ws_url = await get_browser_ws(debug_port, timeout=4)
        conn = await CdpConnection(ws_url).connect(timeout=6)
        try:
            await conn.send("Browser.close")
        finally:
            await conn.close()
        return True
    except Exception:
        return False
