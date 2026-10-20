"""沙箱副本的显示层隔离 + 窗口系统探测 (X11 / Wayland)。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

## 为什么 X11 要单独处理

X11 协议允许同一 X server 上的任何客户端读取别的窗口内容、监听全局键盘 ——
共享用户桌面的 X server = 显示层没有隔离。沙箱副本因此**绝不绑定整个
/tmp/.X11-unix**, 而是按显示方案只给它一个专属 server:

    headless   -> 不给任何显示 (DISPLAY / WAYLAND_DISPLAY 都摘掉)
    xvfb       -> 只绑本副本 Xvfb 的那一个 socket 文件
    desktop    -> 由 prefs.sandbox_x11 决定:
        nested  (默认) Xephyr 嵌套 X server: 在用户桌面上开一个窗口, 浏览器跑在
                其中的独立 X server 里, 看不到桌面上的其他窗口与键盘
        shared  显式选择共享桌面 X server (不隔离, status 如实标出)
    wayland    -> prefs.window_system="wayland": 只绑 Wayland compositor 的 socket;
                Wayland 协议本身隔离客户端 (不能读别人的窗口/输入)

## Wayland (process 与 sandbox 都支持)

window_system 默认 auto = 改动前行为: 有 X (含 XWayland) 就钉 x11 后端, 因为窗口断言/
定位依赖 xdotool。选 wayland 时 Chromium 用 --ozone-platform=wayland、Firefox 设
MOZ_ENABLE_WAYLAND=1; 代价如实写明: X 工具看不到原生 Wayland 窗口, 窗口断言与定位
不可用 (runtime.reason 会说明), 这是 Wayland 的安全模型本身决定的。

X11 的抽象 socket (@/tmp/.X11-unix/Xn) 属于网络命名空间 —— 沙箱 unshare-net 后天然不可达,
所以「只绑一个 socket 文件」就是完整的边界。

缺 Xephyr / 缺 Wayland socket 时**拒绝启动**, 不悄悄退回共享桌面。
"""

from __future__ import annotations

import os
import shutil
import subprocess
import time
from pathlib import Path

from . import xenv, xvfb

TOOL_XEPHYR = "Xephyr"
K_DISPLAY, K_WAYLAND, K_XAUTH = xenv.K_DISPLAY, xenv.K_WAYLAND, xenv.K_XAUTH
K_RUNTIME = "XDG_RUNTIME_DIR"
SANDBOX_WL_DIR = "/run/hx-wayland"
SANDBOX_WL_NAME = "wayland-0"
OZONE_X11 = "--ozone-platform=x11"
OZONE_WAYLAND = "--ozone-platform=wayland"
NESTED, WAYLAND, SHARED = "nested", "wayland", "shared"
K_MOZ_WAYLAND, K_GDK = "MOZ_ENABLE_WAYLAND", "GDK_BACKEND"
XEPHYR_WAIT_S = 10.0


class DisplayUnavailable(RuntimeError):
    pass


def wayland_socket() -> str:
    """找本用户的 Wayland compositor socket (服务进程常没有 WAYLAND_DISPLAY, 所以也扫目录)。"""
    run = os.environ.get(K_RUNTIME) or "/run/user/%d" % os.getuid()
    name = os.environ.get(K_WAYLAND, "")
    cands = [Path(name)] if name.startswith("/") else []
    if name and not name.startswith("/"):
        cands.append(Path(run) / name)
    if Path(run).is_dir():
        cands += sorted(Path(run).glob("wayland-[0-9]*"))
    for c in cands:
        if c.is_socket():
            return str(c)
    return ""


def status() -> dict:
    return {
        "session_type": os.environ.get("XDG_SESSION_TYPE", ""),
        "wayland_socket": wayland_socket(),
        "x11_display": os.environ.get(K_DISPLAY, ""),
        "xephyr": shutil.which(TOOL_XEPHYR) or "",
        "xvfb": shutil.which(xvfb.TOOL_XVFB) or "",
        "window_systems": ["auto", "x11", WAYLAND],
        "sandbox_x11": [NESTED, SHARED],
    }


