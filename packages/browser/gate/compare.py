"""One cell of the site-gate comparison.

Stock cells launch Google Chrome or Firefox directly. Those are the control:
headed and headless, on the virtual desktop the workflow starts.

hx cells start the hx-browser service and open one kernel, headed or headless.
See .agents/notes/implemented/feature/2026-10-18-hxbrowser-site-gate.md.

    python compare.py --group stock --browser chrome --headed --out cell.json
    python compare.py --group hx --kernel cloak --headless --out cell.json
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

from probe import capture_pages

API = "http://127.0.0.1:8325/api/v1"
GECKO = {"firefox", "librewolf"}
HERE = Path(__file__).resolve().parent


def hx_root() -> Path:
    override = os.environ.get("HX_BROWSER_ROOT", "").strip()
    return Path(override) if override else HERE.parent


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--group", choices=("stock", "hx"), required=True)
    parser.add_argument("--browser", choices=("chrome", "firefox"), default="chrome")
    parser.add_argument("--kernel", default="")
    parser.add_argument("--headed", action="store_true")
    parser.add_argument("--headless", action="store_true")
    parser.add_argument("--label", default="")
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    headed = args.headed and not args.headless
    label = args.label or _default_label(args.group, args.browser, args.kernel, headed)
    try:
        if args.group == "stock":
            payload = _stock(args.browser, headed, label)
        else:
            payload = _hx(args.kernel, headed, label)
    except Exception as exc:  # noqa: BLE001 — 一个格子失败也要留下未测，不把整列弄丢
        payload = _missing(label, str(exc))
    Path(args.out).write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"wrote {args.out} available={payload.get('available')}")


def _default_label(group: str, browser: str, kernel: str, headed: bool) -> str:
    mode = "有头" if headed else "无头"
    if group == "stock":
        name = "本机谷歌" if browser == "chrome" else "本机火狐"
        return f"{name} {mode}"
    return f"hx {kernel} {mode}"


def _stock(browser: str, headed: bool, label: str) -> dict:
    exe = _which(("google-chrome-stable", "google-chrome") if browser == "chrome" else ("firefox", "firefox-esr"))
    if not exe:
        return _missing(label, f"找不到{browser}")
    driver = "chrome"
    from playwright.sync_api import sync_playwright

    with sync_playwright() as playwright:
        if browser == "chrome":
            launched = playwright.chromium.launch(
                executable_path=exe,
                headless=not headed,
                args=["--no-sandbox", "--disable-dev-shm-usage"],
            )
        else:
            launched, driver = _launch_firefox(playwright, exe, headed)
        context = launched.new_context(viewport={"width": 1280, "height": 900})
        payload = capture_pages(context, label)
        launched.close()
    payload["available"] = True
    payload["group"] = "stock"
    payload["headed"] = headed
    payload["requestedHeaded"] = headed
    payload["driver"] = driver
    if driver == "playwright-firefox":
        payload["note"] = "系统 Firefox 没法被 Playwright 接上，这一格用的是 Playwright 自带的 Firefox。"
    return payload


def _launch_firefox(playwright, exe: str, headed: bool):
    try:
        return playwright.firefox.launch(executable_path=exe, headless=not headed), "system-firefox"
    except Exception:
        return playwright.firefox.launch(headless=not headed), "playwright-firefox"


def _hx(kernel: str, headed: bool, label: str) -> dict:
    if not kernel:
        return _missing(label, "没有 kernel")
    root = hx_root()
    if not (root / "run.py").is_file():
        return _missing(label, "没有 HX-Browser 源码。公开工作流复用 HX-Workflows 的 secret HYGIENE_TOKEN 拉私有仓。")
    proc = _serve(root)
    try:
        if proc is None:
            return _missing(label, "hx-browser 没起来")
        created = _http("POST", "/environments", {
            "name": label,
            "fingerprint": {
                "kernel": kernel,
                "browser": "flower" if kernel in GECKO else "sun",
                "headless": not headed,
            },
        })
        if created.get("code") != 0:
            return _missing(label, json.dumps(created, ensure_ascii=False)[:500])
        env_id = created["data"]["id"]
        try:
            started = _http("POST", f"/environments/{env_id}/start")
            if started.get("code") != 0:
                return _gap(label, json.dumps(started, ensure_ascii=False)[:500])
            runtime = (started.get("data") or {}).get("runtime") or {}
            if kernel in GECKO:
                return _gap(label, "firefox/librewolf 不是 Chromium CDP，这一格还没接上，不是没安装。", "unmeasured")
            port = runtime.get("debug_port")
            cdp = runtime.get("cdp_http") or (f"http://127.0.0.1:{port}" if port else "")
            if not cdp:
                return _gap(label, "启动了，但响应里没有调试地址", "unmeasured")
            from playwright.sync_api import sync_playwright

            with sync_playwright() as playwright:
                browser = playwright.chromium.connect_over_cdp(cdp)
                context = browser.contexts[0] if browser.contexts else browser.new_context()
                payload = capture_pages(context, label)
            payload["available"] = True
            payload["group"] = "hx"
            payload["kernel"] = kernel
            payload["headed"] = bool(runtime.get("display"))
            payload["requestedHeaded"] = headed
            payload["visible"] = bool(runtime.get("visible"))
            return payload
        finally:
            for method, path in (("POST", f"/environments/{env_id}/stop"), ("DELETE", f"/environments/{env_id}")):
                try:
                    _http(method, path)
                except Exception:
                    pass
    finally:
        if proc is not None:
            proc.terminate()


def _serve(root: Path) -> subprocess.Popen | None:
    env = os.environ.copy()
    env["PYTHONPATH"] = str(root / "src")
    proc = subprocess.Popen(
        [sys.executable, "run.py", "--host", "127.0.0.1", "--port", "8325"],
        cwd=root,
        env=env,
    )
    deadline = time.time() + 30
    while time.time() < deadline:
        try:
            got = _http("GET", "/status")
            if got.get("code") == 0:
                return proc
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            time.sleep(0.4)
    proc.terminate()
    return None


def _http(method: str, path: str, body: dict | None = None) -> dict:
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        API + path, data=data, method=method, headers={"Content-Type": "application/json"}
    )
    with urllib.request.urlopen(req, timeout=60) as response:
        return json.loads(response.read().decode())


def _which(names: tuple[str, ...]) -> str:
    for name in names:
        found = shutil.which(name)
        if found:
            return found
    return ""


def _gap(label: str, error: str, status: str = "missing") -> dict:
    return {
        "capturedAt": "",
        "browser": label,
        "available": False,
        "status": status,
        "error": error,
        "visits": [],
        "note": "没装上，不是 0 分。" if status == "missing" else "没测成，不是 0 分。",
    }


_missing = _gap


if __name__ == "__main__":
    main()
