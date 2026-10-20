"""沙箱内的 seccomp 拒绝表 —— 缩小「浏览器 0day -> 内核提权」可用的内核攻击面。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

## 为什么是拒绝表而不是白名单

浏览器自己 (Chromium/Firefox) 已经给渲染进程装了严格的 seccomp 白名单; 这里补的是
**整个浏览器进程树**的外层: 浏览器正常工作从不需要、却是历年内核提权常用入口的
系统调用 (挂载、内核模块、bpf、userfaultfd、io_uring、keyctl、ptrace 他进程 ...)。
白名单要跟着浏览器版本追系统调用, 一漏就是启动即崩; 拒绝表只挡「不该出现」的那批。

被拒绝的调用返回 EPERM (不是杀进程): 浏览器的特性探测 (如 io_uring) 会自然回落。
架构不符 / x32 ABI 一律杀进程 —— 否则换个 ABI 就能兼容号码表。

## 边界 (如实写)

seccomp 不能防「被允许的系统调用里的内核漏洞」; 它只把攻击面从全部系统调用缩到
浏览器真正用到的那部分。要彻底挡内核级逃逸只能上虚拟机 (gVisor/Kata/microVM)。

本模块只用标准库: 它在沙箱内由 sandbox_shim 在 exec 浏览器前加载。
"""

from __future__ import annotations

import ctypes
import platform
import struct

PR_SET_NO_NEW_PRIVS = 38
PR_SET_SECCOMP = 22
SECCOMP_MODE_FILTER = 2
RET_ALLOW = 0x7FFF0000
RET_EPERM = 0x00050000 | 1
RET_KILL = 0x80000000
LD_ABS_W = 0x20
JEQ_K = 0x15
JGE_K = 0x35
RET_K = 0x06
X32_BIT = 0x40000000

# 共用名字 -> 各架构号码。名字表只有一份, 两种架构不会各自漏项。
DENY = (
    "mount", "umount2", "pivot_root", "swapon", "swapoff", "reboot",
    "kexec_load", "kexec_file_load", "init_module", "finit_module", "delete_module",
    "keyctl", "add_key", "request_key", "bpf", "perf_event_open", "userfaultfd",
    "ptrace", "process_vm_readv", "process_vm_writev",
    "open_by_handle_at", "name_to_handle_at", "acct", "quotactl", "syslog",
    "io_uring_setup", "io_uring_enter", "io_uring_register",
    "open_tree", "move_mount", "fsopen", "fsconfig", "fsmount", "fspick",
    "lookup_dcookie", "vhangup", "settimeofday", "clock_settime", "adjtimex",
    "clock_adjtime", "iopl", "ioperm", "uselib", "modify_ldt",
)  # fmt: skip

_COMMON = {
    "io_uring_setup": 425, "io_uring_enter": 426, "io_uring_register": 427,
    "open_tree": 428, "move_mount": 429, "fsopen": 430, "fsconfig": 431,
    "fsmount": 432, "fspick": 433,
}  # fmt: skip
ARCHES = {
    "x86_64": (0xC000003E, {
        "mount": 165, "umount2": 166, "pivot_root": 155, "swapon": 167,
        "swapoff": 168, "reboot": 169, "kexec_load": 246, "kexec_file_load": 320,
        "init_module": 175, "finit_module": 313, "delete_module": 176,
        "keyctl": 250, "add_key": 248, "request_key": 249, "bpf": 321,
        "perf_event_open": 298, "userfaultfd": 323, "ptrace": 101,
        "process_vm_readv": 310, "process_vm_writev": 311,
        "open_by_handle_at": 304, "name_to_handle_at": 303, "acct": 163,
        "quotactl": 179, "syslog": 103, "lookup_dcookie": 212, "vhangup": 153,
        "settimeofday": 164, "clock_settime": 227, "adjtimex": 159,
        "clock_adjtime": 305, "iopl": 172, "ioperm": 173, "uselib": 134,
        "modify_ldt": 154, **_COMMON,
    }),
    "aarch64": (0xC00000B7, {
        "mount": 40, "umount2": 39, "pivot_root": 41, "swapon": 224,
        "swapoff": 225, "reboot": 142, "kexec_load": 104, "kexec_file_load": 294,
        "init_module": 105, "finit_module": 273, "delete_module": 106,
        "keyctl": 219, "add_key": 217, "request_key": 218, "bpf": 280,
        "perf_event_open": 241, "userfaultfd": 282, "ptrace": 117,
        "process_vm_readv": 270, "process_vm_writev": 271,
        "open_by_handle_at": 265, "name_to_handle_at": 264, "acct": 89,
        "quotactl": 60, "syslog": 116, "lookup_dcookie": 18, "vhangup": 58,
        "settimeofday": 170, "clock_settime": 112, "adjtimex": 171,
        "clock_adjtime": 266, **_COMMON,
    }),
}  # fmt: skip
ARCH_ALIASES = {"amd64": "x86_64", "arm64": "aarch64"}


