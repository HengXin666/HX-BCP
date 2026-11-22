"""TLS ClientHello 解析与顺序无关指纹 (ja4, ja4_r, ja3n), 纯函数, 不出网.

Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-egress-tls-probe.md
"""

from __future__ import annotations

import hashlib
import struct

RECORD_HANDSHAKE = 0x16
HELLO_TYPE = 0x01
MAX_HELLO = 32 * 1024
EXT_SNI = 0x0000
EXT_GROUPS = 0x000A
EXT_POINTS = 0x000B
EXT_SIGALGS = 0x000D
EXT_ALPN = 0x0010
EXT_RECORD_LIMIT = 0x001C
EXT_CERT_COMPRESS = 0x001B
EXT_PSK = 0x0029
EXT_VERSIONS = 0x002B
EXT_KEY_SHARE = 0x0033
EXT_ALPS_OLD = 0x4469
EXT_ALPS_NEW = 0x44CD
EXT_ECH = 0xFE0D
GROUP_KYBER = 0x6399
GROUP_MLKEM = 0x11EC
_VERSIONS = {0x0304: "13", 0x0303: "12", 0x0302: "11", 0x0301: "10", 0x0300: "s3"}
NEED_MORE, NOT_TLS = "need_more", "not_tls"


def is_grease(v: int) -> bool:
    """RFC 8701 GREASE 值, 0x0a0a 0x1a1a ... 0xfafa."""
    return (v & 0x0F0F) == 0x0A0A and (v >> 8) == (v & 0xFF)


def _u16_list(data: bytes) -> list[int]:
    return [struct.unpack_from("!H", data, i)[0] for i in range(0, len(data) - 1, 2)]


def _vec(data: bytes, off: int, width: int) -> tuple[bytes, int]:
    if off + width > len(data):
        raise ValueError("truncated")
    n = int.from_bytes(data[off : off + width], "big")
    end = off + width + n
    if end > len(data):
        raise ValueError("truncated")
    return data[off + width : end], end


def handshake_bytes(buf: bytes) -> bytes | str:
    """把客户端首段字节里的 handshake record 拼起来, 不完整返回 NEED_MORE, 不是 TLS 返回 NOT_TLS."""
    if not buf:
        return NEED_MORE
    if buf[0] != RECORD_HANDSHAKE:
        return NOT_TLS
    body, off = b"", 0
    while True:
        if len(buf) < off + 5:
            return NEED_MORE
        if buf[off] != RECORD_HANDSHAKE or buf[off + 1] != 0x03:
            return NOT_TLS
        n = struct.unpack_from("!H", buf, off + 3)[0]
        if len(buf) < off + 5 + n:
            return NEED_MORE
        body += buf[off + 5 : off + 5 + n]
        off += 5 + n
        if len(body) >= 4:
            if body[0] != HELLO_TYPE:
                return NOT_TLS
            need = 4 + int.from_bytes(body[1:4], "big")
            if need > MAX_HELLO:
                return NOT_TLS
            if len(body) >= need:
                return body[:need]


def parse(buf: bytes) -> dict | str:
    """隧道客户端首段字节 -> hello 字典, 或 NEED_MORE / NOT_TLS."""
    hs = handshake_bytes(buf)
    if isinstance(hs, str):
        return hs
    try:
        return _parse_hello(hs[4:])
    except (ValueError, struct.error, IndexError):
        return NOT_TLS


def _parse_hello(h: bytes) -> dict:
    legacy = struct.unpack_from("!H", h, 0)[0]
    _, off = _vec(h, 34, 1)
    ciphers_raw, off = _vec(h, off, 2)
    _, off = _vec(h, off, 1)
    ext_raw, _ = _vec(h, off, 2) if off < len(h) else (b"", off)
    out = {
        "legacy_version": legacy,
        "ciphers": _u16_list(ciphers_raw),
        "extensions": [],
        "groups": [],
        "points": [],
        "sigalgs": [],
        "alpn": [],
        "versions": [],
        "key_shares": [],
        "sni": "",
    }
    i = 0
    while i + 4 <= len(ext_raw):
        etype, n = struct.unpack_from("!HH", ext_raw, i)
        data = ext_raw[i + 4 : i + 4 + n]
        i += 4 + n
        out["extensions"].append(etype)
        _parse_ext(out, etype, data)
    return out


