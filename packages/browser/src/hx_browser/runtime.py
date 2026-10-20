"""运行态账本与孤儿进程 —— 服务重启后仍认得在跑的浏览器 (2026-10-13 从 browser.py 拆出)。

Agent Note: .agents/notes/implemented/bug-fix/2026-09-19-hx-browser-zombie-and-runtime-persistence.md
Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md

## 为什么拆出来

browser.py 已到 659 行 (棘轮登记在册, **只许降不许涨**), 而本次要加「可见窗口 +
多实例并发」—— 逻辑塞进去必然破 300 行上限。拆的边界按职责取:
**「进程/运行态这盘账」**与**「浏览器怎么起/怎么停」**是两件事。判据原样搬移。

## 两个必须一起搬的东西 (拆错就会静默退化)

`_PROCS` 与 `_reap_finished` **必须同住一个模块**: 原实现丢掉 Popen 引用,
于是**没有任何人调用 wait()** —— 浏览器退出后变僵尸 (defunct), 而僵尸 pid 依然存在,
`os.kill(pid, 0)` 照样成功。实测后果三症状同一根因:

    1. 环境永远显示运行中: 3 个僵尸 pid 正是 3 个环境记录的 runtime.pid;
    2. stop() 空等满 10s: 判活为真 -> 等循环跑满 deadline -> killpg 对僵尸无效;
    3. 僵尸只能由父进程 reap, 面板上怎么点都没用。

## 兼容性

browser.py 从本模块**再导出** RUNTIME / _PROCS / _pid_alive / scan_orphans 等,
api.py 与 tests/ 的既有 import 路径不变 (tests 直接操作 `B._PROCS`, 那是**同一个
dict 对象**, 按引用共享)。
"""

from __future__ import annotations

import json
import os
import signal
import subprocess
import threading
import time
from pathlib import Path

from .config import settings_manager
from .models import RuntimeInfo

# 进程识别用的可执行名片段。这里**不是**在启动浏览器, 而是在 /proc/<pid>/cmdline 里
# **认**出本服务此前起的浏览器 (孤儿扫描 / 重启接管)。启动路径只有 Chromix 一种形态。
# browser-gate: allow-legacy 误报 —— 此处是"按名字认出进程"的数据, 不是可执行路径,
# 本文件不 spawn 任何浏览器 (本段代码原在 browser.py, 那 2 处误报已登记在门禁基线里;
# 拆分后误报随代码搬到本文件)。启动逻辑见 browser.py / chromix。
BROWSER_CMD_MARKERS = (
    "chrome",
    "firefox",
)  # browser-gate: allow-legacy 进程名识别, 非启动

# 环境 id -> 运行态。**内存账本**, 与落盘的 runtime.json 互为备份。
RUNTIME: dict[str, RuntimeInfo] = {}
# 环境 id -> Popen。见模块头: 少了它就会产生僵尸。
_PROCS: dict[str, subprocess.Popen] = {}
_reaper_started = False


def runtime_path() -> Path:
    return Path(settings_manager.load().data_dir) / "runtime.json"


def save_runtime() -> None:
    """运行态落盘。

    为什么不能只放内存: RUNTIME 是进程内存, 服务一重启就与**所有**在跑的浏览器
    失联 —— 实测重启后 11 个 chrome 主进程既不被记录、也停不掉 (stop 对未记录的环境
    直接 return False), 而它们仍占着内存与账号 profile。
    落盘后重启可校验接管 (pid 活着 + 命令行里带本环境 profile), 认不出的才判孤儿。
    """
    try:
        payload = {eid: info.model_dump() for eid, info in RUNTIME.items()}
        p = runtime_path()
        p.parent.mkdir(parents=True, exist_ok=True)
        tmp = p.with_suffix(".tmp")
        tmp.write_text(json.dumps(payload, ensure_ascii=False, indent=2), "utf-8")
        os.chmod(tmp, 0o600)
        tmp.replace(p)
    except Exception:  # noqa: BLE001
        pass  # 磁盘问题不该让启停失败


