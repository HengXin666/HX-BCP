"""CDP 会话原语 — 单 reader, 响应按 id 配对, 事件按回调分发。

本模块是**唯一权威实现** (fp-audit 的测试脚手架从它导入), 因为两条实测教训:

1. **拦截流与请求流必须在同一条会话**。拆两条时, 拦截侧读超时会静默结束放行循环,
   页面导航永远等不到响应 → "CDP Page.navigate 超时"。
2. **在事件回调里调用 send() 必须自动降级为只发不等**。
   reader 线程在回调里等一个只有 reader 能 set 的响应 = 自己等自己, 永久死锁 ——
   第一版就死在这里。
3. **reader 的 recv 超时绝不退出循环**。静默退出等于整条链路假死。
"""
from __future__ import annotations

import base64
import json
import threading
import time
import urllib.request

import websocket  # type: ignore


class _Pending:
    __slots__ = ("resp", "ev")

    def __init__(self) -> None:
        self.resp: dict | None = None
        self.ev = threading.Event()


class Session:
    def __init__(self, endpoint: str, on_event=None, *, timeout: float = 120.0,
                 target_id: str | None = None) -> None:
        self.endpoint = endpoint.rstrip("/")
        ws_url = self._page_ws(target_id)
        self._ws = websocket.create_connection(ws_url,
                                               timeout=timeout, max_size=128 * 1024 * 1024)
        self._id = 0
        self._lock = threading.Lock()
        self._wait: dict[int, _Pending] = {}
        self._stop = False
        self.on_event = on_event
        self._reader = threading.Thread(target=self._loop, daemon=True)
        self._reader.start()

    def _page_ws(self, target_id: str | None) -> str:
        # 新标签的 targetCreated 经常早于 /json/list。指定了 id 就按 devtools/page/<id> 拼,
        # 避免这次失败后再也挂不上。没指定 id 时保持旧行为: 只用列表里的第一个 page。
        tabs = []
        try:
            tabs = json.loads(urllib.request.urlopen(
                self.endpoint + "/json/list", timeout=8).read())
        except Exception:
            if not target_id:
                raise
        pages = [t for t in tabs if t.get("type") == "page" and t.get("webSocketDebuggerUrl")]
        if target_id:
            matched = [t for t in pages if t.get("id") == target_id]
            if matched:
                return matched[0]["webSocketDebuggerUrl"]
            host = self.endpoint.split("://", 1)[-1]
            scheme = "wss://" if self.endpoint.startswith("https") else "ws://"
            return scheme + host + "/devtools/page/" + target_id
        if not pages:
            raise RuntimeError("没有可用的 page target: " + self.endpoint)
        return pages[0]["webSocketDebuggerUrl"]

    def _loop(self) -> None:
        while not self._stop:
            try:
                msg = json.loads(self._ws.recv())
            except websocket.WebSocketTimeoutException:
                continue          # 超时不停, 否则整条链路假死
            except Exception:
                return
            mid = msg.get("id")
            if mid is not None:
                with self._lock:
                    p = self._wait.pop(mid, None)
                if p is not None:
                    p.resp = msg
                    p.ev.set()
                continue
            if self.on_event is not None:
                try:
                    self.on_event(msg)
                except Exception:
                    pass

    @property
    def in_callback(self) -> bool:
        return threading.current_thread() is self._reader

    def send(self, method: str, params: dict | None = None, *, timeout: float = 60.0) -> dict:
        if self.in_callback:
            return self.send_nowait(method, params)
        with self._lock:
            self._id += 1
            mid = self._id
            p = _Pending()
            self._wait[mid] = p
        self._ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
        if not p.ev.wait(timeout):
            with self._lock:
                self._wait.pop(mid, None)
            raise TimeoutError("CDP " + method + " 超时")
        return p.resp or {}

    def send_nowait(self, method: str, params: dict | None = None) -> dict:
        """只发不等 —— 事件回调专用 (放行/填充的响应体调用方不需要)。"""
        with self._lock:
            self._id += 1
            mid = self._id
        self._ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
        return {"id": mid, "sent": True}

    def close(self) -> None:
        self._stop = True
        try:
            self._ws.close()
        except Exception:
            pass

    # ── Fetch 拦截原语 ────────────────────────────────────
    def enable_fetch(self, patterns: list[dict] | None = None) -> None:
        self.send("Fetch.enable", {"patterns": patterns or [{"urlPattern": "*"}]})

    def disable_fetch(self) -> None:
        # 浏览器已关时默认 60s 会把收尾拖死。3s 足够正常的 Fetch.disable。
        self.send("Fetch.disable", timeout=3.0)

    def continue_request(self, request_id: str, headers: list[dict] | None = None) -> None:
        params: dict = {"requestId": request_id}
        if headers:
            params["headers"] = headers
        self.send("Fetch.continueRequest", params)

    def fulfill(self, request_id: str, body: bytes, *, status: int = 200,
               headers: list[dict] | None = None) -> None:
        """用本地字节应答 —— 不产生网络流量, 不触发任何 TLS 握手。"""
        self.send("Fetch.fulfillRequest", {
            "requestId": request_id, "responseCode": status,
            "responseHeaders": headers or [
                {"name": "content-type", "value": "application/octet-stream"}],
            "body": base64.b64encode(body).decode(),
        })

    # ── 导航/取值 ─────────────────────────────────────────
    def navigate(self, url: str, *, settle: float = 1.5, timeout: float = 40.0) -> None:
        self.send("Page.enable", timeout=timeout)
        self.send("Page.navigate", {"url": url}, timeout=timeout)
        time.sleep(settle)

    def js(self, expr: str, *, await_promise: bool = True):
        r = self.send("Runtime.evaluate", {"expression": expr, "returnByValue": True,
                                           "awaitPromise": await_promise, "userGesture": True})
        res = r.get("result", {})
        if res.get("exceptionDetails"):
            raise RuntimeError("JS 异常: " + str(res["exceptionDetails"])[:200])
        return res.get("result", {}).get("value")

    def text(self) -> str:
        return str(self.js("document.body ? document.body.innerText : ''") or "")

    @property
    def url(self) -> str:
        return str(self.js("location.href") or "")
