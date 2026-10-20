"""极简 CDP 驱动 — 只做质检需要的最小原语 (不引入任何自动化框架)。

为什么要自己写这几行:
  · 质检必须对**任意**环境通用 —— HX-Browser / CloakBrowser / 系统 Chrome / 未来的任何实现,
    只要能被 `--remote-debugging-port` 驱动就适用, 不该绑死在某一个框架上;
  · 质检报告要干净 —— 框架自身的注入 (initScript/defineProperty hack) 本身就是指纹信号,
    会污染被测对象 (本仓库已有铁律: 环境自检只在协议层/源码层, 不做 JS 注入)。

用法:
    b = Browser("http://127.0.0.1:43020")
    b.goto("https://example.com")
    print(b.js("navigator.userAgent"))
    b.close()
"""
from __future__ import annotations

import json
import time
import urllib.request

import websocket  # type: ignore


class Browser:
    """连一个已存在的 CDP 端点 (不负责拉起浏览器)。"""

    def __init__(self, endpoint: str = "http://127.0.0.1:43020", timeout: int = 30):
        self.endpoint = endpoint.rstrip("/")
        self.timeout = timeout
        self._id = 0
        self._ws = None
        self._target = None

    # ── 连接 ──────────────────────────────────────────────
    def _tabs(self) -> list[dict]:
        raw = urllib.request.urlopen(self.endpoint + "/json/list", timeout=8).read()
        return json.loads(raw)

    def connect(self, target: str = "page", create: bool = False) -> "Browser":
        """连到一个 page target; create=True 时新建标签页。"""
        if create:
            urllib.request.urlopen(self.endpoint + "/json/new?about:blank", timeout=8).read()
            time.sleep(0.4)
        tabs = [t for t in self._tabs() if t.get("type") == target and t.get("webSocketDebuggerUrl")]
        if not tabs:
            raise RuntimeError(f"{self.endpoint} 上没有可用的 {target} target")
        self._target = tabs[0]
        self._ws = websocket.create_connection(self._target["webSocketDebuggerUrl"],
                                               timeout=self.timeout, max_size=64 * 1024 * 1024)
        return self

    def close(self) -> None:
        try:
            if self._ws:
                self._ws.close()
        except Exception:
            pass

    def __enter__(self) -> "Browser":
        return self.connect() if not self._ws else self

    def __exit__(self, *exc) -> None:
        self.close()

    # ── CDP 原语 ──────────────────────────────────────────
    def send(self, method: str, params: dict | None = None, timeout: float | None = None) -> dict:
        if self._ws is None:
            self.connect()
        self._id += 1
        mid = self._id
        self._ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
        deadline = time.time() + (timeout or self.timeout)
        while time.time() < deadline:
            msg = json.loads(self._ws.recv())
            if msg.get("id") == mid:
                return msg
        raise TimeoutError(f"CDP {method} 超时")

    def js(self, expr: str, await_promise: bool = True, timeout: float | None = None):
        """求值 JS 表达式并返回值 (awaitPromise 默认开, 便于直接写 async IIFE)。"""
        r = self.send("Runtime.evaluate", {
            "expression": expr, "returnByValue": True,
            "awaitPromise": await_promise, "userGesture": True,
        }, timeout=timeout)
        res = r.get("result", {})
        if res.get("exceptionDetails"):
            raise RuntimeError(f"JS 异常: {res['exceptionDetails'].get('text')}")
        return res.get("result", {}).get("value")

    def goto(self, url: str, wait_s: float = 3.0) -> None:
        self.send("Page.enable")
        self.send("Page.navigate", {"url": url})
        time.sleep(wait_s)

    def url(self) -> str:
        return str(self.js("location.href") or "")

    def text(self, limit: int = 0) -> str:
        t = str(self.js("(document.body&&document.body.innerText)||''") or "")
        return t[:limit] if limit else t

    def wait_for(self, expr: str, timeout_s: float = 30, interval: float = 0.6) -> bool:
        """轮询等待 JS 表达式为真。"""
        deadline = time.time() + timeout_s
        while time.time() < deadline:
            try:
                if self.js(expr, await_promise=False, timeout=10):
                    return True
            except Exception:
                pass
            time.sleep(interval)
        return False


def cdp_alive(endpoint: str = "http://127.0.0.1:43020", timeout: float = 3.0) -> bool:
    try:
        urllib.request.urlopen(endpoint.rstrip("/") + "/json/version", timeout=timeout).read()
        return True
    except Exception:
        return False
