"""用户真实桌面探测 —— 「窗口对用户可见吗」的唯一判据 (2026-10-13)。

Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md
Agent Note: .agents/notes/implemented/bug-fix/2026-10-17-hxbrowser-visible-window-no-ime.md

职责切分 (每文件 <=300 行, 且一件事只在一处定义):

    desktop.py  ← 本文件: 用户真实桌面在哪 + 把「可见/不可见」拍板成 DisplayPlan
    xvfb.py              探不到桌面时起虚拟屏 (屏幕外)
    window.py            窗口真的映射了吗 (xdotool 断言) + 窗口位置错开

## 为什么不能写死 DISPLAY (实测 2026-10-13, 本机)

宿主是 KDE/Wayland, 用户看到的桌面在 Xwayland :1 上:

    pid 2760 Xwayland    DISPLAY=:1  XAUTHORITY=/run/user/1000/xauth_gkwcyA
    pid 2850 plasmashell DISPLAY=:1  XAUTHORITY=/run/user/1000/xauth_gkwcyA

而本服务由 HX-WebX 面板托管, 启动形态实测是:

    nsenter -t 1 -m -p -i -u -n -- setpriv --reuid=1000 ... /usr/bin/env HOME=... PATH=...
      -> uv run python run.py --host 127.0.0.1 --port 8325

`setpriv` 之后**只显式传了 HOME 与 PATH** —— 服务进程里 DISPLAY 是空的 (实测
`/proc/3142/environ` 无 DISPLAY)。XAUTHORITY 更是**每会话随机**的
(`xauth_<随机后缀>`), 写死必然失效。

## 判据是**连通性**, 不是 socket 存在 (实测反例)

    $ env -i HOME= PATH=<面板那套> DISPLAY=:1 xdpyinfo
    Authorization required, but no authorization protocol specified
    xdpyinfo:  unable to open display ":1".        <- socket 在, 权限没有

故每个候选 display 都必须真跑一次 xdpyinfo 通过才作数。**探不到就明说探不到** ——
宁可回落 Xvfb 并报 visible=false, 也不盲试 :0/:1/:2 把窗口开到不知道哪里去。

## 为什么必须 pop WAYLAND_DISPLAY

只要环境里有 WAYLAND_DISPLAY, Chromium 就走 Wayland 后端, 窗口在 KWin 合成器里:
X 工具**查不到任何窗口** (`xwininfo -root -children` 只有 KWin/xsettingsd/fcitx)。
后果不是「窗口看不见」, 而是「窗口存不存在**无法断言**」。故 DisplayPlan.unset
带上 WAYLAND_DISPLAY, 由 browser.py 在子进程环境里删掉。

## 来源与边界

判据按需复用 workspaces/browser-auto/gh-pool/server/app/desktop.py (2026-10-02 实测稿),
**不 import 跨工作区** —— 那属于另一条链路, 耦合会让两边都改不动。

与它的一处**有意分歧**: 它缺 xdpyinfo 时退化成「有 socket 就算通」(弱判据, 代码里
自己标了 "明确标注为弱判据")。这里直接判**不通** —— 弱判据会让「窗口可见」变成猜测,
而本模块存在的全部理由就是不猜。代价: 缺工具时一律回落 Xvfb, 但会**明说**原因。
"""

from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, field
from pathlib import Path

from . import xenv, xvfb

# 低层原语 (K_DISPLAY / K_XAUTH / K_WAYLAND / TOOL_XDPYINFO / socket_path /
# display_ok / proc_environ / pids_of) 都在 xenv.py —— 那里是**唯一**定义处。
# 本模块只保留"桌面在哪"的探测顺序与拍板逻辑。
K_DISPLAY = xenv.K_DISPLAY
K_XAUTH = xenv.K_XAUTH
K_WAYLAND = xenv.K_WAYLAND
TOOL_XDPYINFO = xenv.TOOL_XDPYINFO

# 桌面进程的候选可执行名 —— 取**第一个活着的**进程的 environ。顺序即优先级:
# 合成器 (真正持有 display 的那个) 优先。
DESKTOP_PROCS = (
    "Xwayland",
    "kwin_wayland",
    "plasmashell",
    "gnome-shell",
    "xfwm4",
    "mutter",
    "Xorg",
    "xfce4-session",
)


