"""代理检测（对应 PRD F4）：出口 IP / 国家 / ASN / 延迟，多源查询。"""

from __future__ import annotations

import asyncio
import logging

import httpx

from . import relay
from .proxy_link import Upstream

log = logging.getLogger("hx_browser.proxy")

_SOURCES = [
    ("https://ipinfo.io/json", "ipinfo"),
    ("https://ip-api.com/json", "ipapi"),
    ("https://api.ipify.org?format=json", "ipify"),
]


async def _http_get(url: str, proxy: str | None, timeout: float = 8.0) -> dict | None:
    try:
        async with httpx.AsyncClient(proxy=proxy, timeout=timeout, verify=False) as client:
            r = await client.get(url, headers={"user-agent": "hx-browser/0.1"})
            if r.status_code == 200:
                return r.json()
    except Exception as e:  # noqa: BLE001
        log.debug("proxy check source %s failed: %s", url, e)
    return None


def proxy_url_str(proxy: dict | None) -> str | None:
    """把 proxy dict 转成 httpx 代理 URL。"""
    if not proxy or not proxy.get("host"):
        return None
    scheme = proxy.get("type", "http")
    if scheme == "no_proxy":
        return None
    if scheme == "socks5":
        scheme = "socks5"
    host, port = proxy["host"], proxy.get("port", 0)
    if proxy.get("username"):
        return f"{scheme}://{proxy['username']}:{proxy.get('password','')}@{host}:{port}"
    return f"{scheme}://{host}:{port}"


def _upstream(proxy: dict | None) -> Upstream | None:
    if not proxy or proxy.get("type") not in ("http", "https", "socks5") or not proxy.get("host"):
        return None
    return Upstream(proxy["type"], proxy["host"], int(proxy.get("port") or 0),
                    proxy.get("username") or "", proxy.get("password") or "")  # fmt: skip


async def check_proxy(proxy: dict | None) -> dict:
    """检测代理（或无代理直连），返回 ip/country/asn/latency。

    2026-10-16: 自定义上游改经本地 relay 检测 —— 与浏览器同一条路径 (凭据由 relay
    注入, socks5 不再依赖 httpx 的可选 socksio), 且响应里的 proxy 字段不再回显密码。
    """
    up = _upstream(proxy)
    probe = relay.Relay(up) if up else None
    pu = ("http://127.0.0.1:%d" % await probe.start()) if probe else proxy_url_str(proxy)
    start = asyncio.get_running_loop().time()
    try:  # 并行多源
        results = await asyncio.gather(*[_http_get(u, pu) for u, _ in _SOURCES], return_exceptions=True)
    finally:
        if probe:
            await probe.close()
    pu = up.redacted() if up else pu
    latency_ms = int((asyncio.get_running_loop().time() - start) * 1000)
    for res in results:
        if isinstance(res, dict):
            if "ip" in res:  # ipinfo
                org = res.get("org", "")
                asn = org.split()[0] if org else ""
                return {
                    "ok": True,
                    "ip": res.get("ip", ""),
                    "country": res.get("country", ""),
                    "region": res.get("region", ""),
                    "city": res.get("city", ""),
                    "asn": asn,
                    "isp": res.get("org", ""),
                    "latency_ms": latency_ms,
                    "proxy": pu,
                }
            if "query" in res:  # ip-api
                return {
                    "ok": True,
                    "ip": res.get("query", ""),
                    "country": res.get("country", ""),
                    "region": res.get("regionName", ""),
                    "city": res.get("city", ""),
                    "asn": "",
                    "isp": res.get("isp", ""),
                    "latency_ms": latency_ms,
                    "proxy": pu,
                }
    return {"ok": False, "error": "all sources failed", "proxy": pu, "latency_ms": latency_ms}


async def check_browser_webdriver_ready(debug_port: int) -> bool:
    """简单探测调试端口是否就绪（供启动后校验）。"""
    from . import cdp

    try:
        await cdp.get_browser_ws(debug_port, timeout=3)
        return True
    except Exception:
        return False