def start_nested(env_id: str, plan) -> None:
    """desktop + nested: 在用户桌面上起 Xephyr, 把 plan 改指向这个私有 X server。"""
    tool = shutil.which(TOOL_XEPHYR)
    if not tool:
        raise DisplayUnavailable(
            "沙箱显示模式 nested 需要 Xephyr (xserver-xephyr); 已拒绝启动, 未退回共享桌面"
        )
    host_env = dict(os.environ, **plan.env)
    with xvfb._LOCK:  # 与 Xvfb 共用挑号锁与号段: 两者都用 socket 文件判空闲
        n = xvfb.pick_display()
        proc = subprocess.Popen(
            [tool, ":%d" % n, "-screen", xvfb.XVFB_SCREEN.rsplit("x", 1)[0],
             "-resizeable", "-nolisten", "tcp", "-title", "hx-browser sandbox"],
            env=host_env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )  # fmt: skip
        deadline = time.monotonic() + XEPHYR_WAIT_S
        while time.monotonic() < deadline and not xenv.socket_path(n).exists():
            if proc.poll() is not None:
                break
            time.sleep(xvfb.XVFB_POLL_S)
    if not xenv.socket_path(n).exists():
        proc.terminate()
        raise DisplayUnavailable("Xephyr 未能在 %s 上启动" % plan.display)
    xvfb.PROCS[env_id] = proc  # 停止路径已统一回收 xvfb.PROCS
    disp = ":%d" % n
    plan.source_detail = "xephyr-nested-on-" + plan.display
    plan.display = disp
    plan.xauth = ""
    plan.env = {K_DISPLAY: disp}


def wants_wayland(env) -> bool:
    return env.prefs.window_system == WAYLAND and not env.fingerprint.headless


def use_wayland(env_id: str, plan) -> str:
    """window_system=wayland: 把 plan 改成原生 Wayland; 找不到 compositor 就拒绝启动。"""
    sock = wayland_socket()
    if not sock:
        raise DisplayUnavailable(
            "window_system=wayland, 但找不到 Wayland compositor socket; 已拒绝启动"
        )
    if plan.source == "xvfb":
        xvfb.release(env_id)  # 探测阶段为 X 回落起的 Xvfb 用不上了
    plan.source, plan.visible = "desktop", True
    plan.source_detail = "wayland:" + sock
    plan.display, plan.xauth, plan.env = "", "", {}
    plan.unset = (K_DISPLAY, K_XAUTH)
    plan.reason = (
        "原生 Wayland 窗口: X 工具无法断言/定位 (Wayland 不允许客户端互相查看)"
    )
    return sock


def apply_wayland(
    argv: list[str], launch_env: dict, sock: str, sandboxed: bool
) -> list[str]:
    """就地切换到 Wayland 后端; sandboxed 时返回只绑这一个 socket 的 bwrap 参数。"""
    for k in (K_DISPLAY, K_XAUTH):
        launch_env.pop(k, None)
    argv[:] = [OZONE_WAYLAND if a == OZONE_X11 else a for a in argv]
    launch_env.update({K_MOZ_WAYLAND: "1", K_GDK: "wayland"})
    if not sandboxed:
        launch_env[K_WAYLAND] = sock  # 绝对路径形式, 不依赖 XDG_RUNTIME_DIR
        return []
    launch_env.update({K_RUNTIME: SANDBOX_WL_DIR, K_WAYLAND: SANDBOX_WL_NAME})
    target = "%s/%s" % (SANDBOX_WL_DIR, SANDBOX_WL_NAME)
    return ["--dir", SANDBOX_WL_DIR, "--chmod", "0700", SANDBOX_WL_DIR,
            "--bind", sock, target]  # fmt: skip


def _display_number(display: str) -> int:
    num = display.rsplit(":", 1)[-1].split(".")[0]
    return int(num) if num.isdigit() else -1


def sandbox_args(plan, argv: list[str], launch_env: dict) -> list[str]:
    """沙箱的显示层参数: 只给本副本专属的那一个显示 server。"""
    source = getattr(plan, "source", "headless") if plan is not None else "headless"
    detail = getattr(plan, "source_detail", "") or ""
    if source in ("headless", "xvfb-failed"):
        for k in (K_DISPLAY, K_WAYLAND, K_XAUTH):
            launch_env.pop(k, None)
        return []
    if detail.startswith("wayland:"):
        return apply_wayland(argv, launch_env, detail.split(":", 1)[1], True)
    n = _display_number(plan.display)
    if n < 0:
        raise DisplayUnavailable("无法解析 DISPLAY=%s" % plan.display)
    launch_env.pop(K_WAYLAND, None)
    sock = str(xenv.socket_path(n))
    out = ["--ro-bind", sock, sock]  # 只给这一个 X server, 其他显示 (含用户桌面) 不可见
    xauth = launch_env.get(K_XAUTH, "")
    if xauth:
        out += ["--ro-bind-try", xauth, xauth]
    return out


def isolated(plan, sandbox_x11: str) -> bool:
    detail = getattr(plan, "source_detail", "") or ""
    source = getattr(plan, "source", "headless") if plan is not None else "headless"
    return not (
        source == "desktop"
        and sandbox_x11 == SHARED
        and not detail.startswith(("wayland:", "xephyr"))
    )