class SeccompUnsupported(RuntimeError):
    pass


def _ins(code: int, jt: int, jf: int, k: int) -> bytes:
    return struct.pack("=HBBI", code, jt, jf, k)


def machine() -> str:
    m = platform.machine().lower()
    return ARCH_ALIASES.get(m, m)


def denied_numbers(arch: str) -> list[int]:
    table = ARCHES[arch][1]
    return sorted({table[name] for name in DENY if name in table})


def build(arch: str | None = None) -> bytes:
    """生成 BPF 程序 (struct sock_filter 数组的原始字节)。"""
    arch = arch or machine()
    if arch not in ARCHES:
        raise SeccompUnsupported("seccomp 拒绝表不支持该架构: %s" % arch)
    audit_arch = ARCHES[arch][0]
    nrs = denied_numbers(arch)
    body = [_ins(LD_ABS_W, 0, 0, 0)]  # A = seccomp_data.nr
    if arch == "x86_64":
        body.append(_ins(JGE_K, 0, 0, X32_BIT))  # 跳转目标在下面回填 -> KILL
    for nr in nrs:
        body.append(_ins(JEQ_K, 0, 0, nr))  # 回填 -> EPERM
    body.append(_ins(RET_K, 0, 0, RET_ALLOW))
    body.append(_ins(RET_K, 0, 0, RET_EPERM))
    body.append(_ins(RET_K, 0, 0, RET_KILL))
    # 回填相对跳转: 第 i 条到目标 t 的 jt = t - i - 1
    i_eperm, i_kill = len(body) - 2, len(body) - 1
    for i, raw in enumerate(body):
        code, _jt, jf, k = struct.unpack("=HBBI", raw)
        if code == JGE_K:
            body[i] = _ins(code, i_kill - i - 1, jf, k)
        elif code == JEQ_K:
            body[i] = _ins(code, i_eperm - i - 1, jf, k)
    # 头部: 架构不符直接 KILL (A = seccomp_data.arch, 偏移 4)
    head_len = 2
    kill_from_head = head_len + i_kill - 1 - 1
    head = [_ins(LD_ABS_W, 0, 0, 4), _ins(JEQ_K, 0, kill_from_head, audit_arch)]
    prog = head + body
    if len(prog) > 255:
        raise SeccompUnsupported("BPF 程序过长")
    return b"".join(prog)


class _SockFprog(ctypes.Structure):
    _fields_ = [("len", ctypes.c_ushort), ("filter", ctypes.c_void_p)]


def apply() -> None:
    """给当前进程 (及其后代) 装上过滤器。只能在 exec 浏览器前、在子进程里调用。"""
    prog = build()
    buf = ctypes.create_string_buffer(prog, len(prog))
    fprog = _SockFprog(len(prog) // 8, ctypes.cast(buf, ctypes.c_void_p).value)
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0:
        raise OSError(ctypes.get_errno(), "prctl(NO_NEW_PRIVS) failed")
    if libc.prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, ctypes.byref(fprog), 0, 0) != 0:
        raise OSError(ctypes.get_errno(), "prctl(SECCOMP) failed")
