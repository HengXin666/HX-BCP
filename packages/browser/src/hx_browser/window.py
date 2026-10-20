"""窗口断言与位置错开 —— 「窗口到底存不存在」的可判定断言 (2026-10-13)。

Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md

## 为什么「窗口存在」必须可断言 (而不是「进程活着就够了」)

本仓实测过反例 (2026-10-02, gh-pool): 窗口进程活着、截图正常、登录态也对, 但
**桌面上根本没有那个窗口** —— 宿主是 KDE Wayland, Chromium 默认走 Wayland 后端,
窗口在 KWin 合成器里, X 工具完全看不到。

所以断言建立在两件事同时成立上:

    a) 启动时把显示后端**钉死**为 x11 (desktop.resolve_display 的 unset=WAYLAND_DISPLAY
       + browser.py 的 --ozone-platform=x11) —— 否则窗口不在 X 里, 断言无从谈起;
    b) 用 `xdotool search --onlyvisible --pid <chrome 主进程>` 枚举该进程的窗口。

判据取「**至少一个窗口有非空标题**」, 而不只是「枚举到了 id」: Chromium 会建若干
无标题的辅助窗口 (输入法等), 只有一个 id 不足以说明用户真的能看到页面。

## 位置错开 (多实例并发)

窗口按 serial_no 错开 (80+40*i, 60+40*i), 否则 N 个窗口完全重叠, 用户只看得见最上面那个
—— 那等于「多窗口」这个需求没实现。按网格回绕, 免得第 49 个环境开到 x=2000 (屏幕外)。

判据按需复用 workspaces/browser-auto/gh-pool/server/app/window.py (2026-10-02 实测稿),
**不 import 跨工作区** —— 那属于另一条链路, 耦合会让两边都改不动。
"""

from __future__ import annotations

import re
import shutil
import subprocess
import time

from . import xenv

TOOL_XDOTOOL = "xdotool"
K_DISPLAY = xenv.K_DISPLAY
K_XAUTH = xenv.K_XAUTH

# 窗口位置: 第 i 个环境 (i = serial_no-1) 开在 (80+40*i, 60+40*i)。
# 加窗框补偿 (TITLEBAR_H): 合成器给窗口加标题栏后, 客户区会整体下移,
# 而 xdotool getwindowgeometry 报的是**客户区**坐标。不对齐的话"请求 Y=60"
# 实测会落在 Y=152 (差 92px), 多窗口虽然仍错开, 但偏移量不可预期、也难以断言。
WINDOW_ORIGIN_X = 80
WINDOW_ORIGIN_Y = 60
WINDOW_STEP = 40
WINDOW_COLS = 10
WINDOW_ROWS = 6
TITLEBAR_H = 92

_GEOM_RE = re.compile(r"Geometry:\s*(\d+x\d+)")
_POS_RE = re.compile(r"Position:\s*(-?\d+),(-?\d+)")
# 标题就绪轮询: Chromium 建窗口时先给一个空/占位标题, 页面标题稍后才设上。
# 抓早了会得到空串 —— T4 面板靠它显示"这个窗口是什么", 空串等于空白 (2026-10-13 实测)。
TITLE_WAIT_S = 8.0
TITLE_POLL_S = 0.4


