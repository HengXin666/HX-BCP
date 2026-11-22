"""Safarier (macOS native WebKit, github.com/Cd1s/safarier) as an external engine of the webkit family.

Agent Note: .agents/notes/implemented/feature/2026-11-22-safarier-webkit-kernel.md

Safarier is a whole app with its own profile store, proxy stack and runner processes, so HX-Browser
does not spawn a binary with a profile dir. It drives the Safarier CLI instead:
    profile create / update / delete   one Safarier profile per HX environment (Fingerprint.external_profile)
    proxy add                          the environment proxy, only socks5 and http map
    launch PROFILE_ID --url --background   start, prints the runner pid
    agent spaces                       running state
The command comes from settings.kernel_paths["safarier"], split like a shell line, so it can be the local
executable or a remote one ("ssh my-mac /Applications/Safarier.app/Contents/MacOS/Safarier"). Without a
configured command it is detected only on macOS. There is no CDP: live view, cookie injection, CDP
probes and the Linux sandbox do not apply, list_kernels reports that through FEATURES.
"""

from __future__ import annotations

import asyncio
import os
import re
import shlex
import signal
import sys
from pathlib import Path

KERNEL_ID = "safarier"
APP_EXE = "/Applications/Safarier.app/Contents/MacOS/Safarier"
TIMEOUT_S = 30
UUID_RE = re.compile(r"[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}")
PID_RE = re.compile(r"pid\s+(\d+)")
PROXY_KIND = {"socks5": "socks5", "http": "httpConnect", "https": "httpConnect"}
FEATURES = {"cdp": False, "live_view": False, "cookies": False, "sandbox": False, "fingerprint_check": False,
            "proxy": True, "agent_cli": True}
REQUIREMENT = "需要 macOS 14+ 上安装 Safarier; Linux 宿主请在内核路径里填远程命令, 如 ssh my-mac " + APP_EXE


class SafarierError(RuntimeError):
    pass


def command(settings) -> list[str]:
    """Command prefix that runs the Safarier CLI, empty when none is configured or found."""
    raw = ((getattr(settings, "kernel_paths", {}) or {}).get(KERNEL_ID, "") or "").strip()
    if raw:
        return shlex.split(raw)
    if sys.platform == "darwin" and Path(APP_EXE).exists():
        return [APP_EXE]
    return []


def is_remote(cmd: list[str]) -> bool:
    """True when the executable is reached through a wrapper (ssh and the like), not run on this host."""
    return len(cmd) > 1


def available(settings) -> bool:
    """A usable command: local executable that exists, or a wrapper whose first word is on PATH."""
    cmd = command(settings)
    if not cmd:
        return False
    if is_remote(cmd):
        from shutil import which

        return bool(which(cmd[0]))
    return Path(cmd[0]).exists()


async def run(settings, *args: str, stdin: str | None = None) -> str:
    """Run one CLI call, return stdout; a non zero exit raises SafarierError with stderr."""
    cmd = command(settings)
    if not cmd:
        raise SafarierError("Safarier 未配置. " + REQUIREMENT)
    argv = cmd + [shlex.quote(a) if is_remote(cmd) else a for a in args]
    proc = await asyncio.create_subprocess_exec(
        *argv, stdin=asyncio.subprocess.PIPE if stdin is not None else None,
        stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
    try:
        out, err = await asyncio.wait_for(proc.communicate(stdin.encode() if stdin else None), TIMEOUT_S)
    except asyncio.TimeoutError as e:
        proc.kill()
        raise SafarierError("Safarier 命令超时: " + " ".join(args[:2])) from e
    if proc.returncode != 0:
        raise SafarierError("Safarier %s 失败 (%s): %s" % (" ".join(args[:2]), proc.returncode,
                                                          err.decode(errors="replace").strip()))
    return out.decode(errors="replace")


def parse_table(text: str) -> list[dict]:
    """Rows of a Safarier printTable output, columns split on two or more spaces, keys from the header."""
    lines = [ln for ln in text.splitlines() if ln.strip()]
    if not lines or not lines[0].startswith("ID"):
        return []
    head = re.split(r"\s{2,}", lines[0].strip())
    return [dict(zip(head, re.split(r"\s{2,}", ln.strip()))) for ln in lines[1:]]


async def probe(settings) -> dict:
    """Reachability check for the kernels page: profile count and running count, or the error."""
    try:
        rows = parse_table(await run(settings, "agent", "spaces"))
    except (SafarierError, OSError) as e:
        return {"ok": False, "error": str(e)}
    return {"ok": True, "profiles": len(rows), "running": sum(r.get("STATE") == "running" for r in rows)}


async def _proxy_id(settings, env) -> str:
    p = env.proxy
    kind = PROXY_KIND.get(p.type, "")
    if not kind or not p.host or not p.port:
        return ""
    args = ["proxy", "add", "--name", "hxb-" + env.id[:8], "--kind", kind, "--host", p.host, "--port", str(p.port)]
    if p.username:
        args += ["--username", p.username, "--password-stdin"]
    out = await run(settings, *args, stdin=p.password if p.username else None)
    m = UUID_RE.search(out)
    return m.group(0) if m else ""


async def ensure_profile(env, settings) -> str:
    """Safarier profile of env, created on first start; language and timezone follow the proxy exit."""
    known = env.fingerprint.external_profile
    if known:
        rows = parse_table(await run(settings, "profile", "list"))
        if any(r.get("ID", "").lower() == known.lower() for r in rows):
            return known
    args = ["profile", "create", "--name", (env.name or "hxb") + " " + env.id[:8], "--mode", "persistent",
            "--lang", "proxy", "--tz", "proxy"]
    proxy = await _proxy_id(settings, env)
    if proxy:
        args += ["--proxy", proxy]
    m = UUID_RE.search(await run(settings, *args))
    if not m:
        raise SafarierError("Safarier profile create 没有返回 ID")
    env.fingerprint.external_profile = m.group(0)
    return m.group(0)


async def start_runtime(env, settings):
    """Launch env in Safarier in the background, persist its profile id, return the RuntimeInfo to record."""
    from .models import RuntimeInfo, now_ms
    from .store import get_store

    profile = await ensure_profile(env, settings)
    get_store().update_env(env)  # external_profile must survive a restart, or the next start makes a new profile
    url = (env.launch.urls[:1] or [settings.default_homepage or "about:blank"])[0]
    m = PID_RE.search(await run(settings, "launch", profile, "--url", url, "--background"))
    cmd = command(settings)
    pid = int(m.group(1)) if m else 0
    return RuntimeInfo(pid=0 if is_remote(cmd) else pid, remote_pid=pid, executable=" ".join(cmd),
                       profile_dir=profile, started_at=now_ms(), kernel=KERNEL_ID, visible=True,
                       display_source="safarier", display_source_detail="remote" if is_remote(cmd) else "local",
                       isolation="safarier")


async def stop(settings, pid: int) -> bool:
    """Stop the runner: SIGTERM locally, or kill through the same wrapper when remote."""
    if not pid:
        return False
    cmd = command(settings)
    if cmd and is_remote(cmd):
        proc = await asyncio.create_subprocess_exec(*cmd[:-1], "kill", str(pid))
        return await asyncio.wait_for(proc.wait(), TIMEOUT_S) == 0
    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        return False
    return True
