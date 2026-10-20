"""每副本一个本地代理 relay: 浏览器连 127.0.0.1 无凭据入口, relay 注入凭据转发上游。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md
Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

为什么要它见 proxy_link.py 模块头 (Chromium 不解析 --proxy-server 里的 user:pass)。

## 协议
浏览器 -> relay: HTTP 代理协议 (CONNECT / absolute-URI); relay -> 上游: http / https (TLS
到代理) / socks5 (RFC 1928 + 1929) / direct (仅沙箱副本: 宿主直连, 沙箱内没有网卡)。

## 关键取舍

* **只绑 127.0.0.1**: relay 无鉴权, 绑外网卡等于把上游凭据借给整个局域网。
  沙箱副本只开仅属主可连的 Unix socket, 宿主上没有可借用的 TCP 入口。
* **明文请求强制 Connection: close**: Proxy-Authorization 只注入在每条连接的首个请求上;
  keep-alive 时后续请求不带凭据, 上游按请求鉴权 (如 squid) 会 407。一连接一请求最稳。
* **上游失败不回落直连**: 回落会让副本悄悄用真实出口 IP —— 隔离的意义就没了。
  上游 407 时给浏览器回 502 (而不是透传 407), 否则浏览器会弹出凭据输入框。
"""

from __future__ import annotations

import asyncio
import base64
import contextlib
import ipaddress
import logging
import os
import ssl
import struct

from . import bridge
from .proxy_link import DIRECT, SOCKS5, Upstream

log = logging.getLogger("hx_browser.relay")

LOOPBACK = "127.0.0.1"
MAX_HEADER = 64 * 1024
CONNECT_TIMEOUT = 15.0
HEADER_END = b"\r\n\r\n"
CRLF = b"\r\n"
BAD_GATEWAY = (
    b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
)
ESTABLISHED = b"HTTP/1.1 200 Connection established\r\n\r\n"
_HOP_HEADERS = (
    b"proxy-authorization",
    b"proxy-connection",
    b"connection",
    b"keep-alive",
)
# 隧道型上游: 明文请求也先建隧道, 再发 origin-form 请求
TUNNEL_SCHEMES = (SOCKS5, DIRECT)


class RelayError(Exception):
    pass


async def _read_header(reader: asyncio.StreamReader) -> bytes:
    try:
        data = await reader.readuntil(HEADER_END)
    except asyncio.LimitOverrunError as e:
        raise RelayError("header too large") from e
    if len(data) > MAX_HEADER:
        raise RelayError("header too large")
    return data


def _split_target(value: str, default_port: int) -> tuple[str, int]:
    if value.startswith("["):
        host, _, rest = value[1:].partition("]")
        port = rest.lstrip(":")
    else:
        host, _, port = (
            value.rpartition(":") if value.count(":") == 1 else (value, "", "")
        )
    if not port:
        return host, default_port
    if not port.isdigit() or not 0 < int(port) < 65536:
        raise RelayError("bad port")
    return host, int(port)


def _auth_header(up: Upstream) -> bytes:
    if not up.username:
        return b""
    token = base64.b64encode(("%s:%s" % (up.username, up.password)).encode()).decode()
    return ("Proxy-Authorization: Basic %s\r\n" % token).encode()


async def _open_upstream(up: Upstream):
    ctx = ssl.create_default_context() if up.scheme == "https" else None
    return await asyncio.wait_for(
        asyncio.open_connection(
            up.host, up.port, ssl=ctx, server_hostname=up.host if ctx else None
        ),
        CONNECT_TIMEOUT,
    )


async def _socks5_connect(up: Upstream, host: str, port: int):
    reader, writer = await _open_upstream(up)
    methods = b"\x00\x02" if up.username else b"\x00"
    writer.write(b"\x05" + bytes([len(methods)]) + methods)
    await writer.drain()
    ver, method = await reader.readexactly(2)
    if ver != 5 or method == 0xFF:
        raise RelayError("socks5 no acceptable auth method")
    if method == 2:
        user, pw = up.username.encode(), up.password.encode()
        writer.write(b"\x01" + bytes([len(user)]) + user + bytes([len(pw)]) + pw)
        await writer.drain()
        if (await reader.readexactly(2))[1] != 0:
            raise RelayError("socks5 auth rejected")
    try:
        addr = ipaddress.ip_address(host)
        atyp = b"\x01" if addr.version == 4 else b"\x04"
        dest = atyp + addr.packed
    except ValueError:
        name = host.encode("idna")
        dest = b"\x03" + bytes([len(name)]) + name  # 远端解析域名: DNS 不在本机泄露
    writer.write(b"\x05\x01\x00" + dest + struct.pack("!H", port))
    await writer.drain()
    head = await reader.readexactly(4)
    if head[1] != 0:
        raise RelayError("socks5 connect failed: code %d" % head[1])
    skip = {1: 4, 4: 16}.get(head[3])
    if skip is None:
        skip = (await reader.readexactly(1))[0]
    await reader.readexactly(skip + 2)
    return reader, writer


async def _http_connect(up: Upstream, host: str, port: int):
    reader, writer = await _open_upstream(up)
    target = ("[%s]:%d" % (host, port)) if ":" in host else ("%s:%d" % (host, port))
    writer.write(
        ("CONNECT %s HTTP/1.1\r\nHost: %s\r\n" % (target, target)).encode()
        + _auth_header(up)
        + CRLF
    )
    await writer.drain()
    status = (await _read_header(reader)).split(b" ", 2)
    if len(status) < 2 or status[1] != b"200":
        writer.close()
        raise RelayError("upstream CONNECT refused: %s" % status[1:2])
    return reader, writer


