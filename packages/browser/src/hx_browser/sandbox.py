"""可选的进程沙箱 (Linux bubblewrap) —— 副本隔离的第二层。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

## 隔离分两层, 边界如实写清

    第一层 process (默认, 所有副本)
        独立 user-data-dir / 独立 CDP 端口 / 独立进程组 / 独立本地代理 relay / 独立指纹。
        同一个 Linux 用户下的进程**互相可见可读**。

    第二层 sandbox (isolation="sandbox", 需要 bwrap)
        * PID / IPC / UTS / cgroup 命名空间; 主机名统一为 localhost
        * **网络命名空间**: 沙箱内只有回环口。出网唯一路径是 shim -> Unix socket ->
          宿主 relay -> 上游 (direct/system 副本也经宿主 relay)。WebRTC UDP、
          不走代理的直连、访问宿主 127.0.0.1 上的服务 —— 全部无路可走。
        * $HOME 换成空 tmpfs, 只绑本副本 profile; 其他副本与用户家目录不可见
        * **显示层**: 只给本副本专属的 X server (Xvfb / Xephyr 嵌套) 或 Wayland socket,
          见 display_iso.py
        * **字体**: 系统字体目录被遮蔽, 只绑白名单 (prefs.font_isolation), 见 fontiso.py
        * **seccomp 拒绝表**: 挂载/内核模块/bpf/userfaultfd/io_uring/keyctl/ptrace 等
          提权常用系统调用返回 EPERM, 见 seccomp_bpf.py
        * GPU 设备默认可见 (WebGL 指纹真实); prefs.sandbox_gpu=false 时拿掉 /dev/dri

## 仍然共享 (不夸大)

    内核本身: 被允许的系统调用里的内核漏洞仍可能逃逸。seccomp 缩小攻击面, 不消灭它;
    要彻底挡内核级逃逸只能用虚拟机 (gVisor / Kata / microVM)。
    shared 显示模式: 用户显式选择共享桌面 X server 时, 显示层不隔离。

缺 bwrap / Xephyr / Wayland socket / fontconfig 时**拒绝启动**, 绝不静默降级。
"""

from __future__ import annotations

import os
import shutil
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path

TOOL_BWRAP = "bwrap"
RO_SYSTEM = (
    "/usr",
    "/bin",
    "/sbin",
    "/lib",
    "/lib64",
    "/lib32",
    "/etc",
    "/opt",
    "/sys",
)
RO_BIND_TRY = "--ro-bind-try"
EXTRA_RO_ENV = "HXBR_SANDBOX_RO"
SOCK_DIR = "/run/hx"  # 沙箱内: proxy.sock / cdp.sock
SHIM_DIR = "/run/hx-shim"  # 沙箱内: shim 脚本 (只读)
PROXY_SOCK, CDP_SOCK = "proxy.sock", "cdp.sock"
SANDBOX_PROXY_PORT = 3128  # 沙箱自己的网络命名空间里的端口, 与宿主端口无关
SHIM_FILES = ("sandbox_shim.py", "seccomp_bpf.py")
PRIVATE_DIR = 0o700  # 运行目录只给本用户: 里面是可直达浏览器 CDP 的 socket


class SandboxUnavailable(RuntimeError):
    pass


@dataclass
class SandboxSpec:
    """一次沙箱启动所需的全部输入 (由 launch_hooks 组装)。"""

    profile_dir: Path
    run_dir: Path
    cdp_port: int
    extra_ro: list[str] = field(default_factory=list)
    display_args: list[str] = field(default_factory=list)
    font_args: list[str] = field(default_factory=list)
    gpu: bool = True
    seccomp: bool = True


def available() -> bool:
    return bool(shutil.which(TOOL_BWRAP))


def status() -> dict:
    path = shutil.which(TOOL_BWRAP) or ""
    return {
        "available": bool(path),
        "tool": path,
        "isolates": [
            "pid", "ipc", "uts", "network", "home", "other-profiles",
            "display", "fonts", "syscalls(seccomp-denylist)",
        ],
        "shared": ["kernel", "gpu(optional)", "display(shared mode only)"],
    }  # fmt: skip