@dataclass
class DisplayPlan:
    """一次启动的显示方案 —— 调用方据此设置子进程环境并钉死后端。

    visible 是**意图** (探到了真实桌面 / headless 明确不要窗口); 真值断言在
    browser.py 里由 window.window_verdict 给出。两者分开是刻意的:
    「打算开在用户屏上」与「真的开上去了」是两件事, 不能互相冒充。
    """

    display: str = ""
    xauth: str = ""
    visible: bool = False
    # headless 是**本次启动的可见性决策** (fingerprint.headless 或全局 headless_default)。
    # 为什么放进 plan: 启动参数 (--headless=new vs --ozone-platform=x11) 必须与显示方案
    # 同源。两处各判一次就会出现「plan 说 headless, 参数却钉了 x11」——
    # 那是一个连不上 X 的 Chromium (进程起完即死, 见模块头 2026-10-13 的老坑)。
    headless: bool = False
    # source 是**类别** (desktop | headless | xvfb | xvfb-failed), 不是细节。
    # 为什么必须分开 (2026-10-13 实测踩到): 断言要判"这个 X 是不是用户桌面",
    # 判据只能比**类别**。若把细节 (如 "Xwayland#2760") 塞进 source, 断言里
    # source == "desktop" 恒为 false -> 真桌面上也报 visible=false (假阴性)。
    source: str = ""
    source_detail: str = ""  # 细节 ("Xwayland#2760" / "xauth-scan:xauth_xxx"), 只供排查
    reason: str = ""
    env: dict[str, str] = field(default_factory=dict)
    unset: tuple[str, ...] = ()


def _from_desktop_procs(tried: list[str]) -> dict:
    """① 桌面进程自己的 environ —— 权威来源 (它**正在服务**的那个 display)。"""
    for name in DESKTOP_PROCS:
        for pid in xenv.pids_of(name):
            env = xenv.proc_environ(pid)
            disp = (env.get(K_DISPLAY) or "").strip()
            if not disp:
                continue
            xauth = (env.get(K_XAUTH) or "").strip()
            tried.append("%s#%d %s" % (name, pid, disp))
            if xenv.display_ok(disp, xauth):
                return {
                    "ok": True,
                    "display": disp,
                    "xauth": xauth,
                    "source": "%s#%d" % (name, pid),
                    # 整份 environ 带回去: 输入法变量只能从这里取 (本服务自己没有, 见 xenv 模块头)
                    "environ": env,
                }
    return {"ok": False}


def _from_self_env(tried: list[str]) -> dict:
    """② 本进程自带的 DISPLAY (服务若由带桌面的 shell 亲手起, 这是最直接的)。"""
    d = (os.environ.get(K_DISPLAY) or "").strip()
    if not d:
        return {"ok": False}
    xa = (os.environ.get(K_XAUTH) or "").strip()
    tried.append("self %s" % d)
    if xenv.display_ok(d, xa):
        return {"ok": True, "display": d, "xauth": xa, "source": "self-env"}
    return {"ok": False}


def _from_xauth_scan(tried: list[str]) -> dict:
    """③ 已存在的 xauth 文件 + 已存在的 socket 配对。

    **不是猜**: 每个候选都必须 xdpyinfo 通过才返回 —— 与 gh-pool 的 ③ 同判据。
    """
    rt = Path("/run/user/%d" % xenv.uid())
    if not rt.exists():
        return {"ok": False}
    for xa in sorted(rt.glob("xauth_*")):
        for n in range(0, 4):
            if not xenv.socket_path(n).exists():
                continue
            disp = ":%d" % n
            tried.append("xauth-scan %s" % disp)
            if xenv.display_ok(disp, str(xa)):
                return {
                    "ok": True,
                    "display": disp,
                    "xauth": str(xa),
                    "source": "xauth-scan:%s" % xa.name,
                }
    return {"ok": False}


