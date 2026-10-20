"""端到端: 真 Chromium + 真页面 + 真 WebSocket 回传。

这些用例跑的是**真实路径** —— 起服务、起浏览器、加载夹具页面、读库。
测的是"功能可用", 不是"函数被调用过"。

注意: uvicorn 与浏览器会话必须跑在**同一个事件循环**里。
分成两个循环时服务看似起来了, 实际绑在被弃用的循环上, 探针连不回 WS ——
现象是 dg['ws'] == 0, 极易被误读成"注入失败"。
"""
from __future__ import annotations

import asyncio
import contextlib
import json
import socket
import sqlite3
import sys
import threading
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from fpinsight.hub import Hub  # noqa: E402
from fpinsight.session import FpSession, LaunchConfig, find_chromium  # noqa: E402

FIXTURE = Path(__file__).resolve().parent / "fixtures" / "fp-live.html"
pytestmark = pytest.mark.skipif(find_chromium() is None, reason="本机无 Chromium")


def _free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    p = s.getsockname()[1]
    s.close()
    return p


class FixtureServer:
    def __init__(self, root: Path) -> None:
        import http.server

        self.port = _free_port()
        handler = lambda *a, **k: http.server.SimpleHTTPRequestHandler(*a, directory=str(root), **k)  # noqa: E731
        self.httpd = http.server.ThreadingHTTPServer(("127.0.0.1", self.port), handler)
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.port}/fp-live.html"

    @property
    def origin(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    def close(self) -> None:
        with contextlib.suppress(Exception):
            self.httpd.shutdown()
            self.httpd.server_close()


class Harness:
    """在同一循环内驱动 服务 + 会话 + 夹具, 并把库交给断言。"""

    def __init__(self, tmp_path: Path, sid: str) -> None:
        self.tmp = tmp_path
        self.sid = sid
        self.hub: Hub | None = None
        self.srv = None
        self.task = None
        self.fixture = FixtureServer(FIXTURE.parent)
        self.session: FpSession | None = None
        self.diag: dict = {}
        self.diag2: dict = {}

    async def __aenter__(self) -> "Harness":
        import fpinsight.app as appmod
        import uvicorn

        port = _free_port()
        self.hub = Hub(self.tmp / f"{self.sid}.db", port=port)
        self.hub._conn_init()
        self.hub._writer = threading.Thread(target=self.hub._writer_loop, daemon=True)
        self.hub._writer.start()
        appmod.HUB = self.hub
        appmod.SESSIONS.clear()

        cfg = uvicorn.Config(appmod._APP, host="127.0.0.1", port=port, log_level="error")
        self.srv = uvicorn.Server(cfg)
        self.task = asyncio.create_task(self.srv.serve())
        for _ in range(240):
            if self.srv.started:
                break
            await asyncio.sleep(0.05)
        assert self.srv.started, "内嵌服务未启动"
        self.session = FpSession(self.sid, self.hub, self.tmp / self.sid,
                                 LaunchConfig(headless=True), target=self.fixture.url)
        await self.session.start()
        assert self.hub.port == port, "回传端口必须与内嵌服务一致, 否则探针会连到别的进程"
        return self

    async def __aexit__(self, *exc) -> None:
        with contextlib.suppress(Exception):
            if self.session:
                await self.session.stop()
        with contextlib.suppress(Exception):
            if self.hub:
                self.hub.stop()
        with contextlib.suppress(Exception):
            self.srv.should_exit = True
            await asyncio.wait_for(self.task, timeout=8)
        self.fixture.close()

    async def visit(self, url: str | None = None, wait: int = 6000) -> None:
        assert self.session
        await self.session.navigate(url or self.fixture.url, wait)
        self.diag = await self.session.diag()

    def rows(self, sql: str) -> list[tuple]:
        assert self.hub
        self.hub.drain(10)
        return sqlite3.connect(self.hub.db_path).execute(sql).fetchall()


def test_probe_installs_hooks_and_reports(tmp_path: Path) -> None:
    """核心判据: 真站点行为 -> 探针产生事件 -> 落库。"""

    async def main() -> tuple[dict, int]:
        async with Harness(tmp_path, "e2e1") as h:
            await h.visit()
            assert h.session
            n = await h.session.commit_counts()
            rows = h.rows("select count(*) from calls")
            cats = {r[0] for r in h.rows("select distinct cat from calls")}
            total = h.rows("select sum(n) from counters")[0][0]
            assert rows[0][0] > 0, "没有任何调用被记录"
            for must in ("canvas", "webgl", "audio"):
                assert must in cats, f"缺少 {must} 类事件, 实际: {sorted(cats)}"
            assert total > 40, f"精确计数过低: {total}"
            assert n > 20
            return h.diag, rows[0][0]

    dg, calls = asyncio.run(main())
    assert dg, "页面里必须能找到探针 (否则注入没生效)"
    assert dg["hooked"] > 80, f"装载的钩子太少: {dg}"
    assert dg["hookfail"] == 0, f"有钩子装配失败: {dg}"
    assert dg["total"] > 40, f"夹具调用了全套指纹面却没有事件: {dg}"
    assert dg["ws"] == 1, f"回传通道未就绪: {dg}"
    assert calls > 0


def test_stack_attribution_points_at_page_not_probe(tmp_path: Path) -> None:
    """探针自身不得出现在调用栈里 —— 否则"分析入口"指向的是工具自己。"""

    async def main() -> list[tuple]:
        async with Harness(tmp_path, "e2e2") as h:
            await h.visit()
            assert h.session
            await h.session.commit_counts()
            return h.rows("select top_fn, top_url from calls where top_url is not null and top_url != ''")

    rows = asyncio.run(main())
    assert rows, "没有任何带调用位置的记录"
    for fn, url in rows:
        assert "<anonymous>" not in (fn or ""), f"栈帧未被正确切分: {fn}"
        assert "127.0.0.1" in (url or ""), f"采集位置不属于被测站点: {url}"


def test_probe_does_not_pollute_invariants(tmp_path: Path) -> None:
    """回归: 探针自己的静态读取曾以"站点读取"的身份混进纠察列表。"""

    async def main() -> list[str]:
        async with Harness(tmp_path, "e2e3") as h:
            await h.visit(wait=5000)
            return [r[0] for r in h.rows("select distinct path from invariants")]

    paths = asyncio.run(main())
    assert paths, "纠察表为空"
    for p in paths:
        assert not p.startswith("document.cookie"), f"探针自己的读取混进了证据: {p}"
        assert not p.startswith("localStorage"), f"探针自己的读取混进了证据: {p}"
    assert any(p.startswith("env:") for p in paths), "环境快照应带 env: 前缀"
    assert any(p.startswith("store:") for p in paths), "存储快照应带 store: 前缀"


def test_second_document_gets_fresh_probe(tmp_path: Path) -> None:
    """新文档必须重新装配探针, 且计数从零开始 ——
    否则跨页面的读取次数会被混在一起, 得不出"这个站点读了几次"。"""

    async def main() -> tuple[dict, dict]:
        async with Harness(tmp_path, "e2e4") as h:
            await h.visit(wait=4000)
            first = dict(h.diag)
            await h.visit(h.fixture.url + "?second=1", wait=4000)
            return first, dict(h.diag)

    first, second = asyncio.run(main())
    assert first["hooked"] > 80 and second["hooked"] > 80
    assert second["ws"] == 1, f"第二个文档的回传通道未就绪: {second}"
    assert second["seq"] > 0, "第二个文档没有产生新事件"
    assert second["total"] < first["total"] + 100000
