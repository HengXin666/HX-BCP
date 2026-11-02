"""Lane scope over REST and MCP: 400 without project, agent sees own project, human reads agent browsers only, loopback is human.

Agent Note: .agents/notes/implemented/feature/2026-11-01-lane-folders.md
"""

from __future__ import annotations

import json

import lane
import pytest

import hxbr_testenv
from fastapi.testclient import TestClient
from hx_browser import api, envview
from hx_browser.models import RuntimeInfo

API = "/api/v1"


@pytest.fixture
def token(tmp_path, monkeypatch):
    file = tmp_path / "human.token"
    monkeypatch.setattr(lane, "TOKEN_FILE", file)
    return lane.issue_human_token(file)


@pytest.fixture
def c(tmp_path):
    with hxbr_testenv.client(tmp_path, headers={}) as client:
        yield client


def _agent(project):
    return {lane.HEADER_PROJECT: project}


def _human(token):
    return {lane.HEADER_LANE: "human", lane.HEADER_HUMAN_TOKEN: token}


def _create(c, headers, **body):
    r = c.post(API + "/environments", json=body, headers=headers)
    assert r.status_code == 200 and r.json()["code"] == 0, r.text
    return r.json()["data"]


def test_agent_create_without_project_is_400(c):
    r = c.post(API + "/environments", json={"name": "x"})
    assert r.status_code == 400 and r.json()["code"] == "project_required"
    r = c.post(API + "/environments", json={"name": "x"}, headers=_agent("Bad Name!"))
    assert r.status_code == 400
    assert c.get(API + "/environments").status_code == 400
    assert c.get(API + "/status").status_code == 200


def test_human_without_token_is_401(c, token):
    r = c.get(API + "/environments", headers={lane.HEADER_LANE: "human"})
    assert r.status_code == 401 and r.json()["code"] == "human_token_required"


@pytest.fixture
def local(tmp_path):
    """Client whose socket peer is 127.0.0.1 and whose Host is 127.0.0.1:8325, like a local browser."""
    hxbr_testenv.isolate(tmp_path)
    with TestClient(api.create_app(), headers={}, client=("127.0.0.1", 50000), base_url="http://127.0.0.1:8325") as client:
        yield client


def test_loopback_human_needs_no_token(local, tmp_path, monkeypatch):
    monkeypatch.setattr(lane, "TOKEN_FILE", tmp_path / "none.token")
    r = local.get(API + "/environments", headers={lane.HEADER_LANE: "human"})
    assert r.status_code == 200 and r.json()["code"] == 0
    s = local.get(API + "/lane/session", headers={lane.HEADER_LANE: "human"}).json()["data"]
    assert s["human"] is True and s["by"] == "loopback"


def test_loopback_without_lane_header_is_still_agent(local):
    r = local.get(API + "/environments")
    assert r.status_code == 400 and r.json()["code"] == "project_required"


def test_loopback_with_foreign_host_is_refused(local, tmp_path, monkeypatch):
    monkeypatch.setattr(lane, "TOKEN_FILE", tmp_path / "none.token")
    r = local.get(API + "/environments", headers={lane.HEADER_LANE: "human", "host": "evil.example:8325"})
    assert r.status_code == 401 and r.json()["code"] == "human_token_required"


def test_remote_fake_token_and_forwarded_for_are_401(c, token):
    spoof = {lane.HEADER_LANE: "human", lane.HEADER_HUMAN_TOKEN: "fake", "x-forwarded-for": "127.0.0.1",
             "x-real-ip": "127.0.0.1", "forwarded": "for=127.0.0.1", "host": "127.0.0.1:8325"}
    r = c.get(API + "/environments", headers=spoof)
    assert r.status_code == 401 and r.json()["code"] == "human_token_required"
    assert c.get(API + "/lane/session", headers=spoof).json()["data"]["human"] is False


def test_agent_sees_only_own_project(c):
    a = _create(c, _agent("crawler-a"), name="a1", folder="batch-1")
    b = _create(c, _agent("shop-b"), name="b1")
    assert a["lane"] == "agent" and a["project"] == "crawler-a" and a["folder"] == "batch-1"
    listed = c.get(API + "/environments", headers=_agent("crawler-a")).json()["data"]["list"]
    assert [e["id"] for e in listed] == [a["id"]]
    r = c.get(API + "/environments/" + b["id"], headers=_agent("crawler-a"))
    assert r.status_code == 403 and r.json()["code"] == "lane_forbidden"
    assert c.delete(API + "/environments/" + b["id"], headers=_agent("crawler-a")).status_code == 403
    assert c.get(API + "/settings", headers=_agent("crawler-a")).status_code == 403


def test_agent_can_not_touch_human_browser(c, token):
    h = _create(c, _human(token), name="mine", folder="daily")
    assert h["lane"] == "human" and h["project"] == ""
    assert c.post(API + "/environments/" + h["id"] + "/start", headers=_agent("crawler-a")).status_code == 403
    listed = c.get(API + "/environments", headers=_agent("crawler-a")).json()["data"]["list"]
    assert listed == []


def test_human_reads_agent_browser_and_sees_in_use(c, token, monkeypatch):
    a = _create(c, _agent("crawler-a"), name="a1")
    rt = RuntimeInfo(pid=1, started_at=123)
    monkeypatch.setattr(envview, "get_runtime", lambda env_id: rt if env_id == a["id"] else None)
    listed = c.get(API + "/environments", headers=_human(token)).json()["data"]["list"]
    row = next(e for e in listed if e["id"] == a["id"])
    assert row["in_use"] is True and row["in_use_since"] == 123 and row["readonly"] is True
    r = c.put(API + "/environments/" + a["id"], json={"name": "x"}, headers=_human(token))
    assert r.status_code == 403
    r = c.post(API + "/environments", json={"name": "y"}, headers=_human(token))
    assert r.json()["data"]["lane"] == "human"


def test_folders_stay_inside_their_group(c, token):
    hd = _human(token)
    assert c.post(API + "/folders", json={"name": "work"}, headers=hd).json()["code"] == 0
    assert c.post(API + "/folders", json={"name": "work"}, headers=hd).status_code == 409
    assert c.post(API + "/folders", json={"name": "a/b"}, headers=hd).status_code == 400
    _create(c, _agent("crawler-a"), name="a1", folder="batch-1")
    seen_by_human = c.get(API + "/folders", headers=hd).json()["data"]
    assert seen_by_human["human"] == ["work"] and seen_by_human["agent:crawler-a"] == ["batch-1"]
    seen_by_agent = c.get(API + "/folders", headers=_agent("shop-b")).json()["data"]
    assert seen_by_agent == {"agent:shop-b": []}
    e = _create(c, hd, name="h1", folder="work")
    assert c.delete(API + "/folders/work", headers=hd).status_code == 409
    assert c.put(API + "/folders/work", json={"name": "job"}, headers=hd).json()["data"]["name"] == "job"
    assert c.get(API + "/environments/" + e["id"], headers=hd).json()["data"]["folder"] == "job"


def test_mcp_requires_project(c):
    def rpc(args):
        body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "create_profile", "arguments": args}}
        return c.post("/mcp", json=body).json()["result"]

    missing = rpc({"name": "x"})
    assert missing["isError"] is True and "project" in missing["content"][0]["text"]
    made = rpc({"name": "x", "project": "crawler-a"})
    data = json.loads(made["content"][0]["text"])["data"]
    assert made["isError"] is False and data["lane"] == "agent" and data["project"] == "crawler-a"