def detect_user_desktop() -> dict:
    """探测用户**真实可见**的桌面。

    返回 {"ok": True, "display": ":1", "xauth": "/run/user/1000/xauth_xxx",
          "source": "Xwayland#2760", "environ": {...}, "tried": [...]}
    或   {"ok": False, "err": "...", "tried": [...]} —— 调用方据此回落, 并应把 err 透给用户。
    `environ` 是桌面进程的整份环境 (输入法变量从它取, 见 `xenv.SESSION_KEYS`)。
    """
    tried: list[str] = []
    if not shutil.which(TOOL_XDPYINFO):
        return {
            "ok": False,
            "tried": tried,
            "err": "缺 %s, 无法证明桌面可连 (不做弱判据猜测)" % TOOL_XDPYINFO,
        }
    for probe in (_from_desktop_procs, _from_self_env, _from_xauth_scan):
        got = probe(tried)
        if got.get("ok"):
            got["tried"] = tried
            return got
    return {
        "ok": False,
        "tried": tried,
        "err": "找不到用户可见的桌面 (无 DISPLAY 能通过 xdpyinfo 连通性判据)。已试: %s"
        % (", ".join(tried) or "无候选"),
    }


def _desktop_session_environ() -> dict[str, str]:
    """桌面进程的 environ 里能取到输入法变量的那一份 (取到就返回, 不做连通性判据)。

    为什么需要它: 只有 ① 那条路径会把 environ 带回来; ② (本进程自带 DISPLAY) 与
    ③ (xauth 扫描) 没有 —— 而「窗口能不能打字」与「display 是怎么探到的」无关。
    取到第一份带 SESSION_KEYS 的就够, 不必是持有 display 的那个进程。
    """
    for name in DESKTOP_PROCS:
        for pid in xenv.pids_of(name):
            env = xenv.session_env(xenv.proc_environ(pid))
            if env:
                return env
    return {}


def resolve_display(env, headless_default: bool = False) -> DisplayPlan:
    """按 fingerprint.headless 与全局 headless_default 决定显示方案 (**唯一判据**)。

    headless  = 两个开关**取或** —— 环境级 fingerprint.headless, 或全局 headless_default。
                全局那个是"一键关掉所有窗口"的开关 (用户 2026-10-17 的诉求); 之前它
                只在 models/api 里出现、**没有任何读取点**, 于是"设了也没用"。
    headless=false -> 探到真实桌面则 visible 意图为真; 探不到回落 Xvfb 并**明说看不见**
    """
    per_env = bool(getattr(env.fingerprint, "headless", False))
    if per_env or headless_default:
        why = (
            "fingerprint.headless=true" if per_env else "全局设置 headless_default=true"
        )
        return DisplayPlan(
            headless=True,
            source="headless",
            reason="%s —— 本次不开 X display, 用户看不见该窗口" % why,
        )

    det = detect_user_desktop()
    if det.get("ok"):
        disp = str(det["display"])
        xauth = str(det.get("xauth") or "")
        # 输入法/D-Bus 会话变量: 桌面进程那份 (服务自己没有, 见 xenv 模块头)。
        # 少了它们, 窗口照样能开、进程照样活着, 只是用户**打不出中文**。
        sess = xenv.session_env(det.get("environ") or {}) or _desktop_session_environ()
        return DisplayPlan(
            display=disp,
            xauth=xauth,
            visible=True,
            source="desktop",
            source_detail=str(det.get("source") or ""),
            env={
                K_DISPLAY: disp,
                **({K_XAUTH: xauth} if xauth else {}),
                **sess,
            },
            unset=(K_WAYLAND,),
        )

    disp, proc = xvfb.start()
    if not disp or proc is None:
        if proc is not None:
            try:
                proc.terminate()
            except Exception:  # noqa: BLE001
                pass
        return DisplayPlan(
            source="xvfb-failed",
            reason="探测不到用户可见桌面, 且 Xvfb 起不来: %s" % det.get("err"),
        )
    xvfb.PROCS[env.id] = proc
    return DisplayPlan(
        display=disp,
        visible=False,
        source="xvfb",
        reason="探测不到用户可见桌面 (%s); 已回落 Xvfb %s —— 窗口在屏幕外, "
        "用户**看不见**" % (det.get("err"), disp),
        env={K_DISPLAY: disp},
        unset=(K_WAYLAND,),
    )
