"""多页面挂载: 事件解析, 以及页面在连上之前关掉时不能留下引擎。"""

import os
import sys
import threading
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

import hxcp.attach as attach  # noqa: E402
import hxcp.cdp as cdp  # noqa: E402
from hxcp.attach import Fleet, destroyed_id, page_id_from_event  # noqa: E402


def test_page_events_ignore_non_pages():
    created = {
        "method": "Target.targetCreated",
        "params": {"targetInfo": {"type": "page", "targetId": "A"}},
    }
    iframe = {
        "method": "Target.targetCreated",
        "params": {"targetInfo": {"type": "iframe", "targetId": "B"}},
    }
    assert page_id_from_event(created) == "A"
    assert page_id_from_event(iframe) == ""
    assert (
        destroyed_id({"method": "Target.targetDestroyed", "params": {"targetId": "A"}})
        == "A"
    )
    assert (
        destroyed_id(
            {"method": "Target.detachedFromTarget", "params": {"sessionId": "s"}}
        )
        == ""
    )


def test_missing_target_uses_page_websocket():
    session = cdp.Session.__new__(cdp.Session)
    session.endpoint = "http://127.0.0.1:9222"

    class _Body:
        def read(self):
            return b"[]"

    orig = cdp.urllib.request.urlopen
    cdp.urllib.request.urlopen = lambda *_a, **_k: _Body()
    try:
        url = session._page_ws("ABC")
    finally:
        cdp.urllib.request.urlopen = orig
    assert url == "ws://127.0.0.1:9222/devtools/page/ABC"


def test_detach_during_attach_stops_engine():
    fleet = Fleet.__new__(Fleet)
    fleet.endpoint = "http://127.0.0.1:1"
    fleet._slots = {}
    fleet._dead = set()
    fleet._inflight = 0
    fleet._lock = threading.Lock()
    fleet._idle = threading.Condition(fleet._lock)
    fleet._stop = False
    started = threading.Event()
    stopped = []

    class _Eng:
        def start(self):
            started.set()
            time.sleep(0.2)

        def stop(self):
            stopped.append("stop")

    class _Sess:
        def close(self):
            stopped.append("close")

    fleet._make = lambda _session: _Eng()
    orig = attach.Session
    attach.Session = lambda *_a, **_k: _Sess()
    try:
        thread = threading.Thread(target=fleet._attach, args=("T1",))
        thread.start()
        assert started.wait(1)
        fleet._detach("T1")
        thread.join(2)
    finally:
        attach.Session = orig
    assert stopped == ["stop", "close"]
    assert "T1" not in fleet._slots
    assert fleet._inflight == 0