async def open_tunnel(up: Upstream, host: str, port: int):
    if up.scheme == DIRECT:  # 沙箱 direct/system 副本: 宿主 relay 是它唯一的出口
        return await asyncio.wait_for(
            asyncio.open_connection(host, port), CONNECT_TIMEOUT
        )
    if up.scheme == SOCKS5:
        return await _socks5_connect(up, host, port)
    return await _http_connect(up, host, port)


async def _pipe(src: asyncio.StreamReader, dst: asyncio.StreamWriter) -> None:
    with contextlib.suppress(Exception):
        while chunk := await src.read(65536):
            dst.write(chunk)
            await dst.drain()
    with contextlib.suppress(Exception):
        dst.close()


def _rewrite_plain(head: bytes, up: Upstream) -> tuple[bytes, str, int]:
    """明文请求: 去掉逐跳头, 强制 close; socks 上游改写成 origin-form。"""
    lines = head[: -len(HEADER_END)].split(CRLF)
    method, uri, version = lines[0].split(b" ", 2)
    url = uri.decode("latin-1")
    if not url.startswith("http://"):
        raise RelayError("only absolute http:// URIs are proxied in plain mode")
    hostport, _, path = url[len("http://") :].partition("/")
    host, port = _split_target(hostport, 80)
    kept = [
        ln
        for ln in lines[1:]
        if ln.split(b":", 1)[0].strip().lower() not in _HOP_HEADERS
    ]
    first = b" ".join(
        (
            method,
            uri if up.scheme not in TUNNEL_SCHEMES else ("/" + path).encode("latin-1"),
            version,
        )
    )
    extra = _auth_header(up) if up.scheme not in TUNNEL_SCHEMES else b""
    body = CRLF.join([first, *kept]) + CRLF + extra + b"Connection: close\r\n\r\n"
    return body, host, port


async def _handle(
    up: Upstream, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
) -> None:
    try:
        head = await asyncio.wait_for(_read_header(reader), CONNECT_TIMEOUT)
        method = head.split(b" ", 1)[0].upper()
        if method == b"CONNECT":
            host, port = _split_target(head.split(b" ", 2)[1].decode("latin-1"), 443)
            u_reader, u_writer = await open_tunnel(up, host, port)
            writer.write(ESTABLISHED)
        else:
            body, host, port = _rewrite_plain(head, up)
            if up.scheme in TUNNEL_SCHEMES:
                u_reader, u_writer = await open_tunnel(up, host, port)
            else:
                u_reader, u_writer = await _open_upstream(up)
            u_writer.write(body)
        await writer.drain()
        await asyncio.gather(_pipe(reader, u_writer), _pipe(u_reader, writer))
    except Exception as e:  # noqa: BLE001 — 任何上游失败都回 502, 绝不回落直连
        log.debug("relay to %s failed: %s", up.redacted(), e)
        with contextlib.suppress(Exception):
            writer.write(BAD_GATEWAY)
            await writer.drain()
    finally:
        with contextlib.suppress(Exception):
            writer.close()


class Relay:
    def __init__(self, up: Upstream):
        self.upstream = up
        self.server: asyncio.AbstractServer | None = None
        self.port = 0
        self.unix_path = ""

    async def start_unix(self, path: str) -> None:
        """沙箱副本的入口: 只有仅属主可连的 Unix socket, 宿主上不开任何 TCP 端口可供借用。"""
        cb = lambda r, w: _handle(self.upstream, r, w)  # noqa: E731
        self.server = await bridge.private_unix_server(cb, path, MAX_HEADER * 2)
        self.unix_path = path

    async def start(self, port: int = 0) -> int:
        self.server = await asyncio.start_server(
            lambda r, w: _handle(self.upstream, r, w),
            LOOPBACK,
            port,
            limit=MAX_HEADER * 2,
        )
        self.port = self.server.sockets[0].getsockname()[1]
        return self.port

    async def close(self) -> None:
        if self.server is not None:
            self.server.close()
            with contextlib.suppress(Exception):
                await asyncio.wait_for(self.server.wait_closed(), 2)
            self.server = None
        if self.unix_path:
            with contextlib.suppress(OSError):
                os.unlink(self.unix_path)


RELAYS: dict[str, Relay] = {}


async def ensure(env_id: str, up: Upstream, port: int = 0, unix_path: str = "") -> int:
    """给副本起 (或复用) relay, 返回本地端口 (unix 入口时为 0)。上游变了就重建。"""
    cur = RELAYS.get(env_id)
    if (
        cur is not None
        and cur.server is not None
        and cur.upstream == up
        and cur.unix_path == unix_path
        and (unix_path or not port or cur.port == port)
    ):
        return cur.port
    await stop(env_id)
    relay = Relay(up)
    if unix_path:
        await relay.start_unix(unix_path)
        RELAYS[env_id] = relay
        log.info("env=%s relay unix:%s -> %s", env_id, unix_path, up.redacted())
        return 0
    got = await relay.start(port)
    RELAYS[env_id] = relay
    log.info("env=%s relay 127.0.0.1:%d -> %s", env_id, got, up.redacted())
    return got


async def stop(env_id: str) -> None:
    await bridge.stop(
        env_id
    )  # 沙箱副本的 CDP 桥与 relay 同生共死 (所有停止路径都走这里)
    relay = RELAYS.pop(env_id, None)
    if relay is not None:
        await relay.close()


async def stop_all() -> None:
    for env_id in list(RELAYS):
        await stop(env_id)