def run_dir(env_id: str) -> Path:
    """宿主侧的副本运行目录 (0700): 放 Unix socket 与 shim 副本。"""
    base = Path(tempfile.gettempdir()) / ("hx-browser-%d" % os.getuid())
    base.mkdir(mode=PRIVATE_DIR, exist_ok=True)
    st = os.lstat(
        base
    )  # /tmp 共享: 防别的用户抢先建同名目录或软链 (预置 socket 劫持 CDP)
    if base.is_symlink() or st.st_uid != os.getuid():
        raise SandboxUnavailable("沙箱运行目录 %s 不属于当前用户, 已拒绝启动" % base)
    os.chmod(base, PRIVATE_DIR)
    d = base / env_id
    (d / "sock").mkdir(mode=PRIVATE_DIR, parents=True, exist_ok=True)
    shim = d / "shim"
    shim.mkdir(mode=PRIVATE_DIR, exist_ok=True)
    here = Path(__file__).parent
    for name in SHIM_FILES:  # 复制而非绑包目录: 包里的 locale.py 等会遮住标准库
        shutil.copyfile(here / name, shim / name)
    return d


def host_proxy_sock(rd: Path) -> str:
    return str(rd / "sock" / PROXY_SOCK)


def host_cdp_sock(rd: Path) -> str:
    return str(rd / "sock" / CDP_SOCK)


def _interpreter() -> tuple[str, str]:
    """沙箱内跑 shim 的解释器 (venv 不可见, 用基础解释器) 与需要只读可见的前缀。"""
    exe = getattr(sys, "_base_executable", "") or sys.executable
    return str(Path(exe).resolve()), str(Path(sys.base_prefix).resolve())


def _covered(path: str) -> bool:
    return any(path == d or path.startswith(d + "/") for d in RO_SYSTEM)


def wrap(argv: list[str], spec: SandboxSpec, env: dict | None = None) -> list[str]:
    tool = shutil.which(TOOL_BWRAP)
    if not tool:
        raise SandboxUnavailable(
            "副本要求 sandbox 隔离, 但本机未安装 bubblewrap (bwrap); 已拒绝启动, 未降级"
        )
    env = env or {}
    home = env.get("HOME") or os.environ.get("HOME") or "/tmp/hx-home"
    out = [tool, "--die-with-parent", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
           "--unshare-net", "--unshare-cgroup-try", "--hostname", "localhost"]  # fmt: skip
    for d in RO_SYSTEM:
        out += [RO_BIND_TRY, d, d]
    out += ["--dev", "/dev"]
    if spec.gpu:
        out += ["--dev-bind-try", "/dev/dri", "/dev/dri"]
    out += [
        "--proc",
        "/proc",
        "--tmpfs",
        "/tmp",
        "--tmpfs",
        home,
        "--setenv",
        "HOME",
        home,
    ]
    out += spec.font_args + spec.display_args
    python, prefix = _interpreter()
    configured = [x for x in os.environ.get(EXTRA_RO_ENV, "").split(":") if x]
    for path in [*spec.extra_ro, *configured, prefix]:
        p = str(Path(path).resolve())
        if not _covered(p):
            out += [RO_BIND_TRY, p, p]
    real = str(spec.profile_dir.resolve())
    out += ["--bind", real, real]
    if str(spec.profile_dir) != real:  # 软链引用导入的 profile: 两个路径都要可达
        out += ["--symlink", real, str(spec.profile_dir)]
    out += ["--bind", str(spec.run_dir / "sock"), SOCK_DIR]
    out += ["--ro-bind", str(spec.run_dir / "shim"), SHIM_DIR]
    shim = [python, "-S", "-E", SHIM_DIR + "/sandbox_shim.py",
            "--proxy-port", str(SANDBOX_PROXY_PORT),
            "--proxy-sock", SOCK_DIR + "/" + PROXY_SOCK,
            "--cdp-port", str(spec.cdp_port),
            "--cdp-sock", SOCK_DIR + "/" + CDP_SOCK]  # fmt: skip
    if spec.seccomp:
        shim.append("--seccomp")
    return [*out, "--", *shim, "--", *argv]
