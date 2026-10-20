"""代理链接解析 + 本地 relay (凭据注入 / socks5 / 不回落直连) 的回归测试。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

relay 测试全部在进程内起假上游, 不出网: 假 HTTP 代理要求 Basic 认证,
假 socks5 要求用户名密码 —— 证明凭据确实由 relay 注入, 而浏览器侧无需任何凭据。
"""

from __future__ import annotations

import asyncio
import base64
import struct

import pytest

import hxbr_testenv  # noqa: F401 — 设 sys.path
from hx_browser import relay
from hx_browser.models import ProxyConfig
from hx_browser.proxy_link import (
    ProxyLinkError,
    chromium_proxy_args,
    firefox_proxy_prefs,
    parse_link,
    upstream_of,
)

DIRECT = "direct"
USER, PASSWORD = "ghreg-us03", "p@ss:word"
TARGET_BODY = b"hello-from-target"


@pytest.mark.parametrize(
    ("link", "expect"),
    [
        ("socks5://u:p@1.2.3.4:1080", ("socks5", "1.2.3.4", 1080, "u", "p")),
        ("socks5h://u:p@host.example:1080", ("socks5", "host.example", 1080, "u", "p")),
        ("http://127.0.0.1:7890", ("http", "127.0.0.1", 7890, "", "")),
        (
            "https://a%40b:c%3Ad@proxy.example:443",
            ("https", "proxy.example", 443, "a@b", "c:d"),
        ),
        ("1.2.3.4:8080", ("http", "1.2.3.4", 8080, "", "")),
        ("1.2.3.4:8080:user:pw", ("http", "1.2.3.4", 8080, "user", "pw")),
        ("user:pw@1.2.3.4:8080", ("http", "1.2.3.4", 8080, "user", "pw")),
        (
            "hxpxy://ghreg-tw01:secret",
            ("http", "127.0.0.1", 7890, "ghreg-tw01", "secret"),
        ),
        (
            "hxpxy://ghreg-tw01:secret@10.0.0.5:7890",
            ("http", "10.0.0.5", 7890, "ghreg-tw01", "secret"),
        ),
        ("[::1]:3128", ("http", "::1", 3128, "", "")),
    ],
)
def test_parse_link(link, expect):
    cfg = parse_link(link)
    assert (
        cfg["type"],
        cfg["host"],
        cfg["port"],
        cfg["username"],
        cfg["password"],
    ) == expect


@pytest.mark.parametrize(
    "link", ["", "ftp://x:1", "host", "1.2.3.4:99999", "http://bad host:80", "a:b:c"]
)
def test_parse_link_rejects(link):
    with pytest.raises(ProxyLinkError):
        parse_link(link)


def test_modes_and_args():
    assert parse_link("direct")["type"] == "direct"
    assert chromium_proxy_args(ProxyConfig(type=DIRECT), 0) == ["--no-proxy-server"]
    assert chromium_proxy_args(ProxyConfig(type="system"), 0) == []
    assert chromium_proxy_args(ProxyConfig(type="no_proxy"), 0) == []  # 既有语义不变
    custom = ProxyConfig(type="socks5", host="h", port=1, username="u", password="p")
    args = chromium_proxy_args(custom, 41551)
    assert args == ["--proxy-server=http://127.0.0.1:41551"]
    assert "u" not in " ".join(args) and "p@" not in " ".join(args)  # 凭据不进命令行
    assert firefox_proxy_prefs(ProxyConfig(type="system"), 0) == {
        "network.proxy.type": 5
    }
    assert firefox_proxy_prefs(custom, 41551)["network.proxy.ssl_port"] == 41551
    assert upstream_of(ProxyConfig(type=DIRECT)) is None


# ---------------- 假上游 ----------------
async def _target_server():
    async def handle(r, w):
        await r.readuntil(b"\r\n\r\n")
        w.write(
            b"HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n" % len(TARGET_BODY)
            + TARGET_BODY
        )
        await w.drain()
        w.close()

    srv = await asyncio.start_server(handle, "127.0.0.1", 0)
    return srv, srv.sockets[0].getsockname()[1]


