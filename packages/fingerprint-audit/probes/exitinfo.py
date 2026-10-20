"""出口/环境实况读取 — 质检要对比的"真值"从哪来。

为什么需要这个: 2026-09-14 实测踩到 —— 手工给 `--expect-tz Asia/Tokyo` 而出口
其实在新加坡, 于是**自己制造了** Timezone spoofed。期望时区不该由人猜, 应当从
出口实际属性推导。本模块只做"读实况", 不判好坏 (判定在 rules.py)。
"""
from __future__ import annotations

import re
import subprocess


def exit_ip(timeout: int = 15) -> str:
    """当前出口的公网 IP (直连时是宿主 IP)。"""
    for url in ("https://api.ipify.org", "https://ifconfig.me/ip"):
        try:
            r = subprocess.run(["curl", "-s", "-m", str(timeout), url],
                               capture_output=True, text=True, timeout=timeout + 5)
            ip = (r.stdout or "").strip()
            if re.fullmatch(r"\d{1,3}(?:\.\d{1,3}){3}", ip):
                return ip
        except Exception:
            continue
    return ""


def ip_geo(endpoint: str) -> dict:
    """**在被测浏览器内**查询出口 IP 的地理信息。

    为什么在被测浏览器里查而不是从宿主 curl:
      · 出口是绑在浏览器上的 (proxy), 宿主 curl 可能走的是另一条链路 → 查到的是错的时区;
      · 在被测浏览器内查, 拿到的就是**该环境真实表现给站点**的地理属性。
    返回 {timezone, country, ip} (查不到给空值, 由调用方决定怎么办, 绝不猜)。
    """
    from .cdp import Browser
    out = {"timezone": "", "country": "", "ip": ""}
    # ip-api.com 走 http (免费版不支持 https), ipapi.co 走 https 兜底
    # ⚠️ 实测 (2026-09-14): 这两个端点都不可靠 —— ip-api.com 走 http 会被代理拦,
    #   ipapi.co 免费额度耗尽后返回提示文本。故本函数定位是**兜底**,
    #   首选方案是从 pixelscan 报告里读出口时区 (它经被测环境自己报出, 零外部依赖),
    #   见 rules.py 的 R4b 自洽判定。
    endpoints = [
        ("http://ip-api.com/json/?fields=status,countryCode,timezone,query", "ip-api"),
        ("https://ipwho.is/", "ipwho"),
        ("https://ipapi.co/json/", "ipapi"),
    ]
    for url, kind in endpoints:
        try:
            b = Browser(endpoint).connect()
            try:
                b.goto(url, wait_s=4)
                txt = b.text()
            finally:
                b.close()
            if kind == "ip-api":
                import json as _j
                d = _j.loads(txt)
                if d.get("status") == "success":
                    out["timezone"] = str(d.get("timezone") or "")
                    out["country"] = str(d.get("countryCode") or "")
                    out["ip"] = str(d.get("query") or "")
                    return out
            else:
                import json as _j
                d = _j.loads(txt)
                if d.get("success") is False:
                    continue
                tz = d.get("timezone")
                if isinstance(tz, dict):
                    tz = tz.get("id") or tz.get("name") or ""
                out["timezone"] = str(tz or "")
                out["country"] = str(d.get("country_code") or d.get("countryCode") or "")
                out["ip"] = str(d.get("ip") or "")
                if out["timezone"]:
                    return out
        except Exception:
            continue
    return out


def js_timezone_of(endpoint: str) -> str:
    """被测环境当前实际报出的 JS 时区。"""
    from .cdp import Browser
    try:
        b = Browser(endpoint).connect()
        try:
            return str(b.js("Intl.DateTimeFormat().resolvedOptions().timeZone") or "")
        finally:
            b.close()
    except Exception:
        return ""