def proc_cmdline(pid: int) -> str:
    """读 /proc/<pid>/cmdline (NUL 分隔 -> 空格)。取不到返回空串。"""
    try:
        raw = Path("/proc/%d/cmdline" % pid).read_bytes()
        return raw.decode("utf-8", "replace").replace(chr(0), " ")
    except OSError:
        return ""


def is_zombie(pid: int) -> bool:
    """只看状态字段, 不查存活 (供 scan/cleanup 用)。"""
    try:
        stat = Path("/proc/%d/stat" % pid).read_text("utf-8", "replace")
        return stat.rsplit(") ", 1)[1].split(" ", 1)[0] in ("Z", "X", "x")
    except (OSError, IndexError):
        return False


def pid_alive(pid: int) -> bool:
    """进程**可用**吗 (僵尸不算活着)。

    必须同时查两件事: os.kill(pid,0) 对僵尸也成功, 因为进程表项还在。
    只看它就是本次三个僵尸被判成运行中的直接原因。
    真值在 /proc/<pid>/stat 的第 3 字段 (state): Z/X 表示已终止待回收。
    """
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    try:
        stat = Path("/proc/%d/stat" % pid).read_text("utf-8", "replace")
        return stat.rsplit(") ", 1)[1].split(" ", 1)[0] not in ("Z", "X", "x")
    except (OSError, IndexError):
        return True  # 读不到就按存活处理, 不误杀


def scan_orphans() -> list[dict]:
    """找出**在跑但无人认领**的浏览器进程。

    判据 = 命令行里带本服务 data_dir 下的 profiles/<env_id>, 但 env_id 不在 RUNTIME 里。
    为什么要它: RUNTIME 是进程内存, 服务重启即失联; 实测重启后 11 个 chrome 主进程
    无人记录、也停不掉, 白占内存与账号 profile, 面板上还看不到它们。
    """
    profiles = Path(settings_manager.load().data_dir) / "profiles"
    out: list[dict] = []
    try:
        pids = [d for d in os.listdir("/proc") if d.isdigit()]
    except OSError:
        return out
    for d in pids:
        pid = int(d)
        cmd = proc_cmdline(pid)
        if not cmd or not any(m in cmd for m in BROWSER_CMD_MARKERS):
            continue
        if "--remote-debugging-port" not in cmd:
            continue
        if str(profiles) not in cmd:
            continue
        if is_zombie(pid):
            continue
        env_id = ""
        marker = str(profiles) + "/"
        if marker in cmd:
            env_id = cmd.split(marker, 1)[1].split("/", 1)[0].split(" ", 1)[0]
        if env_id in RUNTIME:
            continue
        # 同一个环境只报**主进程** (带 --remote-debugging-port 的那个)。
        # 渲染/GPU/utility 子进程共享同一 profile 路径, 全报出来会让"孤儿数"虚高,
        # 而它们会随主进程一起走 -- 报主进程即可, 清理时按进程组一次收干净。
        if "--remote-debugging-port=" not in cmd:
            continue
        # 主进程的 --type 参数为空/不存在; 子进程带 --type=renderer/gpu/utility。
        if "--type=" in cmd:
            continue
        out.append({"pid": pid, "env_id": env_id, "cmdline": cmd[:240]})
    return out


def cleanup_orphans() -> list[int]:
    """杀掉孤儿进程 (显式动作, 不在启动路径里自动执行)。返回被杀的 pid 列表。

    用进程组 SIGTERM: 主进程一死, 它的渲染/GPU 子进程跟着走。
    """
    killed: list[int] = []
    for o in scan_orphans():
        pid = int(o["pid"])
        if is_zombie(pid):
            continue
        try:
            os.killpg(os.getpgid(pid), signal.SIGTERM)
            killed.append(pid)
        except Exception:  # noqa: BLE001
            try:
                os.kill(pid, signal.SIGTERM)
                killed.append(pid)
            except Exception:  # noqa: BLE001
                pass
    return killed


