"""Xvfb 虚拟屏回落 —— 探不到用户桌面时的兜底 (2026-10-13)。

Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md

⚠️ 这个屏**用户看不见**。调用方 (desktop.resolve_display) 必须把这件事显式
报给用户 (visible=false + reason), 否则表现是「点了没反应」, 而窗口其实开在屏幕外。

## 为什么必须加锁 (实测 2026-10-13)

三个环境**并发** start 时会同时进来挑 display 号。不加锁时它们会挑到同一个号,
后起的 Xvfb 因 socket 已存在而启动失败 —— 而失败是静默的 (stderr 进了 DEVNULL),
表现为「环境起来了但窗口断言恒 false」, 极难查。锁把「挑号 + 等 socket」做成原子段。

## display 号段

从 70 起 (与 gh-pool 的 app/window.py 同段)。两边都用「socket 文件不存在」判空闲,
所以只要不重叠就互不冲突; 本服务与 gh-pool 的 Xvfb 各自独立, 共用段位也不会撞。
"""

from __future__ import annotations

import subprocess
import threading
import time

from . import xenv

TOOL_XVFB = "Xvfb"
XVFB_BASE = 70
XVFB_SPAN = 40
XVFB_SCREEN = "1600x900x24"
XVFB_WAIT_S = 10.0
XVFB_POLL_S = 0.25

_LOCK = threading.Lock()
# env_id -> Xvfb 进程。必须记着: 环境停止时要把它一起收掉, 否则屏幕外留垃圾进程。
PROCS: dict[str, subprocess.Popen] = {}


def pick_display() -> int:
    """挑一个没被占的 display 号。只看 socket 文件 —— 这里只是**挑号**,
    「这个屏能不能连」是 desktop.detect_user_desktop 的事 (那边才是真值判据)。"""
    for n in range(XVFB_BASE, XVFB_BASE + XVFB_SPAN):
        if not xenv.socket_path(n).exists():
            return n
    return XVFB_BASE


def start() -> tuple[str, subprocess.Popen | None]:
    """起一个 Xvfb。返回 (display, proc); 失败返回 ("", None)。

    proc 可能非 None 而 display 为空 (起来但 socket 没出现) —— 调用方按
    display 为空判失败, 并自行 terminate 这个 proc。
    """
    with _LOCK:
        n = pick_display()
        try:
            proc: subprocess.Popen | None = subprocess.Popen(
                [TOOL_XVFB, ":%d" % n, "-screen", "0", XVFB_SCREEN, "-nolisten", "tcp"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                stdin=subprocess.DEVNULL,
                start_new_session=True,
            )
        except Exception:  # noqa: BLE001 — 没装 Xvfb 也要让上层明说, 不在这里崩
            return "", None
        deadline = time.time() + XVFB_WAIT_S
        while time.time() < deadline:
            if xenv.socket_path(n).exists():
                return ":%d" % n, proc
            time.sleep(XVFB_POLL_S)
        return "", proc


def release(env_id: str) -> None:
    """回收该环境的 Xvfb。必须与浏览器一起走, 否则留下屏幕外垃圾进程。"""
    proc = PROCS.pop(env_id, None)
    if proc is None:
        return
    try:
        proc.terminate()
        proc.wait(timeout=5)
    except Exception:  # noqa: BLE001
        try:
            proc.kill()
        except Exception:  # noqa: BLE001
            pass
