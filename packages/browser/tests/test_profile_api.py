"""REST + MCP 端到端 (TestClient, 临时数据目录, 不启动浏览器、不出网)。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md
"""

from __future__ import annotations

import json

import pytest

import hxbr_testenv
from hx_browser import doctor, kernels, netcheck

X = "x"
FF = next(k.id for k in kernels.KERNELS if k.family == kernels.GECKO)  # Firefox 内核 id
API = "/api/v1"


@pytest.fixture
def c(tmp_path, monkeypatch):
    async def no_region(env):
        raise RuntimeError("offline test")

    async def no_clock(env):
        return netcheck.clock_unknown()

    monkeypatch.setattr(netcheck, "lookup_region", no_region)
    monkeypatch.setattr(doctor.netcheck, "clock_check", no_clock)
    with hxbr_testenv.client(tmp_path) as client:
        client.data_dir = tmp_path / "data"
        yield client


def _create(c, **body) -> dict:
    r = c.post(API + "/environments", json=body).json()
    assert r["code"] == 0, r
    return r["data"]


def test_create_with_proxy_link_kernel_and_prefs(c):
    d = _create(c, name="甲", tags=["shop", "us"], notes="主号",
                proxy={"link": "hxpxy://ghreg-us03:secret"}, fingerprint={"kernel": FF},
                prefs={"language_mode": "custom", "languages": ["en-US", "en"], "firefox_font_restriction": True})  # fmt: skip
    assert (
        d["proxy"]["type"] == "http"
        and d["proxy"]["port"] == 7890
        and d["proxy"]["username"] == "ghreg-us03"
    )
    assert d["fingerprint"]["browser"] == "flower" and d["kernel_family"] == "gecko"
    assert d["prefs"]["firefox_font_restriction"] is True
    assert d["pending_restart"] is False and d["deleted"] is False


def test_invalid_combinations_rejected(c):
    r = c.post(API + "/environments", json={"name": X, "fingerprint": {"kernel": FF},
               "prefs": {"firefox_resist_fingerprinting": True, "language_mode": "ip"}}).json()  # fmt: skip
    assert r["code"] != 0 and "互斥" in r["msg"]
    assert (
        c.post(
            API + "/environments", json={"name": X, "proxy": {"link": "ftp://a:1"}}
        ).json()["code"]
        != 0
    )
    assert (
        c.post(
            API + "/environments",
            json={"name": X, "fingerprint": {"kernel": "netscape"}},
        ).json()["code"]
        != 0
    )


def test_search_tags_and_filter(c):
    _create(c, name="alpha", tags=["shop"], notes="vip 客户")
    _create(c, name="beta", tags=["mail"])
    _create(c, name="gamma", tags=["shop", "mail"])
    names = lambda r: sorted(e["name"] for e in r["data"]["list"])  # noqa: E731
    assert names(c.get(API + "/profiles", params={"tag": "shop"}).json()) == [
        "alpha",
        "gamma",
    ]
    assert names(c.get(API + "/profiles", params={"search": "VIP"}).json()) == ["alpha"]
    assert names(c.get(API + "/environments", params={"search": "mail"}).json()) == [
        "beta",
        "gamma",
    ]  # 旧接口也认标签
    assert c.get(API + "/tags").json()["data"] == {"mail": 2, "shop": 2}


def test_trash_restore_purge_cleans_data_and_credentials(c):
    d = _create(c, name="del-me", proxy={"link": "socks5://u:topsecret@127.0.0.1:1080"})
    env_id = d["id"]
    prof = c.data_dir / "profiles" / env_id
    (prof / "Default").mkdir(parents=True)
    (prof / "Default" / "Cookies").write_text("x")
    assert (
        c.post(API + "/environments/%s/purge" % env_id).json()["code"] != 0
    )  # 必须先进最近删除
    assert c.delete(API + "/environments/" + env_id).json()["code"] == 0
    assert env_id not in [
        e["id"] for e in c.get(API + "/environments").json()["data"]["list"]
    ]
    assert [e["id"] for e in c.get(API + "/trash").json()["data"]["list"]] == [env_id]
    assert (
        c.post(API + "/environments/%s/start" % env_id).status_code == 404
    )  # 回收站里的不能启动
    assert prof.exists()  # 移至最近删除不动数据
    assert c.post(API + "/environments/%s/restore" % env_id).json()["code"] == 0
    assert c.get(API + "/trash").json()["data"]["total"] == 0
    c.delete(API + "/environments/" + env_id)
    r = c.post(API + "/environments/%s/purge" % env_id).json()
    assert r["code"] == 0 and r["data"]["profile_removed"] is True
    assert not prof.exists()
    assert "topsecret" not in (c.data_dir / "environments.json").read_text()


def test_legacy_hard_delete_still_works(c):
    env_id = _create(c, name="old-style")["id"]
    assert (
        c.delete(API + "/environments/%s?purge=true" % env_id).json()["data"]["purged"]
        is True
    )
    assert c.get(API + "/environments/" + env_id).status_code == 404