def load_persisted_runtime() -> int:
    """启动时接管落盘的运行态。返回接管的条数。

    接管判据 (两个条件同时成立):
      a) pid 活着且不是僵尸;
      b) 它的命令行里带本环境 profile 目录 -- 防止 pid 被复用后误认。
    不满足的条目不进 RUNTIME (视为孤儿), 但**不自动杀** -- 杀进程是显式动作,
    由 /orphans 或 cleanup_orphans() 触发。
    """
    p = runtime_path()
    raw: dict = {}
    if p.exists():
        try:
            raw = json.loads(p.read_text("utf-8")) or {}
        except Exception:  # noqa: BLE001
            raw = {}
    n = 0
    for eid, info in (raw or {}).items():
        try:
            rt = RuntimeInfo(**info)
        except Exception:  # noqa: BLE001
            continue
        if not rt.pid or not pid_alive(rt.pid):
            continue
        cmd = proc_cmdline(rt.pid)
        if rt.profile_dir and rt.profile_dir in cmd:
            RUNTIME[eid] = rt
            n += 1
    # 落盘缺失/不全时的反推兜底 (硬性):
    # 首次升级、落盘被删、或本服务此前崩溃时, 只读落盘会得到空的 RUNTIME,
    # 而此刻**浏览器是活着的** -- 空 RUNTIME 会让 scan_orphans 把它们全判成孤儿,
    # 用户一点"清理孤儿"就杀掉正在用的环境 (含账号 profile 里的登录态)。
    # 所以反推: 从 /proc 认出属于本服务的在跑浏览器并接管, 让 RUNTIME 反映现实。
    for o in scan_orphans():
        eid = o.get("env_id") or ""
        if not eid or eid in RUNTIME:
            continue
        port = 0
        for tok in (o.get("cmdline") or "").split():
            if tok.startswith("--remote-debugging-port="):
                try:
                    port = int(tok.split("=", 1)[1])
                except ValueError:
                    port = 0
        RUNTIME[eid] = RuntimeInfo(
            pid=int(o["pid"]),
            debug_port=port,
            profile_dir=str(Path(settings_manager.load().data_dir) / "profiles" / eid),
            cdp_http="http://127.0.0.1:%d" % port if port else "",
        )
        n += 1
    if n:
        save_runtime()
    return n


def reap_finished() -> int:
    """回收已退出的子进程 (非阻塞)。返回本轮 reap 的数量。

    子进程只能由创建它的进程回收, 所以这一步必须留在父进程里;
    kill 对僵尸无效, 面板上无论怎么点都杀不掉就是因为它。
    """
    n = 0
    for eid, proc in list(_PROCS.items()):
        if proc.poll() is not None:
            _PROCS.pop(eid, None)
            n += 1
    return n


def start_reaper() -> None:
    """常驻回收线程: 确保子进程一退出就被 reap, 不留僵尸。"""
    global _reaper_started
    if _reaper_started:
        return
    _reaper_started = True

    def loop() -> None:
        while True:
            try:
                reap_finished()
            except Exception:  # noqa: BLE001
                pass
            time.sleep(1.0)

    threading.Thread(target=loop, name="hxbr-reaper", daemon=True).start()


def refresh_runtime() -> None:
    reap_finished()
    changed = False
    for env_id in list(RUNTIME.keys()):
        info = RUNTIME[env_id]
        if info.pid and not pid_alive(info.pid):
            RUNTIME.pop(env_id, None)
            _PROCS.pop(env_id, None)
            changed = True
    if changed:
        save_runtime()


def get_runtime(env_id: str) -> RuntimeInfo | None:
    refresh_runtime()
    return RUNTIME.get(env_id)
