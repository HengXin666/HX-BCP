from __future__ import annotations

import logging
import os
import shutil
import signal
import subprocess
import time
from pathlib import Path

from .config import settings_manager
from .hxcp_ready import env_lock, ready_from_log
from .runtime import RUNTIME, pid_alive, save_runtime

log = logging.getLogger("hx_browser.hxcp_link")

_READY_WAIT = 8.0
_STOP_WAIT = 8.0


class _Mount:
    def __init__(
        self, pid: int, endpoint: str, proc: subprocess.Popen | None, browser_pid: int
    ):
        self.pid = pid
        self.endpoint = endpoint
        self.proc = proc
        self.browser_pid = browser_pid


# 同一环境的启停串行; stdout 写缓存日志, 避免服务重启后管道断开把 hxcp 写死.
# .agents/notes/implemented/bug-fix/2026-10-18-hxcp-mount-survives-restart.md
_RUNNING: dict[str, _Mount] = {}


def _cache_path(env_id: str) -> Path:
    if not env_id or env_id in (".", "..") or any(c in env_id for c in "/\\\x00"):
        raise ValueError("非法 env_id")
    root = (Path(settings_manager.load().data_dir) / "hxcp").resolve()
    return root / env_id


def cache_dir_for(env_id: str) -> Path:
    path = _cache_path(env_id)
    if path.is_symlink():
        raise ValueError("缓存目录是软链接")
    return path


def prepare_cache_dir(env_id: str) -> Path:
    path = cache_dir_for(env_id)
    path.mkdir(parents=True, exist_ok=True)
    if path.is_symlink() or not path.is_dir():
        raise ValueError("缓存目录不是真实目录")
    return path


def _hxcp_cmd(endpoint: str, cache_dir: Path) -> list[str]:
    cache = str(cache_dir)
    tail = ["--cache-dir", cache, "run", "--endpoint", endpoint, "--duration", "0"]
    hxcp = shutil.which("hxcp")
    if hxcp:
        return [hxcp, *tail]
    here = Path(__file__).resolve()
    root = here.parents[4] if len(here.parents) > 4 else Path.cwd()
    toolbox = root / "toolbox" / "hx-capture-proxy"
    if (toolbox / "pyproject.toml").exists():
        return ["uv", "run", "--directory", str(toolbox), "hxcp", *tail]
    raise RuntimeError(
        "找不到 hxcp 可执行文件，也没有 monorepo toolbox/hx-capture-proxy"
    )


def _usable(mount: _Mount) -> bool:
    if mount.proc is not None:
        return mount.proc.poll() is None
    return pid_alive(mount.pid)


def _cmdline(pid: int) -> str:
    try:
        raw = Path("/proc/%d/cmdline" % pid).read_bytes()
    except OSError:
        return ""
    return raw.replace(b"\x00", b" ").decode("utf-8", "replace")


def _is_ours(pid: int, env_id: str) -> bool:
    if pid <= 0 or not pid_alive(pid):
        return False
    try:
        cache = str(_cache_path(env_id))
    except ValueError:
        return False
    cmd = _cmdline(pid)
    return "hxcp" in cmd and cache in cmd


def _remember(env_id: str, pid: int, status: str) -> None:
    rt = RUNTIME.get(env_id)
    if rt is None:
        return
    rt.hxcp_pid = int(pid or 0)
    rt.hxcp_status = status
    save_runtime()


def adopt(env_id: str, pid: int, endpoint: str, browser_pid: int = 0) -> bool:
    if not _is_ours(pid, env_id):
        return False
    current = _RUNNING.get(env_id)
    if current and _usable(current):
        return True
    _RUNNING[env_id] = _Mount(pid, endpoint, None, int(browser_pid or 0))
    return True


def adopt_from_runtime() -> int:
    n = 0
    changed = False
    for eid, rt in list(RUNTIME.items()):
        pid = int(rt.hxcp_pid or 0)
        if pid <= 0:
            continue
        if adopt(eid, pid, rt.cdp_http or "", int(rt.pid or 0)):
            n += 1
        else:
            rt.hxcp_pid = 0
            rt.hxcp_status = "dead"
            changed = True
    if changed:
        save_runtime()
    return n


def _wait_ready(proc: subprocess.Popen, log_path: Path, offset: int) -> None:
    ready_from_log(proc, log_path, offset, _READY_WAIT)


def start(env_id: str, cdp_http: str, browser_pid: int = 0) -> tuple[int, str]:
    with env_lock(env_id):
        return _start_locked(env_id, cdp_http, browser_pid)


