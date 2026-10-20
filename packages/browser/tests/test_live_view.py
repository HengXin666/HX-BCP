"""实时预览会话: 假 CDP 驱动 (CI 上无浏览器)。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md

断言的是**发给 CDP 的实际调用序列** (方法名 + 参数), 不是"函数被调用了":
ack 有没有真的回、坐标有没有真的换算、切标签后 screencast 有没有在新连接上重开。
"""

from __future__ import annotations

import asyncio

import livefake
from livefake import ACK, BLINK, CAST, FF, PORT, STOP, TAB_B_WS, FakeConn
from hx_browser import live_nav, live_view
from hx_browser import live_protocol as lp
from hx_browser.models import Environment, Fingerprint

fake_cdp = livefake.fake_cdp_fixture


def _session(sent: list[dict]) -> live_view.LiveSession:
    async def send(ev: dict) -> None:
        sent.append(ev)

    return live_view.LiveSession(PORT, send, "grid")


def _frame(session_id: int, w: int = 1280, h: int = 720) -> dict:
    meta = {"deviceWidth": w, "deviceHeight": h}
    return {
        "method": "Page.screencastFrame",
        "params": {"data": "QUJD", "sessionId": session_id, "metadata": meta},
    }


def test_start_enables_page_and_casts_grid_preset(fake_cdp):
    async def run():
        s = _session([])
        await s.start()
        conn = FakeConn.instances[0]
        assert conn.methods()[:3] == ["Page.enable", "Runtime.enable", CAST]
        assert conn.calls[2][1] == live_view.GRID_QUALITY
        assert conn.on_event is not None and s.streaming
        await s.close()
        assert STOP in conn.methods() and conn.closed

    asyncio.run(run())


def test_frame_is_acked_then_forwarded(fake_cdp):
    async def run():
        sent: list[dict] = []
        order: list[tuple[str, str]] = []
        FakeConn.log = order

        async def send(ev: dict) -> None:
            sent.append(ev)
            order.append(("client", ev["type"]))

        s = live_view.LiveSession(PORT, send, "grid")
        await s.start()
        conn = FakeConn.instances[0]
        await s._on_event(conn, _frame(42))
        await s.frames.drain()
        assert (ACK, {"sessionId": 42}) in conn.calls
        # ack 必须先于转发: 反过来客户端慢时 Chromium 会因未确认帧数停推
        assert order.index(("cdp", ACK)) < order.index(("client", lp.EVT_FRAME))
        frames = [e for e in sent if e["type"] == lp.EVT_FRAME]
        assert frames and frames[0]["sessionId"] == 42 and frames[0]["data"] == "QUJD"
        assert (s.page_w, s.page_h) == (1280.0, 720.0)
        await s.close()

    asyncio.run(run())


def test_mouse_is_scaled_from_client_to_page(fake_cdp):
    async def run():
        s = _session([])
        await s.start()
        await s._on_event(s.conn, _frame(1))
        await s.handle(
            {
                "type": lp.MSG_MOUSE,
                "action": "down",
                "x": 100,
                "y": 50,
                "clientWidth": 640,
            }
        )
        method, params = FakeConn.instances[0].calls[-1]
        assert method == lp.CDP_MOUSE and (params["x"], params["y"]) == (200.0, 100.0)
        await s.close()

    asyncio.run(run())


def test_keys_text_and_navigation(fake_cdp):
    async def run():
        s = _session([])
        await s.start()
        conn = FakeConn.instances[0]
        await s.handle({"type": lp.MSG_KEY, "action": "down", "key": "a"})
        await s.handle({"type": lp.MSG_TEXT, "text": "中文"})
        await s.handle({"type": lp.MSG_NAVIGATE, "url": "example.org"})
        await s.handle({"type": lp.MSG_NAVIGATE, "action": "back"})
        await s.handle({"type": lp.MSG_NAVIGATE, "action": "reload"})
        tail = conn.calls[-6:]
        assert tail[0][0] == lp.CDP_KEY and tail[1] == (lp.CDP_TEXT, {"text": "中文"})
        assert tail[2] == ("Page.navigate", {"url": "https://example.org"})
        assert tail[4] == ("Page.navigateToHistoryEntry", {"entryId": 10})
        assert tail[5][0] == "Page.reload"
        await s.close()

    asyncio.run(run())


def test_history_edge_and_bad_messages_report_errors(fake_cdp):
    async def run():
        sent: list[dict] = []
        s = _session(sent)
        await s.start()
        FakeConn.instances[0].history = {"currentIndex": 0, "entries": [{"id": 1}]}
        await s.handle({"type": lp.MSG_NAVIGATE, "action": "back"})
        await s.handle({"type": "teleport"})
        await s.handle({"type": lp.MSG_MOUSE, "action": "hover"})
        errs = [e["message"] for e in sent if e["type"] == lp.EVT_ERROR]
        assert len(errs) == 3 and "后退" in errs[0] and "teleport" in errs[1]
        assert all(not e.get("fatal") for e in sent if e["type"] == lp.EVT_ERROR)
        await s.close()

    asyncio.run(run())


def test_pause_resume_and_quality_switch(fake_cdp):
    async def run():
        s = _session([])
        await s.start()
        conn = FakeConn.instances[0]
        await s.handle({"type": lp.MSG_PAUSE})
        await s.handle({"type": lp.MSG_PAUSE})  # 幂等: 不重复 stop
        assert conn.methods().count(STOP) == 1 and not s.streaming
        await s.handle({"type": lp.MSG_RESUME})
        await s.handle({"type": lp.MSG_QUALITY, "preset": "focus"})
        assert conn.calls[-1] == (CAST, live_view.FOCUS_QUALITY) and s.streaming
        await s.close()

    asyncio.run(run())


def test_tabs_list_and_activate_reattach(fake_cdp):
    async def run():
        sent: list[dict] = []
        s = _session(sent)
        await s.start()
        await s.handle({"type": lp.MSG_TABS})
        tabs = next(e["tabs"] for e in sent if e["type"] == lp.EVT_TABS)
        assert [t["targetId"] for t in tabs] == ["A", "B"]  # service_worker 不算标签
        assert [t["active"] for t in tabs] == [True, False]
        await s.handle({"type": lp.MSG_ACTIVATE, "targetId": "B"})
        old, new = FakeConn.instances
        assert old.closed and new.ws_url == TAB_B_WS and CAST in new.methods()
        assert any(u.endswith("/activate/B") for u in fake_cdp)
        await s.handle({"type": lp.MSG_PAUSE})
        await s.handle({"type": lp.MSG_ACTIVATE, "targetId": "A"})
        assert CAST not in FakeConn.instances[-1].methods() and not s.streaming
        await s.handle({"type": lp.MSG_ACTIVATE, "targetId": "ZZ"})
        assert "ZZ" in sent[-1]["message"]
        await s.close()

    asyncio.run(run())


def test_normalize_url():
    assert live_nav.normalize_url("example.org") == "https://example.org"
    assert live_nav.normalize_url("http://x") == "http://x"
    assert live_nav.normalize_url("about:blank") == "about:blank"
    assert live_nav.normalize_url("ABOUT:blank") == "ABOUT:blank"  # scheme 不区分大小写
    assert live_nav.normalize_url("Data:text/plain,x") == "Data:text/plain,x"


def test_gecko_unsupported_reason():
    assert live_view.unsupported_reason(Environment(fingerprint=Fingerprint(kernel=FF)))
    assert (
        live_view.unsupported_reason(Environment(fingerprint=Fingerprint(kernel=BLINK)))
        == ""
    )
