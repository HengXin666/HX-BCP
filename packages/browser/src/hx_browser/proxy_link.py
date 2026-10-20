"""代理链接解析 + 副本代理模式 —— 「浏览器最终连哪里」的唯一判据。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 代理模式 (ProxyConfig.type)

    no_proxy  既有值, 语义不变: 不给浏览器任何代理参数 (Chromium 在 Linux 上读环境变量)
    direct    直连: Chromium --no-proxy-server / Firefox network.proxy.type=0
    system    系统代理: Chromium 不加参数 / Firefox network.proxy.type=5
    http / https / socks5   自定义上游, 一律经**本地 relay** (relay.py) 转发

## 为什么自定义上游一律走本地 relay

Chromium 的 ``--proxy-server`` **不解析** URL 里的 user:pass (本仓 2026-09-26 /
2026-10-06 两次实测: 内嵌凭据 -> ERR_NO_SUPPORTED_PROXIES 或 407 弹窗)。
upstream-proxy 共享入口 7890 的出口身份恰恰由用户名承载, 所以凭据必须由本地 relay 注入。
浏览器只看到 ``http://127.0.0.1:<port>``, 凭据永远不进命令行 (ps 可见) 与 profile。

## upstream-proxy 兼容

    http://<identity>:<pass>@127.0.0.1:7890   标准形态 (共享入口 + 用户名分流)
    hxpxy://<identity>[:<pass>][@host:port]   简写, 省略地址时落到 HXPXY_DEFAULT_ENTRY
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from urllib.parse import unquote, urlsplit

HTTP, SOCKS5 = "http", "socks5"
DIRECT = "direct"  # 仅 relay 内部: 沙箱副本经宿主直连出网
CUSTOM_TYPES = (HTTP, "https", SOCKS5)
ALL_TYPES = ("no_proxy", "direct", "system", *CUSTOM_TYPES)
# upstream-proxy 共享入口; 可用环境变量覆盖 (部署在别的机器时)
HXPXY_DEFAULT_ENTRY = os.environ.get("HXBR_HXPXY_ENTRY", "127.0.0.1:7890")
_SCHEME_ALIASES = {
    HTTP: HTTP,
    "https": "https",
    SOCKS5: SOCKS5,
    "socks5h": SOCKS5,
    "socks": SOCKS5,
    "hxpxy": HTTP,
    "upstream-proxy": HTTP,
}
_FORBIDDEN_HOST_CHARS = set("/\\@?# \t\r\n")


class ProxyLinkError(ValueError):
    """链接无法解析 —— 消息直接给用户看, 不含密码。"""


@dataclass(frozen=True)
class Upstream:
    scheme: str  # http | https | socks5
    host: str
    port: int
    username: str = ""
    password: str = ""

    def authority(self) -> str:
        return (
            "[%s]:%d" % (self.host, self.port)
            if ":" in self.host
            else "%s:%d" % (self.host, self.port)
        )

    def redacted(self) -> str:
        user = (self.username + ":***@") if self.username else ""
        return "%s://%s%s" % (self.scheme, user, self.authority())


def parse_link(text: str) -> dict:
    """代理链接 -> ProxyConfig 字段 dict ({type, host, port, username, password})。"""
    raw = (text or "").strip()
    if not raw:
        raise ProxyLinkError("代理链接为空")
    if raw.lower() in ("direct", "system", "no_proxy"):
        return {
            "type": raw.lower(),
            "host": "",
            "port": 0,
            "username": "",
            "password": "",
        }
    if "://" in raw:
        return _parse_url(raw)
    return _parse_colon_form(raw)


def _parse_url(raw: str) -> dict:
    scheme = raw.split("://", 1)[0].lower()
    if scheme not in _SCHEME_ALIASES:
        raise ProxyLinkError(
            "不支持的代理协议: %s (支持 http/https/socks5/hxpxy)" % scheme
        )
    is_hxpxy = scheme in ("hxpxy", "upstream-proxy")
    rest = raw.split("://", 1)[1]
    if is_hxpxy and "@" not in rest:
        rest = rest.rstrip("/") + "@" + HXPXY_DEFAULT_ENTRY
    parts = urlsplit("http://" + rest)
    try:
        port = parts.port
    except ValueError as e:
        raise ProxyLinkError("代理端口无效") from e
    host = parts.hostname or ""
    if is_hxpxy and not port:
        port = int(HXPXY_DEFAULT_ENTRY.rsplit(":", 1)[1])
    cfg = {
        "type": _SCHEME_ALIASES[scheme],
        "host": host,
        "port": port or 0,
        "username": unquote(parts.username or ""),
        "password": unquote(parts.password or ""),
    }
    validate_config(cfg)
    return cfg


def _parse_colon_form(raw: str) -> dict:
    """host:port / host:port:user:pass / user:pass@host:port (默认 http)。"""
    user = password = ""
    if "@" in raw:
        cred, addr = raw.rsplit("@", 1)
        user, _, password = cred.partition(":")
        bits = _addr_bits(addr)
    elif raw.startswith("["):
        bits = _addr_bits(raw)
    else:
        bits = raw.split(":")
        if len(bits) == 4:
            bits, user, password = bits[:2], bits[2], bits[3]
        elif len(bits) != 2:
            raise ProxyLinkError(
                "无法识别的代理格式, 示例: socks5://user:pass@1.2.3.4:1080"
            )
    if len(bits) != 2 or not bits[1].isdigit():
        raise ProxyLinkError("代理端口无效")
    cfg = {
        "type": HTTP,
        "host": bits[0].strip("[]"),
        "port": int(bits[1]),
        "username": user,
        "password": password,
    }
    validate_config(cfg)
    return cfg


def _addr_bits(addr: str) -> list[str]:
    """host:port / [ipv6]:port -> [host, port]。"""
    if addr.startswith("["):
        host, _, rest = addr[1:].partition("]")
        return [host, rest[1:] if rest.startswith(":") else ""]
    return addr.rsplit(":", 1)


def validate_config(cfg: dict) -> None:
    """校验 ProxyConfig 形状 (dict 或模型的 model_dump)。不合法抛 ProxyLinkError。"""
    kind = cfg.get("type", "no_proxy")
    if kind not in ALL_TYPES:
        raise ProxyLinkError("不支持的代理类型: %s" % kind)
    if kind not in CUSTOM_TYPES:
        return
    host, port = str(cfg.get("host") or ""), int(cfg.get("port") or 0)
    if not host or len(host) > 253 or any(c in _FORBIDDEN_HOST_CHARS for c in host):
        raise ProxyLinkError("代理地址应为 IP 或域名, 不要包含协议或路径")
    if not 0 < port < 65536:
        raise ProxyLinkError("代理端口无效")
    # SOCKS5 (RFC 1929) 用户名/密码各限 255 字节; HTTP Basic 无此限 (网关常用 JWT 作用户名)
    limit = 255 if kind == SOCKS5 else 8192
    for field in ("username", "password"):
        value = str(cfg.get(field) or "")
        if len(value.encode()) > limit or any(ord(c) < 32 for c in value):
            raise ProxyLinkError(
                "代理%s格式无效或超过 %d 字节"
                % ("用户名" if field == "username" else "密码", limit)
            )


def upstream_of(proxy) -> Upstream | None:
    """ProxyConfig -> Upstream; 非自定义上游返回 None。"""
    if proxy is None or proxy.type not in CUSTOM_TYPES or not proxy.host:
        return None
    return Upstream(
        proxy.type,
        proxy.host,
        int(proxy.port),
        proxy.username or "",
        proxy.password or "",
    )


def egress_upstream(proxy, environ: dict) -> Upstream:
    """沙箱副本的宿主侧出口: 自定义上游原样; system 取服务进程的代理环境变量; 否则直连。

    沙箱内没有网卡, 所以 direct/system 也必须由宿主 relay 代为出网 (行为与不开沙箱一致)。
    """
    up = upstream_of(proxy)
    if up is not None:
        return up
    if proxy is not None and proxy.type == "system":
        for key in (
            "HTTPS_PROXY",
            "https_proxy",
            "ALL_PROXY",
            "all_proxy",
            "HTTP_PROXY",
            "http_proxy",
        ):
            if environ.get(key):
                cfg = parse_link(environ[key])
                return Upstream(cfg["type"], cfg["host"], int(cfg["port"]),
                                cfg.get("username") or "", cfg.get("password") or "")  # fmt: skip
    return Upstream(DIRECT, "", 0)


def chromium_proxy_args(proxy, relay_port: int) -> list[str]:
    """Chromium 代理参数。relay_port>0 表示自定义上游已由本地 relay 接管。"""
    if proxy is None:
        return []
    if proxy.type == "direct":
        return ["--no-proxy-server"]
    if proxy.type in CUSTOM_TYPES and relay_port:
        # 本地 relay 无凭据, 凭据只活在 relay 进程内存里
        return ["--proxy-server=http://127.0.0.1:%d" % relay_port]
    return []  # no_proxy / system: 沿用浏览器读系统代理的既有行为


def firefox_proxy_prefs(proxy, relay_port: int) -> dict:
    """Firefox network.proxy.* 偏好。"""
    if proxy is None or proxy.type == "no_proxy":
        return {}
    if proxy.type == "direct":
        return {"network.proxy.type": 0}
    if proxy.type == "system":
        return {"network.proxy.type": 5}
    if not relay_port:
        return {}
    return {
        "network.proxy.type": 1,
        "network.proxy.http": "127.0.0.1",
        "network.proxy.http_port": relay_port,
        "network.proxy.ssl": "127.0.0.1",
        "network.proxy.ssl_port": relay_port,
        "network.proxy.no_proxies_on": "",
        "network.proxy.allow_hijacking_localhost": False,
        "network.proxy.failover_direct": False,
    }
