"""出口侧握手探针: ClientHello 解析, 与宣称内核的比对判定, relay 旁路捕获不改字节. 全部进程内, 不出网.

Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-egress-tls-probe.md
"""

from __future__ import annotations

import asyncio
import random
import ssl
import struct

import hxbr_testenv  # noqa: F401
from hx_browser import relay, tlshello, tlsprobe
from hx_browser.proxy_link import DIRECT, Upstream

CHROME_UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36"
FIREFOX_UA = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
CHROME_CIPHERS = [0x1301, 0x1302, 0x1303, 0xC02B, 0xC02F, 0xC02C, 0xC030, 0xCCA9, 0xCCA8, 0xC013, 0xC014, 0x009C,
                  0x009D, 0x002F, 0x0035]  # fmt: skip
SIGALGS = [0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501, 0x0806, 0x0601]


def _ext(t: int, body: bytes) -> bytes:
    return struct.pack("!HH", t, len(body)) + body


def _u16s(vals, width=2) -> bytes:
    raw = b"".join(struct.pack("!H", v) for v in vals)
    return len(raw).to_bytes(width, "big") + raw


def build_hello(sni="example.com", grease=True, groups=(0x11EC, 0x001D, 0x0017, 0x0018), alps=0x44CD, ech=True,
                record_limit=False, seed=0) -> bytes:  # fmt: skip
    """最小可解析的 ClientHello, 中间扩展按 seed 置换, 模拟 Chrome 逐连接乱序."""
    name = sni.encode()
    middle = [
        _ext(0x0000, struct.pack("!HBH", len(name) + 3, 0, len(name)) + name),
        _ext(0x000A, _u16s(([0x4A4A] if grease else []) + list(groups))),
        _ext(0x000B, b"\x01\x00"),
        _ext(0x000D, _u16s(SIGALGS)),
        _ext(0x0010, struct.pack("!H", 12) + b"\x02h2\x08http/1.1"),
        _ext(0x002B, bytes([4]) + struct.pack("!HH", 0x0304, 0x0303)),
        _ext(0x0033, struct.pack("!H", 8) + struct.pack("!HH", groups[0], 0) + struct.pack("!HH", groups[1], 0)),
        _ext(0x001B, b"\x02\x00\x02"),
        _ext(0xFF01, b"\x00"),
    ]
    if alps:
        middle.append(_ext(alps, b"\x00\x03\x02h2"))
    if ech:
        middle.append(_ext(0xFE0D, b"\x00" * 8))
    if record_limit:
        middle.append(_ext(0x001C, b"\x40\x01"))
    random.Random(seed).shuffle(middle)
    exts = ([_ext(0x0A0A, b"")] if grease else []) + middle + ([_ext(0x1A1A, b"\x00")] if grease else [])
    ciphers = ([0x2A2A] if grease else []) + CHROME_CIPHERS
    ext_raw = b"".join(exts)
    body = struct.pack("!H", 0x0303) + bytes(32) + b"\x00" + _u16s(ciphers) + b"\x01\x00"
    body += struct.pack("!H", len(ext_raw)) + ext_raw
    hs = b"\x01" + len(body).to_bytes(3, "big") + body
    return b"\x16\x03\x01" + struct.pack("!H", len(hs)) + hs


def recs(n=3, **kw):
    return [tlshello.fingerprint(tlshello.parse(build_hello(seed=i, **kw))) for i in range(n)]


def test_parse_and_order_independent_keys():
    a, b = recs(2)
    assert a["ext_order"] != b["ext_order"]
    assert a["ja4_r"] == b["ja4_r"] and a["ja3n_hash"] == b["ja3n_hash"] and a["ja4"] == b["ja4"]
    assert a["ja4"].startswith("t13d1511h2_8daaf6152771_")
    assert a["sni"] == "example.com" and a["grease"] and a["alpn"] == ["h2", "http/1.1"]


def test_partial_and_non_tls_input():
    raw = build_hello()
    assert tlshello.parse(raw[:20]) == tlshello.NEED_MORE
    assert tlshello.parse(b"GET / HTTP/1.1\r\n\r\n") == tlshello.NOT_TLS
    split = raw[:5 + 100]
    assert tlshello.parse(split) == tlshello.NEED_MORE


def test_record_fragmented_hello():
    raw = build_hello()
    hs = raw[5:]
    two = b"\x16\x03\x01" + struct.pack("!H", 50) + hs[:50] + b"\x16\x03\x01" + struct.pack("!H", len(hs) - 50) + hs[50:]
    assert isinstance(tlshello.parse(two), dict)


