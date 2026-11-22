"""Kernel management: list with source, features and usage, path override, default, safarier as webkit.

Agent Note: .agents/notes/implemented/feature/2026-11-22-safarier-webkit-kernel.md
"""

from __future__ import annotations

import lane
import pytest

import hxbr_testenv
from hx_browser import kernels, safarier
from hx_browser.models import Environment, Fingerprint, Settings

API = "/api/v1"


@pytest.fixture
def human(tmp_path, monkeypatch):
    file = tmp_path / "human.token"
    monkeypatch.setattr(lane, "TOKEN_FILE", file)
    # x-api-key emptied: the test client sends the agent key by default, and a key pins the agent lane
    return {lane.HEADER_LANE: "human", lane.HEADER_HUMAN_TOKEN: lane.issue_human_token(file), "x-api-key": ""}


def test_safarier_is_webkit_external():
    env = Environment(fingerprint=Fingerprint(kernel="safarier"))
    assert kernels.family_of(env) == kernels.WEBKIT
    row = next(r for r in kernels.list_kernels(Settings()) if r["id"] == "safarier")
    assert row["mode"] == "external" and row["features"]["cdp"] is False and "how" in row


def test_safarier_is_macos_only(monkeypatch):
    monkeypatch.setattr(kernels.sys, "platform", "linux")
    rows = {r["id"]: r for r in kernels.list_kernels(Settings())}
    assert rows["safarier"]["platforms"] == ["darwin"] and rows["safarier"]["host_supported"] is False
    assert rows["chromium"]["platforms"] == [] and rows["chromium"]["host_supported"] is True  # browser-gate: allow-legacy 内核 id
    monkeypatch.setattr(kernels.sys, "platform", "darwin")
    assert next(r for r in kernels.list_kernels(Settings()) if r["id"] == "safarier")["host_supported"] is True


def test_safarier_command_line_and_table():
    s = Settings(kernel_paths={"safarier": "ssh my-mac '/Applications/Safarier.app/Contents/MacOS/Safarier'"})
    cmd = safarier.command(s)
    assert cmd[:2] == ["ssh", "my-mac"] and safarier.is_remote(cmd)
    text = "ID                                    NAME  MODE        STATE    TABS\n" \
           "0B6F1C2A-1111-2222-3333-444455556666  shop  persistent  running  2 tabs\n"
    rows = safarier.parse_table(text)
    assert rows[0]["STATE"] == "running" and rows[0]["NAME"] == "shop"


def test_kernel_api_paths_default_and_lane(tmp_path, human):
    with hxbr_testenv.client(tmp_path) as c:
        data = c.get(API + "/kernels").json()["data"]
        assert any(r["id"] == "safarier" for r in data["kernels"]) and "inherit" in data
        assert c.put(API + "/kernels/chrome/path", json={"path": "/x"}).status_code == 403
        r = c.put(API + "/kernels/chrome/path", json={"path": "/nope/chrome"}, headers=human).json()["data"]
        assert r["source"] == "configured" and r["available"] is False
        assert c.put(API + "/kernels/chrome/default", headers=human).json()["code"] != 0
        assert c.put(API + "/kernels/bogus/path", json={"path": ""}, headers=human).status_code == 404
        probe = c.post(API + "/kernels/chrome/probe", headers=human).json()["data"]
        assert probe["ok"] is False
        r = c.put(API + "/kernels/chrome/path", json={"path": ""}, headers=human).json()["data"]
        assert r["configured"] == ""


FAKE = """#!/usr/bin/env python3
import sys, pathlib
log = pathlib.Path(__file__).with_suffix(".log")
log.open("a").write(" ".join(sys.argv[1:]) + "\\n")
a = sys.argv[1:]
if a[:2] == ["profile", "create"]:
    print("0B6F1C2A-1111-2222-3333-444455556666")
elif a[:2] == ["proxy", "add"]:
    print("AAAAAAAA-1111-2222-3333-444455556666")
elif a[:2] == ["profile", "list"]:
    print("ID                                    MODE        STATE  NAME")
    print("0B6F1C2A-1111-2222-3333-444455556666  persistent  -      x")
elif a[:1] == ["launch"]:
    print("started x, pid 999999, log /tmp/x")
"""


def test_safarier_start_creates_profile_once(tmp_path):
    import asyncio

    from hx_browser import store

    hxbr_testenv.isolate(tmp_path)
    exe = tmp_path / "fake-safarier"
    exe.write_text(FAKE)
    exe.chmod(0o755)
    s = Settings(kernel_paths={"safarier": str(exe)})
    env = store.get_store().add_env(Environment(name="x", fingerprint=Fingerprint(kernel="safarier")))
    env.proxy.type, env.proxy.host, env.proxy.port = "socks5", "10.0.0.1", 1080
    info = asyncio.run(safarier.start_runtime(env, s))
    assert info.kernel == "safarier" and info.remote_pid == 999999 and info.pid == 999999
    assert store.get_store().get_env(env.id).fingerprint.external_profile.startswith("0B6F1C2A")
    asyncio.run(safarier.start_runtime(env, s))
    calls = exe.with_suffix(".log").read_text().splitlines()
    assert sum(c.startswith("profile create") for c in calls) == 1
    assert any("--proxy AAAAAAAA" in c for c in calls if c.startswith("profile create"))