def test_doctor_fix_backup_and_restore(c):
    env_id = _create(c, name="doc", proxy={"link": "http://u:p@127.0.0.1:8080"})["id"]
    prof = c.data_dir / "profiles" / env_id / "Default"
    prof.mkdir(parents=True)
    (prof / "Preferences").write_text('{"before": true}')
    report = c.get(API + "/environments/%s/doctor" % env_id).json()["data"]
    by_id = {x["id"]: x for x in report["checks"]}
    assert by_id["network"]["status"] == "error"  # 离线: 如实报错, 不假装正常
    assert by_id["clock"]["status"] == "unknown"  # 取不到参考 = 尚未确认
    assert by_id["webrtc"]["fixable"] and by_id["dns"]["fixable"]
    fixed = c.post(
        API + "/environments/%s/doctor/fix" % env_id, json={"items": ["webrtc", "dns"]}
    ).json()["data"]
    rec = fixed["record"]
    assert (
        sorted(fixed["applied"]) == ["dns", "webrtc"]
        and "Default/Preferences" in rec["backup_files"]
    )
    prefs = c.get(API + "/environments/%s/prefs" % env_id).json()["data"]
    assert prefs["webrtc"] == "proxy" and prefs["dns_privacy"] is True
    (prof / "Preferences").write_text('{"after": true}')
    records = c.get(API + "/environments/%s/repairs" % env_id).json()["data"]["list"]
    assert [r["id"] for r in records] == [rec["id"]]
    out = c.post(API + "/repairs/%s/restore" % rec["id"]).json()["data"]
    assert (
        sorted(out["restored"]) == ["dns_privacy", "webrtc"] and out["conflicts"] == []
    )
    assert json.loads((prof / "Preferences").read_text()) == {
        "before": True
    }  # 文件也回滚
    prefs = c.get(API + "/environments/%s/prefs" % env_id).json()["data"]
    assert prefs["webrtc"] == "inherit" and prefs["dns_privacy"] is False
    assert (
        c.post(API + "/repairs/%s/restore" % rec["id"]).json()["code"] != 0
    )  # 不能恢复两次


def test_prefs_put_validates(c):
    env_id = _create(c, name="p")["id"]
    assert (
        c.put(
            API + "/environments/%s/prefs" % env_id,
            json={"timezone_mode": "custom", "timezone": "x"},
        ).json()["code"]
        != 0
    )
    assert (
        c.put(
            API + "/environments/%s/prefs" % env_id, json={"geolocation": "teleport"}
        ).json()["code"]
        != 0
    )
    ok = c.put(
        API + "/environments/%s/prefs" % env_id,
        json={"geolocation": "block", "startup_url": "about:blank"},
    ).json()
    assert ok["code"] == 0 and ok["data"]["prefs"]["geolocation"] == "block"


def test_proxy_parse_does_not_echo_password(c):
    r = c.post(
        API + "/proxies/parse", json={"link": "socks5://me:hunter2@9.9.9.9:1080"}
    ).json()["data"]
    assert r == {
        "type": "socks5",
        "host": "9.9.9.9",
        "port": 1080,
        "username": "me",
        "has_password": True,
    }


def _rpc(c, method, params=None, rid=1):
    return c.post(
        "/mcp",
        json={"jsonrpc": "2.0", "id": rid, "method": method, "params": params or {}},
    ).json()


def test_mcp_http_tools(c):
    init = _rpc(c, "initialize", {"protocolVersion": "2025-03-26", "capabilities": {}})
    assert init["result"]["protocolVersion"] == "2025-03-26"
    assert (
        c.post(
            "/mcp", json={"jsonrpc": "2.0", "method": "notifications/initialized"}
        ).status_code
        == 202
    )
    names = {t["name"] for t in _rpc(c, "tools/list")["result"]["tools"]}
    assert {
        "start_profile",
        "check_profile",
        "repair_profile",
        "purge_profile",
        "list_kernels",
    } <= names
    made = _rpc(c, "tools/call", {"name": "create_profile", "arguments": {
        "name": "via-mcp", "tags": ["ai"], "proxy_link": "socks5://u:supersecret@127.0.0.1:1080"}})  # fmt: skip
    text = made["result"]["content"][0]["text"]
    assert (
        made["result"]["isError"] is False and "supersecret" not in text
    )  # 返回给 AI 的密码已脱敏
    env_id = json.loads(text)["data"]["id"]
    listed = json.loads(
        _rpc(c, "tools/call", {"name": "list_profiles", "arguments": {"tag": "ai"}})[
            "result"
        ]["content"][0]["text"]
    )
    assert [e["id"] for e in listed["data"]["list"]] == [env_id]
    refused = _rpc(
        c,
        "tools/call",
        {"name": "purge_profile", "arguments": {"env_id": env_id, "confirm": False}},
    )
    assert refused["result"]["isError"] is True
    bad = _rpc(
        c,
        "tools/call",
        {"name": "stop_profile", "arguments": {"env_id": env_id, "rm": "-rf"}},
    )
    assert bad["result"]["isError"] is True  # 未声明参数被拒
    assert _rpc(c, "nope")["error"]["code"] == -32601