async def _http_proxy(seen: list):
    """要求 Basic 认证的假 HTTP 代理 (只实现 CONNECT 与 absolute-URI GET)。"""
    token = base64.b64encode(("%s:%s" % (USER, PASSWORD)).encode())

    async def handle(r, w):
        head = await r.readuntil(b"\r\n\r\n")
        seen.append(head)
        if b"Proxy-Authorization: Basic " + token not in head:
            w.write(b"HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
            await w.drain()
            w.close()
            return
        line = head.split(b"\r\n", 1)[0].split(b" ")
        if line[0] == b"CONNECT":
            host, port = line[1].decode().rsplit(":", 1)
            ur, uw = await asyncio.open_connection(host, int(port))
            w.write(b"HTTP/1.1 200 Connection established\r\n\r\n")
        else:
            hostport = line[1].decode().split("/")[2]
            host, port = hostport.rsplit(":", 1)
            ur, uw = await asyncio.open_connection(host, int(port))
            uw.write(head)
        await w.drain()
        await asyncio.gather(relay._pipe(r, uw), relay._pipe(ur, w))

    srv = await asyncio.start_server(handle, "127.0.0.1", 0)
    return srv, srv.sockets[0].getsockname()[1]


async def _socks5_proxy(seen: list):
    async def handle(r, w):
        ver, n = await r.readexactly(2)
        methods = await r.readexactly(n)
        assert 2 in methods
        w.write(b"\x05\x02")
        await w.drain()
        _, ulen = await r.readexactly(2)
        user = await r.readexactly(ulen)
        plen = (await r.readexactly(1))[0]
        pw = await r.readexactly(plen)
        seen.append((user.decode(), pw.decode()))
        ok = user.decode() == USER and pw.decode() == PASSWORD
        w.write(b"\x01" + (b"\x00" if ok else b"\x01"))
        await w.drain()
        if not ok:
            w.close()
            return
        _, _, _, atyp = await r.readexactly(4)
        if atyp == 3:
            host = (await r.readexactly((await r.readexactly(1))[0])).decode()
        else:
            host = ".".join(str(b) for b in await r.readexactly(4))
        (port,) = struct.unpack("!H", await r.readexactly(2))
        seen.append(host)
        ur, uw = await asyncio.open_connection("127.0.0.1", port)
        w.write(b"\x05\x00\x00\x01" + bytes(4) + b"\x00\x00")
        await w.drain()
        await asyncio.gather(relay._pipe(r, uw), relay._pipe(ur, w))

    srv = await asyncio.start_server(handle, "127.0.0.1", 0)
    return srv, srv.sockets[0].getsockname()[1]


async def _via_relay(relay_port: int, request: bytes) -> bytes:
    r, w = await asyncio.open_connection("127.0.0.1", relay_port)
    w.write(request)
    await w.drain()
    data = await asyncio.wait_for(r.read(-1), 10)
    w.close()
    return data


def _run(coro):
    return asyncio.run(coro)


def test_relay_http_upstream_injects_credentials():
    async def go():
        seen: list = []
        tsrv, tport = await _target_server()
        psrv, pport = await _http_proxy(seen)
        port = await relay.ensure(
            "env_t1", relay.Upstream("http", "127.0.0.1", pport, USER, PASSWORD)
        )
        try:
            plain = await _via_relay(
                port, b"GET http://127.0.0.1:%d/x HTTP/1.1\r\nHost: a\r\n\r\n" % tport
            )
            r, w = await asyncio.open_connection("127.0.0.1", port)
            w.write(b"CONNECT 127.0.0.1:%d HTTP/1.1\r\nHost: x\r\n\r\n" % tport)
            await w.drain()
            assert (await r.readuntil(b"\r\n\r\n")).startswith(b"HTTP/1.1 200")
            w.write(b"GET / HTTP/1.1\r\nHost: a\r\n\r\n")
            await w.drain()
            tunneled = await asyncio.wait_for(r.read(-1), 10)
            w.close()
        finally:
            await relay.stop("env_t1")
            psrv.close()
            tsrv.close()
        assert TARGET_BODY in plain and TARGET_BODY in tunneled
        assert all(
            b"Connection: close" in h for h in seen if not h.startswith(b"CONNECT")
        )

    _run(go())


def test_relay_socks5_upstream_with_auth():
    async def go():
        seen: list = []
        tsrv, tport = await _target_server()
        ssrv, sport = await _socks5_proxy(seen)
        port = await relay.ensure(
            "env_t2", relay.Upstream("socks5", "127.0.0.1", sport, USER, PASSWORD)
        )
        try:
            plain = await _via_relay(
                port,
                b"GET http://localhost:%d/x HTTP/1.1\r\nHost: localhost\r\n\r\n"
                % tport,
            )
        finally:
            await relay.stop("env_t2")
            ssrv.close()
            tsrv.close()
        assert TARGET_BODY in plain
        assert (USER, PASSWORD) in seen
        assert "localhost" in seen  # 域名交给上游解析 (socks5h 语义), 本机不做 DNS

    _run(go())


def test_relay_wrong_credentials_gives_502_not_407():
    async def go():
        seen: list = []
        psrv, pport = await _http_proxy(seen)
        port = await relay.ensure(
            "env_t3", relay.Upstream("http", "127.0.0.1", pport, USER, "wrong")
        )
        try:
            out = await _via_relay(
                port, b"CONNECT example.com:443 HTTP/1.1\r\nHost: x\r\n\r\n"
            )
        finally:
            await relay.stop("env_t3")
            psrv.close()
        # 407 透传会让浏览器弹凭据框; 也绝不回落直连
        assert out.startswith(b"HTTP/1.1 502")

    _run(go())


def test_relay_binds_loopback_only_and_reuses():
    async def go():
        up = relay.Upstream("http", "127.0.0.1", 9, "", "")
        p1 = await relay.ensure("env_t4", up)
        p2 = await relay.ensure("env_t4", up)
        sock = relay.RELAYS["env_t4"].server.sockets[0].getsockname()[0]
        await relay.stop("env_t4")
        return p1, p2, sock

    p1, p2, host = _run(go())
    assert p1 == p2 and host == "127.0.0.1"
