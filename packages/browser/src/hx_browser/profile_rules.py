"""副本偏好的互斥校验与「实际生效值」推导 —— 依赖内核族, 故与纯模型 prefs.py 分开。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

拆开的原因是导入环: models -> prefs 必须是纯数据; 而判"是不是 Firefox"要用
kernels.family_of (kernels -> cloak -> fingerprint -> models)。
"""

from __future__ import annotations

from . import kernels
from .prefs import (
    MODE_CUSTOM,
    MODE_IP,
    RFP_TIMEZONE,
    ProfilePrefs,
    valid_lang,
    valid_startup,
    valid_timezone,
)


def is_firefox(env) -> bool:
    """Gecko 族? 判据唯一处在 kernels.family_of, 这里只转发。"""
    return kernels.family_of(env) == kernels.GECKO


def validate_prefs(env) -> list[str]:
    """返回错误列表 (空 = 合法)。与 nodecloak 的互斥规则对齐。"""
    p: ProfilePrefs = env.prefs
    errors: list[str] = []
    if p.language_mode == MODE_CUSTOM and not [x for x in p.languages if x.strip()]:
        errors.append("自定义语言需至少填写一个语言, 例如 en-US")
    for lang in p.languages:
        if not valid_lang(lang):
            errors.append("语言标签无效: %s" % lang)
    if p.timezone_mode == MODE_CUSTOM and not valid_timezone(p.timezone):
        errors.append("请填写有效的 IANA 时区, 例如 America/Los_Angeles")
    if p.firefox_resist_fingerprinting and p.language_mode == MODE_IP:
        errors.append("Firefox 严格指纹保护与「语言跟随出口 IP」互斥, 请改用自定义语言")
    if p.firefox_resist_fingerprinting and p.timezone_mode in (MODE_IP, MODE_CUSTOM):
        errors.append("Firefox 严格指纹保护统一使用 UTC 时区, 不能同时设置独立时区")
    if p.startup_url and not valid_startup(p.startup_url):
        errors.append("启动页面只允许 http(s):// 或 about:blank")
    if p.dns_privacy and not p.doh_url.startswith("https://"):
        errors.append("DoH 地址必须是 https://")
    if p.font_isolation == MODE_CUSTOM and not [f for f in p.fonts if f.strip()]:
        errors.append("自定义字体隔离需至少填写一个字体族, 例如 DejaVu Sans")
    if any(len(f) > 100 or any(ord(c) < 32 for c in f) for f in p.fonts):
        errors.append("字体族名无效")
    return errors


def effective_timezone(env, ip_timezone: str = "") -> str:
    """该副本实际要设的 TZ ("" = 跟随系统)。语义表见模块头。"""
    p: ProfilePrefs = env.prefs
    if is_firefox(env) and p.firefox_resist_fingerprinting:
        return RFP_TIMEZONE
    if p.timezone_mode == "system":
        return ""
    if p.timezone_mode == MODE_CUSTOM:
        return p.timezone
    if p.timezone_mode == MODE_IP:
        return ip_timezone
    return env.fingerprint.timezone  # inherit: 既有行为 (match_ip 时由出口对齐写入)


def effective_languages(env, ip_languages: str = "") -> str:
    """该副本实际的 Accept-Language 列表 ("" = 浏览器默认)。"""
    p: ProfilePrefs = env.prefs
    if p.language_mode == MODE_CUSTOM:
        return ",".join(x.strip() for x in p.languages if x.strip())
    if p.language_mode == MODE_IP:
        return ip_languages or env.fingerprint.language
    return env.fingerprint.language
