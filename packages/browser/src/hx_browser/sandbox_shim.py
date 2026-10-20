"""沙箱内的 1 号业务进程: 打通「仅有的两条」出入口, 装 seccomp, 再起浏览器。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

沙箱用独立网络命名空间 (只有回环口, 没有任何网卡), 于是浏览器**物理上**只能经
下面两条 Unix socket 与外界通信 —— 不存在「某个请求兼容代理直连」的路径:

    浏览器 --TCP--> 127.0.0.1:<proxy_port> (本 shim) --unix--> proxy.sock (宿主 relay) -> 上游
    宿主 CDP 客户端 -> 127.0.0.1:<cdp_port> (宿主 bridge) --unix--> cdp.sock (本 shim)
                     --TCP--> 127.0.0.1:<cdp_port> (沙箱内浏览器)

WebRTC 的 UDP、DNS 预解析、任何不走代理的连接在沙箱里都**无路可走** (ENETUNREACH)。

只用标准库, 由宿主的基础解释器以 ``-I -S`` 直接运行本文件 (沙箱内看不到 venv)。
"""

from __future__ import annotations

import argparse
import os
import signal
import socket
import subprocess
import sys
import threading

try:  # 以文件方式运行 (沙箱内) 时没有包上下文
    from . import seccomp_bpf
except ImportError:  # pragma: no cover - 只在沙箱内走到
    import seccomp_bpf  # type: ignore[no-redef]

LOOPBACK = "127.0.0.1"
CHUNK = 65536


def _pipe(src: socket.socket, dst: socket.socket) -> None:
    try:
        while data := src.recv(CHUNK):
            dst.sendall(data)
    except OSError:
        pass
    finally:
        for s in (src, dst):
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass


def _serve(listener: socket.socket, connect) -> None:
    while True:
        try:
            client, _ = listener.accept()
        except OSError:
            return
        try:
            upstream = connect()
        except OSError:
            client.close()
            continue
        for a, b in ((client, upstream), (upstream, client)):
            threading.Thread(target=_pipe, args=(a, b), daemon=True).start()


def _tcp_to_unix(port: int, path: str) -> None:
    lst = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    lst.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    lst.bind((LOOPBACK, port))
    lst.listen(128)

    def connect() -> socket.socket:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.connect(path)
        return s

    threading.Thread(target=_serve, args=(lst, connect), daemon=True).start()


def _unix_to_tcp(path: str, port: int) -> None:
    if os.path.exists(path):
        os.unlink(path)
    lst = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    old = os.umask(0o077)  # socket 只给本用户: 同机其他用户连不上这个 CDP
    try:
        lst.bind(path)
    finally:
        os.umask(old)
    lst.listen(64)

    def connect() -> socket.socket:
        s = socket.create_connection((LOOPBACK, port), 5)
        # 连接超时不能留成读超时: CDP WebSocket 空闲 5 秒就会被当成断线 (实测踩到)
        s.settimeout(None)
        return s

    threading.Thread(target=_serve, args=(lst, connect), daemon=True).start()


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--proxy-port", type=int, default=0)
    ap.add_argument("--proxy-sock", default="")
    ap.add_argument("--cdp-port", type=int, default=0)
    ap.add_argument("--cdp-sock", default="")
    ap.add_argument("--seccomp", action="store_true")
    ap.add_argument("cmd", nargs=argparse.REMAINDER)
    a = ap.parse_args(argv)
    cmd = a.cmd[1:] if a.cmd[:1] == ["--"] else a.cmd
    if not cmd:
        ap.error("missing browser command")
    if a.proxy_port and a.proxy_sock:
        _tcp_to_unix(a.proxy_port, a.proxy_sock)
    if a.cdp_port and a.cdp_sock:
        _unix_to_tcp(a.cdp_sock, a.cdp_port)
    # 过滤器只装在浏览器进程树上; shim 自己的转发线程不受影响
    pre = seccomp_bpf.apply if a.seccomp else None
    proc = subprocess.Popen(cmd, preexec_fn=pre, start_new_session=True)

    def forward(signum, _frame):
        try:
            os.killpg(proc.pid, signum)
        except OSError:
            pass

    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, forward)
    return proc.wait()


if __name__ == "__main__":
    sys.exit(main())