def _parse_ext(out: dict, etype: int, data: bytes) -> None:
    if etype == EXT_SNI and len(data) > 5:
        name, _ = _vec(data, 3, 2)
        out["sni"] = name.decode("ascii", "replace")
    elif etype == EXT_GROUPS:
        out["groups"] = _u16_list(_vec(data, 0, 2)[0])
    elif etype == EXT_POINTS:
        out["points"] = list(_vec(data, 0, 1)[0])
    elif etype == EXT_SIGALGS:
        out["sigalgs"] = _u16_list(_vec(data, 0, 2)[0])
    elif etype == EXT_VERSIONS:
        out["versions"] = _u16_list(_vec(data, 0, 1)[0])
    elif etype == EXT_ALPN:
        raw, off = _vec(data, 0, 2)
        j = 0
        while j < len(raw):
            proto, j = _vec(raw, j, 1)
            out["alpn"].append(proto.decode("ascii", "replace"))
    elif etype == EXT_KEY_SHARE:
        raw, _ = _vec(data, 0, 2)
        j = 0
        while j + 4 <= len(raw):
            group = struct.unpack_from("!H", raw, j)[0]
            _, j = _vec(raw, j + 2, 2)
            out["key_shares"].append(group)


def _clean(vals: list[int]) -> list[int]:
    return [v for v in vals if not is_grease(v)]


def _hex(vals: list[int]) -> str:
    return ",".join("%04x" % v for v in vals)


def _sha12(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()[:12] if text else "000000000000"


def _alpn_tag(alpn: list[str]) -> str:
    if not alpn or not alpn[0]:
        return "00"
    v = alpn[0]
    if v[0].isascii() and v[0].isalnum() and v[-1].isascii() and v[-1].isalnum():
        return v[0] + v[-1]
    raw = v.encode("latin-1", "replace").hex()
    return raw[0] + raw[-1]


def ja4_parts(h: dict) -> tuple[str, str, str, str]:
    """FoxIO JA4 (TCP): (a 段, 排序后套件, 排序后扩展且去掉 SNI 与 ALPN, 线序签名算法)."""
    versions = _clean(h["versions"])
    ver = _VERSIONS.get(max(versions) if versions else h["legacy_version"], "00")
    ciphers, exts = _clean(h["ciphers"]), _clean(h["extensions"])
    sni = "d" if EXT_SNI in exts else "i"
    a = "t%s%s%02d%02d%s" % (ver, sni, min(len(ciphers), 99), min(len(exts), 99), _alpn_tag(h["alpn"]))
    b = _hex(sorted(ciphers))
    c = _hex(sorted(e for e in exts if e not in (EXT_SNI, EXT_ALPN)))
    return a, b, c, _hex(_clean(h["sigalgs"]))


def ja3n_text(h: dict) -> str:
    """扩展排序且去 GREASE 的 JA3, 与 browserleaks 的 ja3n 同义, 顺序无关."""
    parts = [
        str(h["legacy_version"]),
        "-".join(str(v) for v in _clean(h["ciphers"])),
        "-".join(str(v) for v in sorted(_clean(h["extensions"]))),
        "-".join(str(v) for v in _clean(h["groups"])),
        "-".join(str(v) for v in h["points"]),
    ]
    return ",".join(parts)


def fingerprint(h: dict) -> dict:
    """每个捕获握手保留的摘要, 参与判定的只有顺序无关量."""
    a, b, c, d = ja4_parts(h)
    ja4_c = c + ("_" + d if d else "")
    j3n = ja3n_text(h)
    return {
        "sni": h["sni"],
        "ja4": "%s_%s_%s" % (a, _sha12(b), _sha12(ja4_c)),
        "ja4_r": "%s_%s_%s" % (a, b, ja4_c),
        "ja3n_hash": hashlib.md5(j3n.encode()).hexdigest(),
        "ja3n_text": j3n,
        "alpn": list(h["alpn"]),
        "groups": _hex(_clean(h["groups"])),
        "key_shares": _hex(_clean(h["key_shares"])),
        "grease": any(is_grease(v) for v in h["ciphers"] + h["extensions"]),
        "ext_order": _hex([e for e in _clean(h["extensions"]) if e != EXT_PSK]),
        "ext_set": sorted(_clean(h["extensions"])),
        "group_set": sorted(_clean(h["groups"]) + _clean(h["key_shares"])),
    }
