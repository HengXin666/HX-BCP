"""hxcp 就绪与按环境串行。日志只认本次启动之后的「已挂载」。"""

from __future__ import annotations

import subprocess
import threading
import time
from pathlib import Path

_GUARD = threading.Lock()
_LOCKS: dict[str, threading.RLock] = {}


def env_lock(env_id: str) -> threading.RLock:
    with _GUARD:
        lock = _LOCKS.get(env_id)
        if lock is None:
            lock = threading.RLock()
            _LOCKS[env_id] = lock
        return lock


def ready_from_log(
    proc: subprocess.Popen, log_path: Path, offset: int, timeout: float
) -> None:
    deadline = time.monotonic() + timeout
    while True:
        code = proc.poll()
        if code is not None:
            raise RuntimeError("hxcp 退出码 %s" % code)
        try:
            with log_path.open("rb") as fh:
                fh.seek(offset)
                chunk = fh.read().decode("utf-8", "replace")
        except OSError:
            chunk = ""
        if "已挂载" in chunk:
            return
        if time.monotonic() >= deadline:
            raise RuntimeError("hxcp 未在时限内挂上 CDP")
        time.sleep(0.05)