def _start_locked(env_id: str, cdp_http: str, browser_pid: int = 0) -> tuple[int, str]:
    existing = _RUNNING.get(env_id)
    if existing and _usable(existing) and existing.endpoint == cdp_http:
        stale = bool(browser_pid) and existing.browser_pid != browser_pid
        if not stale:
            return existing.pid, "already-running"
    if existing:
        stop(env_id)
    proc = None
    log_fh = None
    try:
        cdir = prepare_cache_dir(env_id)
        log_path = cdir / "hxcp.log"
        offset = log_path.stat().st_size if log_path.exists() else 0
        log_fh = log_path.open("ab", buffering=0)
        proc = subprocess.Popen(
            _hxcp_cmd(cdp_http, cdir),
            stdout=log_fh,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        _wait_ready(proc, log_path, offset)
        _RUNNING[env_id] = _Mount(proc.pid, cdp_http, proc, browser_pid)
        _remember(env_id, proc.pid, "running")
        return proc.pid, "running"
    except Exception as e:
        log.warning("hxcp start failed for %s: %s", env_id, e)
        if proc is not None and proc.poll() is None:
            try:
                _signal_group(proc.pid, signal.SIGKILL)
            except OSError:
                pass
        _remember(env_id, 0, "failed")
        return 0, "failed: %s" % e
    finally:
        if log_fh is not None:
            log_fh.close()


def _signal_group(pid: int, sig: int) -> None:
    try:
        os.killpg(pid, sig)
    except ProcessLookupError:
        raise
    except OSError:
        os.kill(pid, sig)


def stop(env_id: str) -> str:
    with env_lock(env_id):
        return _stop_locked(env_id)


def _stop_locked(env_id: str) -> str:
    mount = _RUNNING.pop(env_id, None)
    proc = mount.proc if mount else None
    pid = mount.pid if mount else 0
    if pid <= 0:
        rt = RUNTIME.get(env_id)
        if rt and rt.hxcp_pid:
            pid = int(rt.hxcp_pid)
    if proc is None and pid <= 0:
        _remember(env_id, 0, "absent")
        return "absent"
    if proc is not None and proc.poll() is not None:
        _remember(env_id, 0, "already-gone")
        return "already-gone"
    if proc is None and not _is_ours(pid, env_id):
        _remember(env_id, 0, "already-gone")
        return "already-gone"
    result = "stopped"
    try:
        _signal_group(pid, signal.SIGINT)
        deadline = time.monotonic() + _STOP_WAIT
        while time.monotonic() < deadline:
            if proc is not None and proc.poll() is not None:
                break
            if proc is None and not pid_alive(pid):
                break
            time.sleep(0.05)
        else:
            if pid_alive(pid):
                _signal_group(pid, signal.SIGKILL)
                result = "forced"
                log.warning("hxcp %s 收尾超时, 已强制结束 pid=%s", env_id, pid)
                if proc is not None:
                    try:
                        proc.wait(timeout=1)
                    except subprocess.TimeoutExpired:
                        pass
    except ProcessLookupError:
        result = "already-gone"
    except Exception as e:
        log.warning("hxcp stop %s: %s", env_id, e)
        result = "error: %s" % e
    _remember(env_id, 0, result)
    return result


def stop_all() -> None:
    for env_id in list(_RUNNING):
        stop(env_id)


def status(env_id: str) -> dict:
    if env_id not in _RUNNING:
        rt = RUNTIME.get(env_id)
        pid = int(rt.hxcp_pid or 0) if rt else 0
        browser_pid = int(rt.pid or 0) if rt else 0
        endpoint = (rt.cdp_http if rt else "") or ""
        if pid and not adopt(env_id, pid, endpoint, browser_pid):
            _remember(env_id, 0, "dead")
    mount = _RUNNING.get(env_id)
    if mount is None:
        return {"status": "absent", "pid": 0}
    if not _usable(mount):
        pid = mount.pid
        _RUNNING.pop(env_id, None)
        _remember(env_id, 0, "dead")
        return {"status": "dead", "pid": pid}
    return {"status": "running", "pid": mount.pid, "endpoint": mount.endpoint}


def drop_cache(env_id: str) -> None:
    path = _cache_path(env_id)
    if path.is_symlink() or path.is_file():
        path.unlink()
    elif path.exists():
        shutil.rmtree(path)
    if path.exists():
        raise OSError("缓存目录未删除: %s" % path)


def mount_if_enabled(env, info) -> None:
    if not getattr(env, "hxcp_enabled", False) or not getattr(info, "cdp_http", ""):
        return
    try:
        pid, st = start(env.id, info.cdp_http, int(getattr(info, "pid", 0) or 0))
        info.hxcp_pid = pid
        info.hxcp_status = "failed" if str(st).startswith("failed") else st
    except Exception as e:  # noqa: BLE001
        log.warning("auto hxcp mount failed for %s: %s", getattr(env, "id", ""), e)
        info.hxcp_status = "failed"


from .hxcp_async import amount, astart, astop, astop_all  # noqa: E402

__all__ = ["amount", "astart", "astop", "astop_all", "start", "stop", "status"]
