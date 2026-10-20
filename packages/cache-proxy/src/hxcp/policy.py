"""缓存策略 — 什么该命中、TTL 多久。

原则: **保守优先, 宁可不缓存也不缓存错**。
  静态资源按类型直接缓存 (内容由版本化 URL 唯一确定);
  JSON/HTML 只在显式正向 Cache-Control 时缓存, 因为 API JSON 通常是会话化的 ——
  缓存了会把 A 账号的响应喂给 B 账号 (旧 cacheproxy 明确记录过这条: 带 Set-Cookie 绝不缓存)。

本模块只做判定, 不做 IO; 判定结果由 engine 执行。
"""

# Agent Note: .agents/notes/implemented/feature/2026-09-18-hxcp-three-entries.md
from __future__ import annotations

import re
from dataclasses import dataclass, field

# 内容由 URL 唯一确定的类型 → 无视响应头直接缓存
STATIC_TYPES = frozenset({
    "application/javascript", "text/javascript", "application/x-javascript",
    "text/css",
    "image/png", "image/jpeg", "image/gif", "image/svg+xml", "image/webp",
    "image/avif", "image/x-icon", "image/vnd.microsoft.icon",
    "font/woff", "font/woff2", "font/ttf", "font/otf",
    "application/font-woff", "application/font-woff2",
    "application/vnd.ms-fontobject", "application/x-font-ttf",
    "application/wasm", "application/octet-stream",
    "video/mp4", "video/webm", "audio/mp4", "audio/webm",
})

# 内容可变 → 只有显式正向缓存头才缓存
CONDITIONAL_TYPES = frozenset({
    "application/json", "application/manifest+json", "application/xml",
    "text/xml", "text/html", "application/xhtml+xml", "text/plain",
})

# 会话/敏感路径特征 → 永不缓存 (即使类型可缓存)
SENSITIVE_PATTERNS = (
    re.compile(r"/(login|signin|signup|logout|oauth|authorize|token|session|account|password|checkout|pay)", re.I),
    re.compile(r"/(api|graphql|rpc)(/|$)", re.I),
)

# 默认 TTL (秒)
TTL_STATIC = 365 * 86400      # 版本化静态资源: 一年 (URL 变了就是新资源)
TTL_CONDITIONAL = 600         # 显式声明的动态内容: 谨慎
TTL_MAX = 365 * 86400


@dataclass
class Decision:
    cache: bool
    ttl: float = 0.0
    reason: str = ""
    etag: str = ""

    def __bool__(self) -> bool:
        return self.cache


def _parse_cache_control(headers: dict[str, str]) -> dict[str, str]:
    out: dict[str, str] = {}
    for k, v in (headers or {}).items():
        if k.lower() != "cache-control":
            continue
        for part in str(v).split(","):
            part = part.strip()
            if not part:
                continue
            if "=" in part:
                a, b = part.split("=", 1)
                out[a.strip().lower()] = b.strip()
            else:
                out[part.lower()] = ""
    return out


def content_type(headers: dict[str, str]) -> str:
    for k, v in (headers or {}).items():
        if k.lower() == "content-type":
            return str(v).split(";")[0].strip().lower()
    return ""


def decide(url: str, status: int, headers: dict[str, str], *, size: int,
           max_bytes: int) -> Decision:
    """判断一个**响应**能否入缓存。传入的 headers 必须是响应头。"""
    if status not in (200, 203, 301, 308):
        return Decision(False, reason="status:" + str(status))
    if size > max_bytes:
        return Decision(False, reason="too_large")
    # 会话化响应绝不缓存: Set-Cookie 意味着这份响应与某个会话绑定
    if any(k.lower() == "set-cookie" for k in (headers or {})):
        return Decision(False, reason="set_cookie")
    if not url.lower().startswith(("http://", "https://")):
        return Decision(False, reason="non_http_scheme")
    for pat in SENSITIVE_PATTERNS:
        if pat.search(url):
            return Decision(False, reason="sensitive_path")

    cc = _parse_cache_control(headers)
    if "no-store" in cc or "private" in cc:
        return Decision(False, reason="cache_control_no_store")
    etag = ""
    for k, v in (headers or {}).items():
        if k.lower() in ("etag", "last-modified"):
            etag = str(v)
            break

    ctype = content_type(headers)
    if ctype in STATIC_TYPES:
        if "max-age" in cc:
            try:
                ttl = float(cc["max-age"])
            except ValueError:
                ttl = TTL_STATIC
        else:
            ttl = TTL_STATIC
        return Decision(True, ttl=min(ttl, TTL_MAX), reason="static:" + ctype, etag=etag)

    if ctype in CONDITIONAL_TYPES:
        if "max-age" in cc:
            try:
                ttl = float(cc["max-age"])
            except ValueError:
                ttl = 0.0
            if ttl >= TTL_CONDITIONAL:
                return Decision(True, ttl=min(ttl, TTL_MAX),
                                reason="conditional_positive:" + ctype, etag=etag)
            return Decision(False, reason="conditional_short_ttl")
        if "immutable" in cc:
            return Decision(True, ttl=TTL_STATIC, reason="conditional_immutable", etag=etag)
        return Decision(False, reason="conditional_no_header")

    return Decision(False, reason="unknown_type:" + (ctype or "?"))


@dataclass
class Rules:
    """按 host 覆盖策略。默认全放行 (不缓存即直连, 零风险)。"""
    allow_hosts: frozenset[str] = frozenset()      # 非空时只处理这些 host 的后缀匹配
    deny_hosts: frozenset[str] = frozenset()
    block_images: bool = False
    max_bytes: int = 8 * 1024 * 1024
    extra: dict = field(default_factory=dict)

    def may_handle(self, url: str) -> bool:
        host = ""
        if "//" in url:
            host = url.split("//", 1)[1].split("/", 1)[0].split(":", 1)[0].lower()
        if any(host == d or host.endswith("." + d) for d in self.deny_hosts):
            return False
        if self.allow_hosts and not any(host == a or host.endswith("." + a)
                                        for a in self.allow_hosts):
            return False
        return True
