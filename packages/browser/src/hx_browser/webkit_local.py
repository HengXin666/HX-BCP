"""WebKit on this Linux host: the Playwright build of WebKit (GTK/WPE port), webkit family, macOS not required.

Agent Note: .agents/notes/implemented/feature/2026-11-22-webkit-local-kernel.md

Safarier needs macOS only because it embeds Apple's WKWebView system framework. The WebKit engine
itself is open source and Playwright ships a Linux build of it. HX-Browser runs that build through a
small runner process (python -m hx_browser.webkit_local) that holds one persistent context per
environment, so the profile dir survives restarts and stop is SIGTERM to the runner process group.
Proxy goes through relay.py (local entry without credentials); language and timezone are the effective
values from profile_rules, the same inputs every other kernel reads. There is no CDP: live view, cookie
injection, CDP probes and the sandbox do not apply. Pages see Linux WebKit, not macOS Safari.
"""

from __future__ import annotations

import argparse
import asyncio
import importlib.util
import os
import signal
import subprocess
import sys
from pathlib import Path

KERNEL_ID = "webkit"
READY_FILE = ".hxb-webkit-ready"
LOG_FILE = "webkit-runner.log"
READY_TIMEOUT_S = 30
FEATURES = {"cdp": False, "live_view": False, "cookies": False, "sandbox": False, "fingerprint_check": False,
            "proxy": True, "agent_cli": False}
HOW = "Playwright WebKit 构建 + 本机 runner 进程"
REQUIREMENT = "需要 playwright 和它的 WebKit 构建: uv run --with playwright playwright install --with-deps webkit"


def browsers_root() -> Path:
    """Where Playwright keeps its browser builds, PLAYWRIGHT_BROWSERS_PATH wins over the default cache."""
    env = os.environ.get("PLAYWRIGHT_BROWSERS_PATH", "")
    if env and env != "0":
        return Path(env)
    return Path.home() / ".cache" / "ms-playwright"


def has_playwright() -> bool:
    return importlib.util.find_spec("playwright") is not None


def detect() -> str:
    """Newest installed Playwright WebKit launcher (pw_run.sh), empty when playwright or the build is missing."""
    if not has_playwright():
        return ""
    found = sorted(browsers_root().glob("webkit-*/pw_run.sh"))
    return str(found[-1]) if found else ""


def executable(settings) -> str:
    """settings.kernel_paths["webkit"] when it exists, else the detected build."""
    configured = ((getattr(settings, "kernel_paths", {}) or {}).get(KERNEL_ID, "") or "").strip()
    if configured and Path(configured).exists():
        return configured
    return detect()


def available(settings) -> bool:
    return has_playwright() and bool(executable(settings))


def probe(settings) -> dict:
    """Kernels page check: playwright importable and a WebKit build found. Nothing is launched."""
    if not has_playwright():
        return {"ok": False, "error": "没有安装 playwright. " + REQUIREMENT}
    exe = executable(settings)
    if not exe:
        return {"ok": False, "error": "没有找到 WebKit 构建. " + REQUIREMENT}
    return {"ok": True, "version": Path(exe).parent.name}


def runner_argv(profile_dir: Path, url: str, proxy_port: int, locale: str, tz: str, headless: bool,
                exe: str = "") -> list[str]:
    argv = [sys.executable, "-m", "hx_browser.webkit_local", "--profile", str(profile_dir), "--url", url]
    if proxy_port:
        argv += ["--proxy", "http://127.0.0.1:%d" % proxy_port]
    if locale:
        argv += ["--locale", locale]
    if tz:
        argv += ["--tz", tz]
    if headless:
        argv.append("--headless")
    if exe:
        argv += ["--exe", exe]
    return argv


def _child_env(plan) -> dict:
    env = dict(os.environ)
    if plan is not None and getattr(plan, "display", ""):
        env["DISPLAY"] = plan.display
        if getattr(plan, "xauth", ""):
            env["XAUTHORITY"] = plan.xauth
    return env