def window_position(serial_no: int) -> tuple[int, int]:
    """按 serial_no 错开窗口位置 —— 多实例并发时窗口不重叠。"""
    i = max(int(serial_no or 1) - 1, 0)
    return (
        WINDOW_ORIGIN_X + WINDOW_STEP * (i % WINDOW_COLS),
        WINDOW_ORIGIN_Y + WINDOW_STEP * ((i // WINDOW_COLS) % WINDOW_ROWS),
    )


def move_window(win_id: str, x: int, y: int, display: str, xauth: str = "") -> bool:
    """把窗口移到指定**客户区**绝对坐标。返回是否移动成功。

    ## 为什么不能只靠 --window-position (2026-10-13 实测)

    实测 KDE/KWin 下 `--window-position` **传了但不生效**: 三个环境分别请求
    (120,260)/(160,260)/(200,260), 而实际落点全是 KWin 的智能摆放结果
    (0,0)/(180,152)/(240,152) —— 参数在 cmdline 里, 效果被合成器覆盖。
    这正是"传了参数 != 生效了"的典型: 只看 cmdline 会以为错开成功。

    故: 启动参数照传 (在别的桌面环境上它本来就有效), 但**窗口映射之后用
    xdotool windowmove 强制摆位**, 并以实测坐标为准。
    """
    if not win_id or not display:
        return False
    try:
        _xdo(
            ["windowmove", win_id, str(int(x)), str(int(y))], _x_env(display, xauth), 5
        )
    except Exception:  # noqa: BLE001
        return False
    got = window_geometry(win_id, display, xauth)
    if got is None:
        return False
    return abs(got[0] - int(x)) <= 2 and abs(got[1] - int(y)) <= 2


def window_geometry(
    win_id: str, display: str, xauth: str = ""
) -> tuple[int, int] | None:
    """取窗口**客户区**绝对坐标 (实测值, 不是请求值)。取不到返回 None。"""
    if not win_id or not display:
        return None
    try:
        out = _xdo(["getwindowgeometry", win_id], _x_env(display, xauth), 5)
    except Exception:  # noqa: BLE001
        return None
    m = _POS_RE.search(out)
    return (int(m.group(1)), int(m.group(2))) if m else None


def _x_env(display: str, xauth: str) -> dict[str, str]:
    """X 子进程环境 —— 判据与拼接在 xenv.py (唯一处), 这里只转发。"""
    return xenv.x_env(display, xauth)


def _xdo(args: list[str], env: dict[str, str], timeout: float) -> str:
    r = subprocess.run(
        [TOOL_XDOTOOL, *args], env=env, capture_output=True, timeout=timeout, text=True
    )
    return r.stdout or ""


def find_window(pid: int, display: str, xauth: str = "") -> dict:
    """该进程在给定 X display 上有没有**可见顶层窗口** (单次快照)。

    返回 {ok, windows: [{id,title,geom}], tool} / {ok: False, windows, err}。
    """
    if not pid:
        return {
            "ok": False,
            "windows": [],
            "err": "没有 chrome 主进程 pid, 无法断言窗口",
        }
    if not display:
        return {
            "ok": False,
            "windows": [],
            "err": "本次启动没有 X display, 无法断言窗口",
        }
    if not shutil.which(TOOL_XDOTOOL):
        return {
            "ok": False,
            "windows": [],
            "err": "缺 %s, 无法断言窗口 (弱证据: 进程活着)" % TOOL_XDOTOOL,
        }
    env = _x_env(display, xauth)
    try:
        ids = [
            w
            for w in _xdo(
                ["search", "--onlyvisible", "--pid", str(pid)], env, 8
            ).split()
            if w.strip()
        ]
        wins = []
        for w in ids:
            title = _xdo(["getwindowname", w], env, 5).strip()
            m = _GEOM_RE.search(_xdo(["getwindowgeometry", w], env, 5))
            wins.append({"id": w, "title": title, "geom": m.group(1) if m else ""})
    except Exception as exc:  # noqa: BLE001
        return {"ok": False, "windows": [], "err": str(exc)[:160]}
    return {"ok": any(w["title"] for w in wins), "windows": wins, "tool": TOOL_XDOTOOL}


def _no_window_reason(snap: dict, pid: int, display: str) -> str:
    err = str(snap.get("err") or "")
    if err:
        return "窗口断言失败: %s" % err
    n = len(snap.get("windows") or [])
    if n:
        return "枚举到 %d 个窗口但**标题全空** (不足以说明用户能看到页面)" % n
    return "xdotool --onlyvisible --pid %d 在 %s 上枚举不到可见窗口" % (pid, display)


def window_verdict(
    pid: int,
    display: str,
    xauth: str = "",
    timeout: float = 20.0,
    title_wait: float = TITLE_WAIT_S,
) -> tuple[bool, str, str, str]:
    """等窗口映射与标题就绪, 返回 (visible, window_title, reason, window_id)。

    这是 visible 字段的**唯一真值来源**。两个等待阶段分开, 因为它们是两件事:

        ① 窗口**映射**出来   —— 浏览器刚起来时窗口还没建
        ② 窗口**标题**非空   —— Chromium 先建一个空/占位标题的窗口, 页面标题稍后才设上

    阶段 ② 单独给 `title_wait` 预算: 抓早了会拿到空串, 而 T4 面板靠标题显示
    "这个窗口是什么", 空串等于空白。等不到就**如实说明**"标题未在 N 秒内就绪"
    (带上 window_id, 上层仍可用它截图/操作), 不静默给空串。

    超时仍无窗口 -> visible=False, reason 说清「进程活着但窗口没映射」,
    绝不拿「进程活着」冒充「窗口可见」。
    """
    deadline = time.time() + max(timeout, 0.0)
    title_deadline: float | None = None
    win_id = ""
    while True:
        snap = find_window(pid, display, xauth)
        wins = snap.get("windows") or []
        titled = [w for w in wins if w["title"]]
        if titled:
            return True, str(titled[0]["title"]), "", str(titled[0]["id"])
        if wins:
            # 窗口已在, 只是标题还空 -> 进标题等待期 (只计一次起点)
            if title_deadline is None:
                title_deadline = time.time() + max(title_wait, 0.0)
                win_id = str(wins[0]["id"])
            if time.time() >= title_deadline:
                return (
                    False,
                    "",
                    (
                        "窗口 %s 已映射到 %s, 但标题未在 %.0fs 内就绪 "
                        "(Chromium 尚未设上页面标题; 窗口存在, 用户可能已能看到)"
                        % (win_id, display, title_wait)
                    ),
                    win_id,
                )
        elif time.time() >= deadline:
            return False, "", _no_window_reason(snap, pid, display), ""
        time.sleep(0.5)


def activate_window(pid: int, win_id: str, display: str, xauth: str = "") -> dict:
    """定位窗口: 激活并抬到最前 (副本管理「定位窗口」)。返回 {ok, window_id, err}。

    无 win_id 时现场按 pid 找一次 (窗口可能在断言之后才重建)。Xvfb 上也能"激活",
    但用户看不见 —— 调用方须结合 display_source 如实提示。
    """
    if not shutil.which(TOOL_XDOTOOL):
        return {
            "ok": False,
            "window_id": "",
            "err": "缺 %s, 无法定位窗口" % TOOL_XDOTOOL,
        }
    if not display:
        return {
            "ok": False,
            "window_id": "",
            "err": "该副本没有 X display (headless), 没有可定位的窗口",
        }
    if not win_id:
        wins = find_window(pid, display, xauth).get("windows") or []
        win_id = str(wins[0]["id"]) if wins else ""
    if not win_id:
        return {"ok": False, "window_id": "", "err": "找不到该副本的可见窗口"}
    env = _x_env(display, xauth)
    try:
        _xdo(["windowmap", win_id], env, 5)
        _xdo(["windowactivate", win_id], env, 5)
        _xdo(["windowraise", win_id], env, 5)
    except Exception as exc:  # noqa: BLE001
        return {"ok": False, "window_id": win_id, "err": str(exc)[:160]}
    return {"ok": True, "window_id": win_id, "err": ""}
