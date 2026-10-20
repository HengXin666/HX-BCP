"""按副本自己的网络路径做探测: 出口 IP 区域 (语言/时区匹配) 与系统时钟比对。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 为什么探测必须走副本的网络路径

「语言跟随出口 IP」要的是**这个副本**的出口, 不是服务进程的出口。自定义上游时
探测走一条临时 relay (与浏览器同一份 relay 代码), 直连时禁用环境变量代理,
系统代理时读环境变量 —— 三种模式与浏览器的行为一一对应。

## 系统时钟 (nodecloak clock_probe 同一判据)

比较的是电脑的**绝对时间**与 HTTPS 服务端时间 (Cloudflare trace 的 ts=),
容差 2 分钟 + 往返耗时。取不到可靠参考时报 unknown「尚未确认」, 绝不报正常。
从不按时区偏移去"修正" UTC —— 时区与时钟是两件事, 改时区校准不了时钟。
"""

from __future__ import annotations

import contextlib
import time

import httpx

from . import locale, relay
from .proxy_link import upstream_of

CLOCK_URL = "https://www.cloudflare.com/cdn-cgi/trace"
REGION_URL = "https://ipwho.is/?fields=success,ip,country_code,city,timezone.id"
FALLBACK_REGION_URL = "https://ipinfo.io/json"
CLOCK_TOLERANCE_S = 120.0
MAX_PROBE_S = 15.0
UA = "hx-browser/0.2 (environment diagnostics)"
STATUS_UNKNOWN = "unknown"


@contextlib.asynccontextmanager
async def client_for(env, timeout: float = 10.0):
    """与该副本浏览器同一网络路径的 httpx 客户端。"""
    up = upstream_of(env.proxy)
    probe: relay.Relay | None = None
    kwargs: dict = {
        "timeout": timeout,
        "headers": {"user-agent": UA},
        "follow_redirects": False,
    }
    if up is not None:
        probe = relay.Relay(up)
        port = await probe.start()
        kwargs["proxy"] = "http://127.0.0.1:%d" % port
        kwargs["trust_env"] = False
    else:
        kwargs["trust_env"] = env.proxy.type != "direct"
    try:
        async with httpx.AsyncClient(**kwargs) as client:
            yield client
    finally:
        if probe is not None:
            await probe.close()


def region_from(data: dict) -> dict:
    """ipwho.is / ipinfo 两种响应 -> {ip, country, city, timezone, languages}。"""
    if data.get("success") is True:
        country = str(data.get("country_code") or "").upper()
        tz = str((data.get("timezone") or {}).get("id") or "")
    else:
        country = str(data.get("country") or "").upper()
        tz = str(data.get("timezone") or "")
    if len(country) != 2 or not country.isalpha():
        raise ValueError("区域服务没有返回有效国家")
    return {
        "ip": str(data.get("ip") or ""),
        "country": country,
        "city": str(data.get("city") or "")[:100],
        "timezone": tz or locale.tz_for_country(country),
        "languages": locale.lang_for_country(country) or "en-US,en",
    }


async def lookup_region(env) -> dict:
    """副本出口 IP 的区域。失败抛异常 (调用方决定是否阻断启动)。"""
    errors = []
    async with client_for(env) as client:
        for url in (REGION_URL, FALLBACK_REGION_URL):
            try:
                r = await client.get(url)
                if r.status_code == 200:
                    out = region_from(r.json())
                    out["checked_at"] = int(time.time() * 1000)
                    return out
                errors.append("%s -> HTTP %d" % (url.split("/")[2], r.status_code))
            except Exception as e:  # noqa: BLE001
                errors.append("%s -> %s" % (url.split("/")[2], type(e).__name__))
    raise RuntimeError("无法通过此副本的网络路径匹配 IP 区域: " + "; ".join(errors))


def assess_clock(body: str, start: float, end: float, elapsed: float) -> dict:
    """纯函数: trace 响应 + 本机前后两次读数 -> 检查项。"""
    result = clock_unknown()
    stamps = [ln[3:] for ln in body.splitlines() if ln.startswith("ts=")]
    if len(stamps) != 1:
        return result
    try:
        server = float(stamps[0])
    except ValueError:
        return result
    if (
        not 0 < server < 253402300799
        or elapsed > MAX_PROBE_S
        or abs((end - start) - elapsed) > 2
    ):
        return result  # 本机时间在探测期间被调过, 或往返太久 —— 参考不可靠
    diff = round((start + end) / 2 - server)
    skewed = server < start - CLOCK_TOLERANCE_S or server > end + CLOCK_TOLERANCE_S
    result["status"] = "warning" if skewed else "healthy"
    result["value"] = (
        "电脑时间与 HTTPS 参考相差约 %d 秒, 请同步系统时间" % abs(diff)
        if skewed
        else "电脑时间与 HTTPS 参考偏差在 2 分钟内"
    )
    result["offset_s"] = diff
    return result


def clock_unknown() -> dict:
    return {
        "id": "clock",
        "status": STATUS_UNKNOWN,
        "value": "时间准确性尚未确认",
        "detail": "比较电脑实际时间与 HTTPS 服务端时间 (容差 2 分钟)。时区正确不代表时钟准确; "
        "偏差过大时请在系统设置中启用自动校时并立即同步, 不要按代理国家手动增减时间。",
        "fixable": False,
        "fix": {},
    }


async def clock_check(env) -> dict:
    try:
        async with client_for(env) as client:
            start, mono = time.time(), time.monotonic()
            r = await client.get(CLOCK_URL, headers={"cache-control": "no-cache"})
            end, elapsed = time.time(), time.monotonic() - mono
        if (
            r.status_code != 200
            or r.url.host != "www.cloudflare.com"
            or len(r.content) > 8192
        ):
            return clock_unknown()
        return assess_clock(r.text, start, end, elapsed)
    except Exception:  # noqa: BLE001 — 取不到参考 = 尚未确认, 不是错误
        return clock_unknown()
