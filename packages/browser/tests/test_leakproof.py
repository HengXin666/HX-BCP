"""Leak guards as the creation default, every create path, explicit acks, unavoidable exposures (no browser, no network).

Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-leakproof-by-default.md
"""

from __future__ import annotations

import lane
import pytest

import hxbr_testenv
from hx_browser import kernels, leakproof
from hx_browser.models import Environment, Fingerprint

API = "/api/v1"
FF = next(k.id for k in kernels.KERNELS if k.family == kernels.GECKO)


@pytest.fixture
def host(monkeypatch):
    """Host switches: bwrap present, Xephyr present, a visible desktop. Tests flip them."""
    state = {"bwrap": True, "xephyr": True, "desktop": True}
    monkeypatch.setattr(leakproof.sandbox, "available", lambda: state["bwrap"])
    monkeypatch.setattr(leakproof, "_has_xephyr", lambda: state["xephyr"])
    monkeypatch.setattr(leakproof, "_desktop_visible", lambda: state["desktop"])
    return state


@pytest.fixture
def c(tmp_path, host):
    with hxbr_testenv.client(tmp_path) as client:
        yield client


def _post(c, path, body=None):
    return c.post(API + path, json=body if body is not None else {}).json()


def test_new_browser_is_guarded_by_default(c):
    r = _post(c, "/environments", {"name": "a"})
    assert r["code"] == 0, r
    d = r["data"]
    assert d["fingerprint"]["webrtc_mode"] == "proxy" and d["prefs"]["isolation"] == "sandbox"
    assert d["leak"] == {"exposures": [], "acked": [], "unavoidable": {}, "guarded": True}


def test_explicit_leak_needs_ack(c):
    body = {"name": "b", "fingerprint": {"webrtc_mode": "real"}, "prefs": {"isolation": "process"}}
    r = _post(c, "/environments", body)
    assert r["code"] != 0 and "webrtc_real" in r["msg"] and "host_loopback" in r["msg"]
    r = _post(c, "/environments", {**body, "leak_ack": ["webrtc_real", "host_loopback"]})
    assert r["code"] == 0, r
    assert r["data"]["leak"]["acked"] == ["webrtc_real", "host_loopback"]
    assert r["data"]["leak_ack"] == ["webrtc_real", "host_loopback"]


def test_no_bwrap_falls_back_and_says_why(c, host):
    host["bwrap"] = False
    d = _post(c, "/environments", {"name": "c"})["data"]
    assert d["prefs"]["isolation"] == "process" and d["fingerprint"]["webrtc_mode"] == "proxy"
    assert d["leak"]["exposures"] == ["host_loopback"] and "bwrap" in d["leak"]["unavoidable"]["host_loopback"]


def test_visible_desktop_without_xephyr_is_unavoidable(c, host):
    host["xephyr"] = False
    d = _post(c, "/environments", {"name": "d"})["data"]
    assert d["prefs"]["isolation"] == "process" and "Xephyr" in d["leak"]["unavoidable"]["host_loopback"]
    d = _post(c, "/environments", {"name": "e", "fingerprint": {"headless": True}})["data"]
    assert d["prefs"]["isolation"] == "sandbox"


def test_update_opening_a_guard_needs_ack_and_ack_sticks(c):
    env_id = _post(c, "/environments", {"name": "f"})["data"]["id"]
    put = lambda body: c.put(API + "/environments/" + env_id, json=body).json()  # noqa: E731
    assert put({"fingerprint": {"webrtc_mode": "real"}})["code"] != 0
    assert put({"fingerprint": {"webrtc_mode": "real"}, "leak_ack": ["webrtc_real"]})["code"] == 0
    assert put({"name": "f2"})["code"] == 0  # unrelated edit keeps passing
    r = c.put(API + "/environments/%s/prefs" % env_id, json={"isolation": "process"}).json()
    assert r["code"] != 0 and "host_loopback" in r["msg"]
    r = c.put(API + "/environments/%s/prefs" % env_id, json={"isolation": "process", "leak_ack": ["host_loopback"]})
    assert r.json()["code"] == 0


def test_batch_template_duplicate_paths(c):
    r = _post(c, "/environments/batch", {"count": 2, "base": {"name": "g"}})
    assert r["code"] == 0
    envs = c.get(API + "/environments").json()["data"]["list"]
    assert all(e["leak"]["guarded"] for e in envs)
    r = _post(c, "/environments/batch", {"count": 2, "base": {"name": "h", "fingerprint": {"webrtc_mode": "real"}}})
    assert r["code"] != 0
    src = envs[0]["id"]
    assert _post(c, "/environments/%s/duplicate" % src)["data"]["leak"]["guarded"]


def test_template_apply_human_lane(tmp_path, host, monkeypatch):
    file = tmp_path / "human.token"
    monkeypatch.setattr(lane, "TOKEN_FILE", file)
    human = {lane.HEADER_LANE: "human", lane.HEADER_HUMAN_TOKEN: lane.issue_human_token(file)}
    with hxbr_testenv.client(tmp_path, headers=human) as h:
        tpl = _post(h, "/templates", {"name": "t", "fingerprint": {"webrtc_mode": "real"}})
        assert tpl["code"] == 0, tpl
        path = "/templates/%s/apply?count=1" % tpl["data"]["id"]
        assert _post(h, path)["code"] != 0
        assert _post(h, path + "&leak_ack=webrtc_real")["code"] == 0
        tpl = _post(h, "/templates", {"name": "u", "fingerprint": {"os": "Linux"}})["data"]
        assert _post(h, "/templates/%s/apply?count=1" % tpl["id"])["code"] == 0
        assert all(e["leak"]["guarded"] for e in h.get(API + "/environments").json()["data"]["list"] if e["name"].startswith("u"))


def test_import_forces_guards_unless_acked(host):
    env = Environment(fingerprint=Fingerprint(webrtc_mode="real"))
    assert leakproof.force_safe(env, {}) == ["webrtc_real", "host_loopback"]
    assert env.fingerprint.webrtc_mode == "proxy" and env.prefs.isolation == "sandbox"
    env = Environment(fingerprint=Fingerprint(webrtc_mode="real"))
    assert leakproof.force_safe(env, {"leak_ack": ["webrtc_real"]}) == ["host_loopback"]
    assert env.leak_ack == ["webrtc_real"]


def test_account_pack_import_route(c):
    pack = {"version": 1, "platform": "custom", "cookies": [], "fingerprint": {"webrtc_mode": "real"}}
    d = _post(c, "/account-packs/import", {"pack": pack})["data"]
    assert d["fingerprint"]["webrtc_mode"] == "proxy" and d["leak"]["guarded"]


def test_gecko_guard_reads_effective_prefs(host):
    env = Environment(fingerprint=Fingerprint(kernel=FF, browser="flower", webrtc_mode="proxy"))
    assert leakproof.effective_webrtc(env) == "proxy" and leakproof.webrtc_guarded(env)
    env.fingerprint.webrtc_mode = "real"
    assert not leakproof.webrtc_guarded(env)
    env.prefs.webrtc = "disable"
    assert leakproof.webrtc_guarded(env) and leakproof.effective_webrtc(env) == "disable"


def test_blink_hook_follows_prefs_too():
    env = Environment()
    env.prefs.webrtc = "proxy"
    assert leakproof.blink_hook(env) and leakproof.webrtc_guarded(env)
    env.prefs.webrtc = "inherit"
    env.fingerprint.webrtc_mode = "disable"
    assert leakproof.blink_hook(env)
