"""实时预览 WS 端点与在跑副本列表 (TestClient, 临时数据目录, 假 CDP)。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md
"""

from __future__ import annotations

import json

import pytest
from starlette.websockets import WebSocketDisconnect

import hxbr_testenv
import livefake
from livefake import BLINK, FF, PORT
from hx_browser import api_live, browser, live_view
from hx_browser import live_protocol as lp
from hx_browser.models import RuntimeInfo

fake_cdp = livefake.fake_cdp_fixture
NOT_FOUND_MSG = "环境不存在"


def _add_env(c, **fp) -> str:
    r = c.post("/api/v1/environments", json={"name": "预览", "fingerprint": fp}).json()
    assert r["code"] == 0, r
    return r["data"]["id"]


def _first_event(c, path: str, origin: str = "") -> dict:
    headers = {"origin": origin} if origin else {}
    with c.websocket_connect(path, headers=headers) as ws:
        ev = json.loads(ws.receive_text())
        with pytest.raises(WebSocketDisconnect):
            ws.receive_text()
    return ev


def test_ws_refuses_gecko_missing_and_stopped(tmp_path):
    with hxbr_testenv.client(tmp_path) as c:
        ff = _add_env(c, kernel=FF)
        assert (
            "Firefox" in _first_event(c, f"/api/v1/environments/{ff}/live")["message"]
        )
        assert _first_event(c, "/api/v1/environments/nope/live")["fatal"] is True
        blink = _add_env(c, kernel=BLINK)
        ev = _first_event(c, f"/api/v1/environments/{blink}/live")
        assert ev["message"] == live_view.NOT_RUNNING
        assert c.get("/api/v1/live/envs").json()["data"]["envs"] == []


def test_ws_streams_from_running_env_and_lists_it(tmp_path, fake_cdp, monkeypatch):
    with hxbr_testenv.client(tmp_path) as c:
        env_id = _add_env(c, kernel=BLINK)
        rt = RuntimeInfo(debug_port=PORT, kernel=BLINK, isolation="sandbox")
        monkeypatch.setattr(
            browser, "get_runtime", lambda i: rt if i == env_id else None
        )
        monkeypatch.setattr(
            "hx_browser.api_live.get_runtime", lambda i: rt if i == env_id else None
        )
        monkeypatch.setattr(
            "hx_browser.envview.get_runtime", lambda i: rt if i == env_id else None
        )
        envs = c.get("/api/v1/live/envs").json()["data"]["envs"]
        assert (
            len(envs) == 1
            and envs[0]["isolation"] == "sandbox"
            and envs[0]["supported"]
        )
        with c.websocket_connect(
            f"/api/v1/environments/{env_id}/live?preset=grid"
        ) as ws:
            ws.send_text(json.dumps({"type": lp.MSG_TABS}))
            seen = set()
            for _ in range(4):
                ev = json.loads(ws.receive_text())
                seen.add(ev["type"])
                if ev["type"] == lp.EVT_TABS:
                    break
            assert lp.EVT_TABS in seen


def test_ws_requires_api_key_when_configured(tmp_path):
    with hxbr_testenv.client(tmp_path) as c:
        s = hxbr_testenv.config.settings_manager.load()
        s.api_key = "k1"
        hxbr_testenv.config.settings_manager.save(s)
        ev = _first_event(c, "/api/v1/environments/x/live")
        assert ev["message"] == api_live.AUTH_REFUSED
        # 鉴权通过后才轮到查环境: 明确落到「环境不存在」才说明 query key 被接受
        ev = _first_event(c, "/api/v1/environments/x/live?key=k1")
        assert ev["message"] == NOT_FOUND_MSG


def test_ws_rejects_cross_site_origin_even_without_api_key(tmp_path, monkeypatch):
    monkeypatch.delenv(api_live.ORIGINS_ENV, raising=False)
    with hxbr_testenv.client(tmp_path) as c:
        path = "/api/v1/environments/x/live"
        assert (
            _first_event(c, path, "https://evil.example")["message"]
            == api_live.AUTH_REFUSED
        )
        assert (
            _first_event(c, path, "http://127.0.0.1:8930")["message"] == NOT_FOUND_MSG
        )
        monkeypatch.setenv(api_live.ORIGINS_ENV, "https://console.example")
        assert (
            _first_event(c, path, "https://console.example")["message"] == NOT_FOUND_MSG
        )


def test_origin_policy():
    allowed = api_live.origin_allowed
    assert allowed("", "127.0.0.1:8325")  # 脚本客户端不带 Origin
    assert allowed("http://localhost:36000", "127.0.0.1:8325")
    assert allowed("http://[::1]:8930", "127.0.0.1:8325")
    assert allowed("http://10.0.0.5:8325", "10.0.0.5:8325")  # 同 host (面板自己)
    assert not allowed("http://10.0.0.5:8930", "10.0.0.5:8325")
    assert not allowed("https://127.0.0.1.evil.example", "127.0.0.1:8325")
    assert allowed(
        "https://ops.example/", "x:1", "https://ops.example, https://b.example"
    )
