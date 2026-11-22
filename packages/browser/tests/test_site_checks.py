"""Checker runs per environment: refusals, sites capture plus score, audit report, state across restarts.

Agent Note: .agents/notes/implemented/feature/2026-11-22-console-site-checks.md
"""

from __future__ import annotations

import asyncio
import json

import hxbr_testenv
from hx_browser import site_checks, store
from hx_browser.models import Environment, Fingerprint

API = "/api/v1"
RESULT = 'RESULT {"score": 88.5, "verdict": "pass", "rows": [{"siteId": "sannysoft", "name": "Sannysoft", ' \
         '"score": 100, "verdict": "pass", "headline": "20 / 0"}]}'


def test_refusals(tmp_path):
    hxbr_testenv.isolate(tmp_path)
    blink = store.get_store().add_env(Environment(id="b1", name="b"))
    webkit = store.get_store().add_env(Environment(id="w1", name="w", fingerprint=Fingerprint(kernel="webkit")))
    assert "未运行" in site_checks.refusal(blink.id, "sites")[1]
    assert "Chromium" in site_checks.refusal(webkit.id, "audit")[1]
    assert "未知" in site_checks.refusal(blink.id, "nope")[1]
    assert "不存在" in site_checks.refusal("missing", "sites")[1]


def test_parse_result_takes_last_line():
    assert site_checks.parse_result("x\n" + RESULT + "\n")["score"] == 88.5
    assert site_checks.parse_result("no result") is None


def test_sites_run_stores_scores(tmp_path, monkeypatch):
    hxbr_testenv.isolate(tmp_path)
    calls = []

    async def fake_exec(argv, cwd, timeout):
        calls.append(argv)
        if "--out" in argv:
            open(argv[argv.index("--out") + 1], "w").write("{}")
            return 0, "wrote 9 visits"
        return 0, "  88.5 pass\n" + RESULT

    monkeypatch.setattr(site_checks, "_exec", fake_exec)
    site_checks.results_dir().mkdir(parents=True, exist_ok=True)
    asyncio.run(site_checks._run("e1", "sites", "http://127.0.0.1:43000", ["python"]))
    got = site_checks.read("e1", "sites")
    assert got["state"] == "done" and got["score"] == 88.5 and got["rows"][0]["name"] == "Sannysoft"
    assert calls[0][-4:] == ["--cdp", "http://127.0.0.1:43000", "--out", calls[0][-1]]


def test_audit_run_reads_its_report(tmp_path, monkeypatch):
    hxbr_testenv.isolate(tmp_path)
    audit = tmp_path / "audit"
    (audit / "reports").mkdir(parents=True)
    monkeypatch.setenv("HX_FP_AUDIT_DIR", str(audit))

    async def fake_exec(argv, cwd, timeout):
        label = argv[argv.index("--label") + 1]
        rep = {"verdict": {"level": "yellow", "signals": {"tz": "UTC"},
                           "findings": [{"level": "yellow", "rule": "R4", "detail": "tz", "fix": "align"}]}}
        (audit / "reports" / ("20261122-000000-" + label + ".json")).write_text(json.dumps(rep))
        return 1, ""

    monkeypatch.setattr(site_checks, "_exec", fake_exec)
    asyncio.run(site_checks._run("e2", "audit", "http://127.0.0.1:43001", ["python"]))
    got = site_checks.read("e2", "audit")
    assert got["state"] == "done" and got["verdict"] == "yellow" and got["rows"][0]["fix"] == "align"


def test_running_without_task_reads_as_interrupted(tmp_path):
    hxbr_testenv.isolate(tmp_path)
    site_checks.write("e3", "sites", {"kind": "sites", "state": "running"})
    assert site_checks.read("e3", "sites")["state"] == "failed"


def test_api_idle_and_refusal(tmp_path):
    with hxbr_testenv.client(tmp_path) as c:
        env = c.post(API + "/environments", json={"name": "x"}).json()["data"]
        data = c.get(API + "/environments/" + env["id"] + "/site-checks").json()["data"]
        assert data["sites"]["state"] == "idle" and data["audit"]["state"] == "idle"
        r = c.post(API + "/environments/" + env["id"] + "/site-checks/sites").json()
        assert r["code"] != 0 and "未运行" in r["msg"]
