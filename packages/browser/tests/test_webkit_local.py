"""Local WebKit kernel: listed in the webkit family, detection from the Playwright cache, runner argv, stop.

Agent Note: .agents/notes/implemented/feature/2026-11-22-webkit-local-kernel.md
"""

from __future__ import annotations

import subprocess
import sys

from hx_browser import external, kernels, webkit_local
from hx_browser.models import Environment, Fingerprint, Settings


def fake_build(tmp_path, monkeypatch):
    root = tmp_path / "ms-playwright"
    (root / "webkit-2388").mkdir(parents=True)
    exe = root / "webkit-2388" / "pw_run.sh"
    exe.write_text("#!/bin/sh\n")
    monkeypatch.setenv("PLAYWRIGHT_BROWSERS_PATH", str(root))
    monkeypatch.setattr(webkit_local, "has_playwright", lambda: True)
    return exe


def test_row_is_webkit_runner_without_cdp(tmp_path, monkeypatch):
    exe = fake_build(tmp_path, monkeypatch)
    env = Environment(fingerprint=Fingerprint(kernel="webkit"))
    assert kernels.family_of(env) == kernels.WEBKIT
    row = next(r for r in kernels.list_kernels(Settings()) if r["id"] == "webkit")
    assert row["mode"] == "runner" and row["available"] and row["source"] == "detected"
    assert row["path"] == str(exe) and row["features"]["cdp"] is False and row["features"]["proxy"] is True
    assert kernels.resolve_executable(env, Settings()) == str(exe)


def test_missing_build_reports_requirement(tmp_path, monkeypatch):
    monkeypatch.setenv("PLAYWRIGHT_BROWSERS_PATH", str(tmp_path / "none"))
    row = next(r for r in kernels.list_kernels(Settings()) if r["id"] == "webkit")
    assert not row["available"] and "playwright" in row["note"]
    assert webkit_local.probe(Settings())["ok"] is False


def test_configured_path_wins(tmp_path, monkeypatch):
    fake_build(tmp_path, monkeypatch)
    mine = tmp_path / "my_pw_run.sh"
    mine.write_text("#!/bin/sh\n")
    s = Settings(kernel_paths={"webkit": str(mine)})
    row = next(r for r in kernels.list_kernels(s) if r["id"] == "webkit")
    assert row["source"] == "configured" and row["path"] == str(mine)


def test_runner_argv_has_no_credentials(tmp_path):
    argv = webkit_local.runner_argv(tmp_path, "https://example.com", 41001, "en-US", "Asia/Tokyo", True)
    assert argv[1:3] == ["-m", "hx_browser.webkit_local"]
    assert "--proxy" in argv and "http://127.0.0.1:41001" in argv
    assert argv[argv.index("--tz") + 1] == "Asia/Tokyo" and "--headless" in argv
    assert not any("@" in a for a in argv)


def test_external_dispatch_and_stop():
    assert external.handles("webkit") and external.handles("safarier") and not external.handles("chrome")
    proc = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"], start_new_session=True)
    assert webkit_local.stop(proc.pid)
    assert proc.wait(timeout=5) != 0
    assert webkit_local.stop(0) is False