def test_openssl_hello_is_parsed():
    ctx = ssl.create_default_context()
    out = ssl.MemoryBIO()
    s = ctx.wrap_bio(ssl.MemoryBIO(), out, server_hostname="example.org")
    try:
        s.do_handshake()
    except ssl.SSLWantReadError:
        pass
    h = tlshello.parse(out.read())
    assert isinstance(h, dict) and h["sni"] == "example.org"
    assert not tlshello.fingerprint(h)["grease"]


def test_matching_chrome_is_healthy():
    out = tlsprobe.assess(recs(), CHROME_UA % 146, "Chrome/146.0.7680.31", "blink")
    assert out["status"] == tlsprobe.HEALTHY, out
    assert out["h2"]["observable"] is False and "H2" in out["value"]


def test_ua_major_differs_from_real_kernel_is_red():
    out = tlsprobe.assess(recs(), CHROME_UA % 150, "Chrome/146.0.7680.31", "blink")
    assert out["status"] == tlsprobe.ERROR and "150" in out["detail"] and "146" in out["detail"]


def test_firefox_handshake_with_chrome_ua_is_red():
    ff = recs(grease=False, alps=0, ech=False, record_limit=True, groups=(0x001D, 0x0017, 0x0018))
    out = tlsprobe.assess(ff, CHROME_UA % 146, "", "gecko")
    assert out["status"] == tlsprobe.ERROR and "gecko" in out["detail"]


def test_old_claim_with_newer_handshake_is_red():
    out = tlsprobe.assess(recs(), CHROME_UA % 120, "Chrome/120.0.1.1", "blink")
    assert out["status"] == tlsprobe.ERROR and "X25519MLKEM768" in out["detail"]


def test_missing_expected_feature_is_warning():
    out = tlsprobe.assess(recs(groups=(0x001D, 0x0017, 0x0018)), CHROME_UA % 146, "Chrome/146.0.1.1", "blink")
    assert out["status"] == tlsprobe.WARNING and "X25519MLKEM768" in out["detail"]


def test_fixed_extension_order_is_red_for_modern_chrome():
    same = [tlshello.fingerprint(tlshello.parse(build_hello(seed=7))) for _ in range(3)]
    out = tlsprobe.assess(same, CHROME_UA % 146, "Chrome/146.0.1.1", "blink")
    assert out["status"] == tlsprobe.ERROR and "顺序" in out["detail"]


def test_missing_evidence_is_unknown():
    assert tlsprobe.assess([], CHROME_UA % 146, "Chrome/146.0.1.1")["status"] == tlsprobe.UNKNOWN
    assert tlsprobe.assess(recs(), "", "Chrome/146.0.1.1")["status"] == tlsprobe.UNKNOWN
    assert tlsprobe.assess(recs(), CHROME_UA % 146, "", "blink")["status"] == tlsprobe.UNKNOWN


def test_relay_taps_hello_without_changing_bytes():
    async def go():
        got = bytearray()

        async def sink(r, w):
            got.extend(await r.read(-1))
            w.close()

        srv = await asyncio.start_server(sink, "127.0.0.1", 0)
        tport = srv.sockets[0].getsockname()[1]
        port = await relay.ensure("env_tls1", Upstream(DIRECT, "", 0, "", ""))
        try:
            hello = build_hello(sni="probe.test")
            r, w = await asyncio.open_connection("127.0.0.1", port)
            w.write(b"CONNECT 127.0.0.1:%d HTTP/1.1\r\nHost: x\r\n\r\n" % tport)
            await w.drain()
            assert b"200" in await r.readuntil(b"\r\n\r\n")
            for i in range(0, len(hello), 7):  # 逐段写入, 覆盖跨 chunk 拼接
                w.write(hello[i : i + 7])
                await w.drain()
            w.write(b"tail")
            w.close()
            for _ in range(50):
                if len(got) >= len(hello) + 4:
                    break
                await asyncio.sleep(0.05)
            assert bytes(got) == hello + b"tail"
            seen = tlsprobe.seen("env_tls1")
            assert len(seen) == 1 and seen[0]["sni"] == "probe.test" and seen[0]["target"].endswith(":%d" % tport)
        finally:
            await relay.stop("env_tls1")
            srv.close()
        assert tlsprobe.seen("env_tls1") == []

    asyncio.run(go())


def test_temporary_probe_relay_records_nothing():
    assert tlsprobe.tap_for("", "x:443") is None
    assert relay.Relay(Upstream(DIRECT, "", 0, "", "")).env_id == ""
