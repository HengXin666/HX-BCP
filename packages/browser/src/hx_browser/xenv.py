"""X 环境的低层原语 —— 进程/环境/socket/连通性 (2026-10-13)。

Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md
Agent Note: .agents/notes/implemented/bug-fix/2026-10-17-hxbrowser-visible-window-no-ime.md

## 为什么单独一层

desktop.py (桌面在哪) / window.py (窗口在不在) / xvfb.py (虚拟屏) 三个模块都要做
同一批底层动作: 拼 X 环境变量、读 /proc/<pid>/environ、判 socket 存在、**判能不能连**。
放在任何其中一个里都会让另外两个反向依赖它 —— 三个模块互相 import, 职责糊成一团。

## 判据只此一处: display_ok

**socket 存在 != 有权限连** (2026-10-13 本机实测):

    $ env -i HOME= PATH=<面板那套> DISPLAY=:1 xdpyinfo
    Authorization required, but no authorization protocol specified
    xdpyinfo:  unable to open display ":1".        <- socket 在, 权限没有

故每个候选 display 都必须真跑一次 xdpyinfo 通过才作数。**缺 xdpyinfo 时判不通** ——
本仓 gh-pool 的实现会退化成「有 socket 就算通」(它自己标了「弱判据」), 这里**有意分歧**:
弱判据会让「窗口可见」变成猜测, 而这条链路存在的全部理由就是不猜。
"""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

K_DISPLAY = "DISPLAY"
K_XAUTH = "XAUTHORITY"
K_WAYLAND = "WAYLAND_DISPLAY"
TOOL_XDPYINFO = "xdpyinfo"
X11_SOCKET_DIR = "/tmp/.X11-unix"
_XDPYINFO_TIMEOUT = 6

## 输入法 / D-Bus 会话变量 —— 开在**用户桌面**上的窗口必须带上它们 (2026-10-17)。
##
## 为什么本服务自己没有: 面板用 `nsenter + setpriv ... env HOME=... PATH=...` 拉起本服务,
## 环境里**只有 HOME 与 PATH** (实测 `/proc/<服务pid>/environ`); 桌面会话那份
## (`plasmashell` / `Xwayland` 的 environ) 才带这些。子进程继承的是**服务**那份,
## 于是窗口起来了、进程也活着, 而用户在窗口里**一个字都打不出来**:
##   · XIM 客户端靠 `XMODIFIERS` 选输入法服务器 —— 缺了就根本不连 fcitx5;
##   · GTK/Qt 的 im module 还要经 `DBUS_SESSION_BUS_ADDRESS` 找到 fcitx5;
##   · `XDG_RUNTIME_DIR` 是 D-Bus/会话目录的根 (缺了 im module 连不上总线)。
##
## 实测 (2026-10-17, 隔离 Xvfb :99 + kwin_x11 + 私有 fcitx5, 打 "nihao" + 空格):
##   不带这些变量 -> 输入框得到 `"nihao "`, fcitx5 侧 state=0 (从未连接);
##   带上        -> 输入框得到 `"你好"`,   fcitx5 侧 state=2 + 当前输入法 pinyin。
## 只取白名单, 不整份转发: 桌面进程的 environ 里还有一堆与显示/输入法无关的东西,
## 全盘照抄等于把服务进程的环境面偷偷放大 (PATH/代理/凭据都在这份里)。
SESSION_KEYS = (
    "XMODIFIERS",
    "GTK_IM_MODULE",
    "QT_IM_MODULE",
    "QT4_IM_MODULE",
    "SDL_IM_MODULE",
    "GLFW_IM_MODULE",
    "INPUT_METHOD",
    "DBUS_SESSION_BUS_ADDRESS",
    "XDG_RUNTIME_DIR",
)


def uid() -> int:
    try:
        return os.getuid()
    except Exception:  # noqa: BLE001
        return -1


def socket_path(n: int) -> Path:
    """display 号 n 对应的 X socket 路径。"""
    return Path("%s/X%d" % (X11_SOCKET_DIR, n))


def x_env(display: str, xauth: str = "") -> dict[str, str]:
    """在**当前**环境上叠加 DISPLAY/XAUTHORITY (给子进程用)。"""
    env = {**os.environ, K_DISPLAY: display}
    if xauth:
        env[K_XAUTH] = xauth
    return env


def session_env(environ: dict[str, str]) -> dict[str, str]:
    """从某个进程的 environ 里挑出输入法/会话变量 (白名单见 SESSION_KEYS)。

    只挑白名单, **不整份转发**: 桌面进程的 environ 里还有代理、凭据、PATH 等
    与「能不能打字」无关的东西, 照抄等于把子进程的环境面偷偷放大。
    """
    return {k: environ[k] for k in SESSION_KEYS if environ.get(k)}


def proc_environ(pid: int) -> dict[str, str]:
    """读 /proc/<pid>/environ (NUL 分隔)。读不到返回空 dict。"""
    try:
        raw = Path("/proc/%d/environ" % pid).read_bytes()
    except OSError:
        return {}
    out: dict[str, str] = {}
    for kv in raw.split(b"\0"):
        if b"=" not in kv:
            continue
        k, _, v = kv.partition(b"=")
        try:
            out[k.decode()] = v.decode("utf-8", "replace")
        except Exception:  # noqa: BLE001
            continue
    return out


def pids_of(name: str) -> list[int]:
    """按 comm 精确匹配**同用户**进程 (不误伤名字里含同串的别的进程)。"""
    out: list[int] = []
    me = uid()
    try:
        entries = list(Path("/proc").iterdir())
    except OSError:
        return out
    for p in entries:
        if not p.name.isdigit():
            continue
        try:
            if os.stat(p).st_uid != me:
                continue
            comm = (p / "comm").read_text().strip()
        except OSError:
            continue
        if comm == name or comm.startswith(name):
            out.append(int(p.name))
    return out


def display_ok(display: str, xauth: str = "") -> bool:
    """真值判据: 这个 display 能不能**真正连上去** (见模块头实测反例)。

    缺 xdpyinfo 时判**不通** —— 与 gh-pool 的有意分歧, 理由见模块头。
    """
    if not shutil.which(TOOL_XDPYINFO):
        return False
    try:
        r = subprocess.run(
            [TOOL_XDPYINFO],
            env=x_env(display, xauth),
            capture_output=True,
            timeout=_XDPYINFO_TIMEOUT,
        )
        return r.returncode == 0
    except Exception:  # noqa: BLE001
        return False