async def _wait_ready(proc: subprocess.Popen, profile_dir: Path) -> None:
    ready = profile_dir / READY_FILE
    for _ in range(READY_TIMEOUT_S * 4):
        if ready.exists():
            return
        if proc.poll() is not None:
            break
        await asyncio.sleep(0.25)
    log = (profile_dir / LOG_FILE).read_text(errors="replace")[-800:] if (profile_dir / LOG_FILE).exists() else ""
    if proc.poll() is None:
        stop(proc.pid)
    raise RuntimeError("WebKit runner 没有就绪 (exit %s): %s" % (proc.poll(), log.strip() or "无日志"))


async def start_runtime(env, settings, profile_dir: Path, plan):
    """Start the runner for env with its effective language, timezone and relay proxy, return the RuntimeInfo."""
    from . import netcheck, relay
    from .launch_hooks import needs_ip_region
    from .models import RuntimeInfo, now_ms
    from .profile_rules import effective_languages, effective_timezone
    from .proxy_link import upstream_of

    if not available(settings):
        raise RuntimeError("WebKit 内核不可用. " + REQUIREMENT)
    region = await netcheck.lookup_region(env) if needs_ip_region(env) else {}
    langs = effective_languages(env, region.get("languages", ""))
    tz = effective_timezone(env, region.get("timezone", ""))
    up = upstream_of(env.proxy)
    port = await relay.ensure(env.id, up) if up is not None else 0
    profile_dir.mkdir(parents=True, exist_ok=True)
    (profile_dir / READY_FILE).unlink(missing_ok=True)
    configured = ((getattr(settings, "kernel_paths", {}) or {}).get(KERNEL_ID, "") or "").strip()
    url = (env.launch.urls[:1] or [settings.default_homepage or "about:blank"])[0]
    headless = bool(getattr(plan, "headless", False))
    argv = runner_argv(profile_dir, url, port, (langs or "").split(",")[0].strip(), tz or "", headless,
                       configured if configured and Path(configured).exists() else "")
    with open(profile_dir / LOG_FILE, "wb") as log:
        proc = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT, env=_child_env(plan),
                                start_new_session=True)
    await _wait_ready(proc, profile_dir)
    display = getattr(plan, "display", "") or ""
    return RuntimeInfo(pid=proc.pid, executable=executable(settings), profile_dir=str(profile_dir),
                       started_at=now_ms(), kernel=KERNEL_ID, visible=not headless and bool(display),
                       display=display, display_source="desktop" if display and not headless else "headless",
                       display_source_detail="webkit-runner")


def stop(pid: int) -> bool:
    """SIGTERM the runner process group; the runner closes its context and WebKit exits with it."""
    if not pid:
        return False
    try:
        os.killpg(pid, signal.SIGTERM)
    except (ProcessLookupError, PermissionError):
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            return False
    return True


def main(argv: list[str] | None = None) -> int:
    """Runner: one persistent WebKit context, alive until all pages close or SIGTERM arrives."""
    ap = argparse.ArgumentParser(description="HX-Browser WebKit runner")
    ap.add_argument("--profile", required=True)
    ap.add_argument("--url", default="about:blank")
    ap.add_argument("--proxy", default="")
    ap.add_argument("--locale", default="")
    ap.add_argument("--tz", default="")
    ap.add_argument("--headless", action="store_true")
    ap.add_argument("--exe", default="")
    a = ap.parse_args(argv)
    stopping = []
    signal.signal(signal.SIGTERM, lambda *_: stopping.append(1))
    from playwright.sync_api import sync_playwright

    opts: dict = {"headless": a.headless, "no_viewport": not a.headless}
    if a.proxy:
        opts["proxy"] = {"server": a.proxy}
    if a.locale:
        opts["locale"] = a.locale
    if a.tz:
        opts["timezone_id"] = a.tz
    if a.exe:
        opts["executable_path"] = a.exe
    with sync_playwright() as pw:
        ctx = pw.webkit.launch_persistent_context(a.profile, **opts)
        page = ctx.pages[0] if ctx.pages else ctx.new_page()
        try:
            page.goto(a.url, timeout=20000)
        except Exception as e:  # noqa: BLE001 a dead homepage must not kill the window
            print("goto failed: %s" % e, flush=True)
        Path(a.profile, READY_FILE).write_text(str(os.getpid()))
        while not stopping and ctx.pages:
            try:
                ctx.pages[0].wait_for_timeout(500)
            except Exception:  # noqa: BLE001 the page closed while waiting, recheck
                continue
        ctx.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
