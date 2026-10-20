"""实时预览测试共用的假 CDP (CdpConnection 替身 + /json 目标表)。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md
"""

from __future__ import annotations

import pytest

import hxbr_testenv  # noqa: F401 — 把 src 放进 sys.path
from hx_browser import cdp, kernels

PAGE_WS = "ws://fake/page/A"
TAB_B_WS = "ws://fake/page/B"
PORT = 9333
CAST = "Page.startScreencast"
STOP = "Page.stopScreencast"
ACK = "Page.screencastFrameAck"
# 测试用 blink 内核取默认的 cloak (仓库浏览器门禁只认 Chromix/CloakBrowser)
BLINK = next(k.id for k in kernels.KERNELS if k.family == kernels.BLINK)
FF = next(k.id for k in kernels.KERNELS if k.family == kernels.GECKO)


class FakeConn:
    """CdpConnection 的替身: 记下每次 send, 按方法名回预置结果。"""

    instances: list[FakeConn] = []

    #: 测试可设的共享事件日志: send 时追加 ("cdp", method), 用来断言跨对象的先后顺序
    log: list[tuple[str, str]] | None = None

    def __init__(self, ws_url: str):
        self.ws_url = ws_url
        self.on_event = None
        self.on_close = None
        self.fail_with: Exception | None = None
        self.calls: list[tuple[str, dict]] = []
        self.closed = False
        self.history = {
            "currentIndex": 1,
            "entries": [{"id": 10}, {"id": 11}, {"id": 12}],
        }
        FakeConn.instances.append(self)

    async def connect(self, timeout: float = 8.0) -> FakeConn:
        return self

    async def send(
        self, method: str, params: dict | None = None, timeout: float = 30.0
    ):
        self.calls.append((method, params or {}))
        if FakeConn.log is not None:
            FakeConn.log.append(("cdp", method))
        if self.fail_with is not None:
            raise self.fail_with
        if method == "Page.getNavigationHistory":
            return self.history
        if method == "Runtime.evaluate":
            return {"result": {"value": {"u": "https://example.org/", "t": "Example"}}}
        return {}

    async def close(self) -> None:
        self.closed = True

    def methods(self) -> list[str]:
        return [m for m, _ in self.calls]


TARGETS = [
    {
        "id": "A",
        "type": "page",
        "title": "甲",
        "url": "https://a/",
        "webSocketDebuggerUrl": PAGE_WS,
    },
    {
        "id": "B",
        "type": "page",
        "title": "乙",
        "url": "https://b/",
        "webSocketDebuggerUrl": TAB_B_WS,
    },
    {
        "id": "W",
        "type": "service_worker",
        "url": "https://sw/",
        "webSocketDebuggerUrl": "ws://sw",
    },
]


@pytest.fixture
def fake_cdp_fixture(monkeypatch):
    FakeConn.instances = []
    FakeConn.log = None
    opened: list[str] = []

    async def find_page_ws(port, timeout=15.0):
        assert port == PORT
        return PAGE_WS

    async def http_json(url, timeout=5.0):
        opened.append(url)
        return TARGETS

    monkeypatch.setattr(cdp, "CdpConnection", FakeConn)
    monkeypatch.setattr(cdp, "find_page_ws", find_page_ws)
    monkeypatch.setattr(cdp, "http_json", http_json)
    return opened
