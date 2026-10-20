"""CDP 端口分配 —— 并发启动时端口不冲突 (2026-10-13 从 browser.py 拆出)。

Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md

## 为什么必须预留, 不能只探测

`_port_free` 只反映「**此刻**有没有人 bind」。而「分配端口」与「浏览器真的 bind
上去」之间有窗口期 (chrome 冷启动约 1 秒)。并发 start 3 个环境时, 三个都会在这个
窗口期里探测到同一个"空闲"号 —— 后起的 chrome 因 `--remote-debugging-port` 冲突
而启动失败 (实测症状: 3 个里只有 1 个真的起来, 另两个进程立刻退出)。

判据: 分配时**预留**, 停止/启动失败时**释放**。同一事实只在这一处定义。
漏掉释放会让端口池随启停次数单调泄漏 —— 表现为"用久了就分配不到端口"。
"""

from __future__ import annotations

import socket

_PROBE_TIMEOUT_S = 0.3
_PORTS_IN_USE: set[int] = set()


def port_free(port: int) -> bool:
    """此刻有没有人在该端口上监听。"""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.settimeout(_PROBE_TIMEOUT_S)
        return s.connect_ex(("127.0.0.1", port)) != 0


def next_port(base: int, index: int) -> int:
    """分配一个 CDP 端口 (从 base+index 起找空闲, 并排除本进程已预留的号)。"""
    port = base + max(index, 0)
    while not port_free(port) or port in _PORTS_IN_USE:
        port += 1
    _PORTS_IN_USE.add(port)
    return port


def release_port(port: int) -> None:
    """归还预留。"""
    _PORTS_IN_USE.discard(port)


def reserved() -> set[int]:
    """当前预留集 (只读快照, 供验收与排查)。"""
    return set(_PORTS_IN_USE)


def clear_reserved() -> None:
    """全清预留 (服务停机时用)。停掉所有浏览器后没有任何端口还该被占着。"""
    _PORTS_IN_USE.clear()
