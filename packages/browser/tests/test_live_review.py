"""实时预览会话的健壮性: 评审 (PR #99) 指出的断流 / 串标签 / 堆帧 / 坏输入 (假 CDP, 无浏览器)。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md
"""

from __future__ import annotations

import asyncio

import livefake
from livefake import ACK, PORT, FakeConn
from hx_browser import cdp, live_view
from hx_browser import live_protocol as lp
from hx_browser.live_frames import FrameRelay

fake_cdp = livefake.fake_cdp_fixture
FATAL = "fatal"


def _frame(session_id: int) -> dict:
    meta = {"deviceWidth": 1280, "deviceHeight": 720}
    return {
        "method": "Page.screencastFrame",
        "params": {"data": "QUJD", "sessionId": session_id, "metadata": meta},
    }


async def _started(sent: list[dict], closed: list[bool] | None = None):
    async def send(ev: dict) -> None:
        sent.append(ev)

    async def on_fatal() -> None:
        if closed is not None:
            closed.append(True)

    s = live_view.LiveSession(PORT, send, "focus", on_fatal)
    await s.start()
    return s


def _errors(sent: list[dict]) -> list[dict]:
    return [e for e in sent if e["type"] == lp.EVT_ERROR]


def test_stale_tab_frames_are_neither_acked_to_new_tab_nor_forwarded(fake_cdp):
    async def run():
        sent: list[dict] = []
        s = await _started(sent)
        old = FakeConn.instances[0]
        await s.handle({"type": lp.MSG_ACTIVATE, "targetId": "B"})
        new = FakeConn.instances[-1]
        await s._on_event(old, _frame(7))  # 旧连接里排队到切换之后才到的帧
        await s.frames.drain()
        assert (ACK, {"sessionId": 7}) not in new.calls
        assert not [e for e in sent if e["type"] == lp.EVT_FRAME]
        await s.close()

    asyncio.run(run())


def test_frame_relay_keeps_only_latest_when_client_is_slow():
    async def run():
        gate = asyncio.Event()
        got: list[int] = []

        async def slow_send(ev: dict) -> None:
            await gate.wait()
            got.append(ev["n"])

        relay = FrameRelay(slow_send)
        for n in range(50):
            relay.push({"n": n})
            await asyncio.sleep(0)
        gate.set()
        await relay.drain()
        assert got == [0, 49] and relay.dropped == 48

    asyncio.run(run())


def test_upstream_close_reports_fatal_once_and_closes(fake_cdp):
    async def run():
        sent: list[dict] = []
        closed: list[bool] = []
        s = await _started(sent, closed)
        conn = FakeConn.instances[0]
        await conn.on_close()
        await conn.on_close()
        fatal = [e for e in _errors(sent) if e[FATAL]]
        assert len(fatal) == 1 and closed == [True]
        await s.close()

    asyncio.run(run())


def test_close_of_replaced_connection_is_ignored(fake_cdp):
    async def run():
        sent: list[dict] = []
        closed: list[bool] = []
        s = await _started(sent, closed)
        old = FakeConn.instances[0]
        await s.handle({"type": lp.MSG_ACTIVATE, "targetId": "B"})
        await old.on_close()
        assert closed == [] and not [e for e in _errors(sent) if e[FATAL]]
        await s.close()

    asyncio.run(run())


def test_bad_numbers_and_tab_list_failure_keep_session_alive(fake_cdp, monkeypatch):
    async def run():
        sent: list[dict] = []
        s = await _started(sent)
        await s.handle({"type": lp.MSG_MOUSE, "action": "down", "x": "abc", "y": 1})
        await s.handle(
            {"type": lp.MSG_KEY, "action": "down", "key": "a", "keyCode": {}}
        )
        await s.handle(
            {"type": lp.MSG_MOUSE, "action": "move", "x": float("nan"), "y": 1}
        )

        async def broken(url, timeout=5.0):
            raise TimeoutError("timed out")

        monkeypatch.setattr(cdp, "http_json", broken)
        await s.handle({"type": lp.MSG_TABS})
        errs = _errors(sent)
        assert len(errs) == 4 and not any(e[FATAL] for e in errs)
        assert "读取标签页失败" in errs[-1]["message"]
        await s.handle({"type": lp.MSG_TEXT, "text": "still alive"})
        assert FakeConn.instances[0].calls[-1] == (lp.CDP_TEXT, {"text": "still alive"})
        await s.close()

    asyncio.run(run())


def test_connection_closed_during_input_is_fatal(fake_cdp):
    async def run():
        sent: list[dict] = []
        closed: list[bool] = []
        s = await _started(sent, closed)
        FakeConn.instances[0].fail_with = cdp.ConnectionClosed(
            "cdp closed: peer closed"
        )
        await s.handle({"type": lp.MSG_TEXT, "text": "x"})
        assert closed == [True] and _errors(sent)[-1][FATAL]
        await s.close()

    asyncio.run(run())


def test_meta_loop_gives_up_after_consecutive_failures(fake_cdp, monkeypatch):
    async def run():
        sent: list[dict] = []
        closed: list[bool] = []
        monkeypatch.setattr(live_view, "_META_POLL_S", 0)
        s = await _started(sent, closed)
        FakeConn.instances[0].fail_with = cdp.CdpError("cdp timeout: Runtime.evaluate")
        for _ in range(20):
            await asyncio.sleep(0)
            if closed:
                break
        assert closed == [True] and "连续" in _errors(sent)[-1]["message"]
        await s.close()

    asyncio.run(run())
