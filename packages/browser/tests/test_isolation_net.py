"""沙箱出入口的单元测试: 出口上游 / relay 的 unix+direct 入口 / CDP 桥 / shim 转发。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md
"""

from __future__ import annotations

import asyncio
import os
import socket as so
import sys
import threading
import time

import pytest
from hx_browser import bridge, relay, sandbox_shim
from hx_browser.models import Environment
from hx_browser.proxy_link import DIRECT, Upstream, egress_upstream, parse_link

import hxbr_testenv  # noqa: F401  (把 src 放进 sys.path)


# ---------- 出口 ----------
def test_egress_upstream():
    env = Environment()
    assert egress_upstream(env.proxy, {}).scheme == DIRECT
    env.proxy.type = "system"
    up = egress_upstream(env.proxy, {"HTTPS_PROXY": "http://u:p@10.0.0.1:3128"})
    assert (up.scheme, up.host, up.port, up.username) == ("http", "10.0.0.1", 3128, "u")
    long_user = "x" * 900  # HTTP 网关常用 JWT 作用户名, 不受 SOCKS5 的 255 字节限制
    assert parse_link("http://%s:pw@h:1" % long_user)["username"] == long_user
    with pytest.raises(Exception):  # noqa: B017
        parse_link("socks5://%s:pw@h:1" % long_user)


def test_relay_unix_direct_and_bridge(tmp_path):
    async def main():
        async def site(r, w):
            head = await r.readuntil(b"\r\n\r\n")
            w.write(
                b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"
                if head.startswith(b"GET /p ")
                else b"HTTP/1.1 400 X\r\n\r\n"
            )
            await w.drain()
            w.close()

        srv = await asyncio.start_server(site, "127.0.0.1", 0)
        port = srv.sockets[0].getsockname()[1]
        path = str(tmp_path / "proxy.sock")
        await relay.ensure("env_u", Upstream(DIRECT, "", 0), unix_path=path)
        assert os.stat(path).st_mode & 0o077 == 0  # 只有属主能连
        r, w = await asyncio.open_unix_connection(path)
        w.write(b"GET http://127.0.0.1:%d/p HTTP/1.1\r\nHost: x\r\n\r\n" % port)
        await w.drain()
        assert (await r.read()).endswith(b"ok")  # direct 上游改写成 origin-form
        await bridge.ensure("env_u", 0, path)
        await relay.stop("env_u")  # 同时停掉 CDP 桥
        assert "env_u" not in bridge.BRIDGES and not os.path.exists(path)
        srv.close()

    asyncio.run(main())


def test_shim_forwarders(tmp_path):
    lst = so.socket(so.AF_UNIX)
    path = str(tmp_path / "up.sock")
    lst.bind(path)
    lst.listen(1)

    def serve():
        c, _ = lst.accept()
        c.sendall(b"pong:" + c.recv(10))
        c.close()

    threading.Thread(target=serve, daemon=True).start()
    probe = so.socket()
    probe.bind(("127.0.0.1", 0))
    port = probe.getsockname()[1]
    probe.close()
    sandbox_shim._tcp_to_unix(port, path)
    c = so.create_connection(("127.0.0.1", port), 3)
    c.sendall(b"ping")
    assert c.recv(20) == b"pong:ping"
    assert sandbox_shim.main(["--", sys.executable, "-c", "raise SystemExit(3)"]) == 3


def test_shim_cdp_forward_survives_idle(tmp_path, monkeypatch):
    """回归: unix->TCP 转发的连接不能带读超时, 否则空闲的 CDP WebSocket 会被断开。"""

    srv = so.socket()
    srv.bind(("127.0.0.1", 0))
    srv.listen(1)
    path = str(tmp_path / "cdp.sock")
    made = []
    real = so.create_connection

    def spy(*a, **k):
        made.append(real(*a, **k))
        return made[-1]

    monkeypatch.setattr(sandbox_shim.socket, "create_connection", spy)
    sandbox_shim._unix_to_tcp(path, srv.getsockname()[1])
    c = so.socket(so.AF_UNIX)
    c.connect(path)
    srv.accept()
    for _ in range(50):
        if made:
            break

        time.sleep(0.05)
    assert made and made[0].gettimeout() is None
