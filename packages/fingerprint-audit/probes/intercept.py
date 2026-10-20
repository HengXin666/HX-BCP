"""浏览器层拦截原语 — HX-Capture-Proxy 缓存层的种子实现。

为什么必须自成模块 (不再是测试脚本里的一段):
  实测撞到 (2026-09-18): 用**两个** CDP 连接 (一个拦截、一个导航) 时,
  拦截侧的读超时会静默结束放行循环, 于是页面导航永远等不到响应, 整个链路假死。
  根因不是超时值, 而是**把事件流和请求流拆到两条连接上**。
  正确形状是单会话: 一个 reader 同时收"响应(id 配对)"和"事件(回调分发)"。

同时确认的两条实测事实 (见 knowledge/hx-capture-proxy/):
  · `Fetch.enable` + 全放行 **不改指纹** (顺序无关量 5/5 相同, 12 次暂停全放行);
  · `Fetch.fulfillRequest` 能在请求发出前用本地 blob 应答, 页面正常读到内容,
    **全程不碰 TLS**。这就是"缓存收益 + 指纹 100%"能同时成立的机制。

纪律: 本模块只做原语, 不含缓存策略 (什么该命中、TTL 怎么算属于上层)。
"""

# Agent Note: .agents/notes/implemented/architecture/2026-09-18-cache-at-browser-layer.md
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
    """单条 CDP 会话: 响应按 id 配对, 事件按回调分发 (两者同一 reader)。"""

    def __init__(self, endpoint: str, on_event=None, timeout: float = 120.0) -> None:
        tabs = json.loads(urllib.request.urlopen(endpoint.rstrip("/") + "/json/list",
                                                 timeout=8).read())
        page = [t for t in tabs if t.get("type") == "page" and t.get("webSocketDebuggerUrl")]
        if not page:
            raise RuntimeError("没有可用的 page target: " + endpoint)
        self._ws = websocket.create_connection(page[0]["webSocketDebuggerUrl"],
                                               timeout=timeout, max_size=128 * 1024 * 1024)
        self._id = 0
        self._lock = threading.Lock()
        self._wait: dict[int, _Pending] = {}
        self._stop = False
        self.on_event = on_event
        self._reader = threading.Thread(target=self._loop, daemon=True)
        self._reader.start()

    # ── 内部 ──────────────────────────────────────────────
    def _loop(self) -> None:
        """唯一读者。**超时绝不退出循环** —— 静默退出等于整条链路假死
        (这正是"两条连接"版本的致命缺陷)。"""
        while not self._stop:
            try:
                msg = json.loads(self._ws.recv())
            except websocket.WebSocketTimeoutException:
                continue
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

    def send(self, method: str, params: dict | None = None, timeout: float = 60.0) -> dict:
        """发一条命令并等它的响应。

        **在事件回调里调用会自动降级为不等响应** (见 _send_nowait 的注释):
        否则 reader 线程会在回调里等 reader 自己, 永久死锁。
        """
        if threading.current_thread() is self._reader:
            return self._send_nowait(method, params)
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

    def _send_nowait(self, method: str, params: dict | None = None) -> dict:
        """只发不等。给**事件回调**用 (拦截时必须在这里放行/填充)。

        实测死锁 (2026-09-18): 回调里调 send() 会等一个只有 reader 才能 set 的事件,
        而 reader 此刻正卡在这个回调里 —— 自己等自己, 页面导航永远拿不到响应,
        表现为「CDP Page.navigate 超时」。放行/填充的响应体对方并不需要,
        所以回调路径一律只发不等。
        """
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

    # ── 拦截原语 ──────────────────────────────────────────
    def enable_fetch(self, patterns: list[dict] | None = None) -> None:
        self.send("Fetch.enable", {"patterns": patterns or [{"urlPattern": "*"}]})

    def disable_fetch(self) -> None:
        self.send("Fetch.disable")

    def continue_request(self, request_id: str, headers: list[dict] | None = None) -> None:
        params: dict = {"requestId": request_id}
        if headers:
            params["headers"] = headers
        self.send("Fetch.continueRequest", params)

    def fulfill(self, request_id: str, body: bytes, *,
               status: int = 200, headers: list[dict] | None = None) -> None:
        """用本地字节应答, **不产生任何网络流量、不触发任何 TLS 握手**。"""
        self.send("Fetch.fulfillRequest", {
            "requestId": request_id, "responseCode": status,
            "responseHeaders": headers or [{"name": "content-type", "value": "application/octet-stream"}],
            "body": base64.b64encode(body).decode(),
        })

    # ── 导航/取值 ─────────────────────────────────────────
    def navigate(self, url: str, settle: float = 1.5, timeout: float = 40.0) -> None:
        self.send("Page.enable", timeout=timeout)
        self.send("Page.navigate", {"url": url}, timeout=timeout)
        time.sleep(settle)

    def text(self) -> str:
        r = self.send("Runtime.evaluate", {"expression": "document.body ? document.body.innerText : ''",
                                           "returnByValue": True})
        return str(r.get("result", {}).get("result", {}).get("value", ""))

    def js(self, expr: str, await_promise: bool = True):
        r = self.send("Runtime.evaluate", {"expression": expr, "returnByValue": True,
                                           "awaitPromise": await_promise, "userGesture": True})
        res = r.get("result", {})
        if res.get("exceptionDetails"):
            raise RuntimeError("JS 异常: " + str(res["exceptionDetails"])[:200])
        return res.get("result", {}).get("value")
