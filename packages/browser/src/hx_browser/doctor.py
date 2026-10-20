"""检测: 网络 / 语言 / 时区 / 系统时钟 / WebRTC / DNS / DNT-GPC / 字体 / 定位 / 内核 / 隔离。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

每项输出 {id, status, value, detail, fixable, fix}:

    status   healthy 正常 / configured 已写入配置(需网页复检) / warning 建议修复 /
             error 无法启动或不可用 / unknown 尚未确认 / manual 需在浏览器里实测
    fix      可修复时给出要改的偏好 {字段: 新值}; 修复由 repair_ops 执行 (先备份再改)

**「配置已写入」≠「网页实测通过」**: 本模块只读配置与出口, 不读网页; 网页实际值
请用 GET /environments/{id}/fingerprint/check 在运行中的副本里复检。
"""

from __future__ import annotations

import os
from pathlib import Path

from . import kernels, netcheck, sandbox
from .firefox_prefs import chinese_fonts
from .prefs import MODE_IP, ON
from .profile_rules import effective_languages, effective_timezone, is_firefox
from .proxy_link import CUSTOM_TYPES

HEALTHY, CONFIGURED, WARNING, ERROR, UNKNOWN, MANUAL = (
    "healthy", "configured", "warning", "error", netcheck.STATUS_UNKNOWN, "manual",
)  # fmt: skip
ZONEINFO_MARK = "zoneinfo/"


def check(
    cid: str, status: str, value: str, detail: str = "", fix: dict | None = None
) -> dict:
    return {
        "id": cid,
        "status": status,
        "value": value,
        "detail": detail,
        "fixable": bool(fix),
        "fix": fix or {},
    }


def system_timezone() -> str:
    """电脑当前 IANA 时区 (读 /etc/localtime 软链; 读不到返回空串)。"""
    tz = os.environ.get("TZ", "").lstrip(":")
    if tz:
        return tz
    try:
        real = str(Path("/etc/localtime").resolve())
    except OSError:
        return ""
    return real.split(ZONEINFO_MARK, 1)[1] if ZONEINFO_MARK in real else ""


def _network(env, region: dict, err: str) -> dict:
    mode = env.proxy.type
    if err:
        return check("network", ERROR, "出口检测失败", err + "。代理失败不会回落直连。")
    where = "%s %s" % (region.get("country", ""), region.get("city", ""))
    return check(
        "network",
        HEALTHY,
        "%s · %s" % (region.get("ip", ""), where.strip()),
        "代理模式: %s" % mode,
    )


def _language(env, region: dict) -> dict:
    p = env.prefs
    langs = effective_languages(env, region.get("languages", ""))
    if not langs:
        fix = (
            {"language_mode": "custom", "languages": ["en-US", "en"]}
            if p.firefox_resist_fingerprinting
            else {"language_mode": MODE_IP}
        )
        return check(
            "language",
            WARNING,
            "跟随浏览器默认",
            "未固定语言, 网页会读到电脑系统语言",
            fix,
        )
    want = region.get("languages", "")
    if (
        want
        and p.language_mode != MODE_IP
        and langs.split(",")[0] != want.split(",")[0]
    ):
        fix = {} if p.firefox_resist_fingerprinting else {"language_mode": MODE_IP}
        detail = (
            "出口 IP 所在地首选语言为 %s; 严格指纹保护开启时只能用自定义语言" % want
        )
        return check("language", WARNING, langs, detail, fix)
    return check(
        "language",
        CONFIGURED,
        langs,
        "模式: %s (需在网页中复检 navigator.languages)" % p.language_mode,
    )


def _timezone(env, region: dict) -> dict:
    p = env.prefs
    if is_firefox(env) and p.firefox_resist_fingerprinting:
        return check("timezone", CONFIGURED, "UTC", "Firefox 严格指纹保护统一报告 UTC")
    tz = effective_timezone(env, region.get("timezone", "")) or system_timezone()
    src = (
        "跟随电脑系统"
        if not effective_timezone(env, region.get("timezone", ""))
        else "模式: %s" % p.timezone_mode
    )
    want = region.get("timezone", "")
    if want and tz and tz != want:
        detail = (
            "出口 IP 所在时区为 %s。系统时区可在电脑的日期与时间设置中修改 (影响所有应用)"
            % want
        )
        return check(
            "timezone",
            WARNING,
            "%s (%s)" % (tz, src),
            detail,
            {"timezone_mode": MODE_IP},
        )
    return check("timezone", HEALTHY if tz else UNKNOWN, tz or "读取失败", src)


def _webrtc(env) -> dict:
    p, fp = env.prefs, env.fingerprint
    if p.webrtc in ("proxy", "disable") or fp.webrtc_mode in ("proxy", "disable"):
        return check(
            "webrtc",
            CONFIGURED,
            "已限制非代理 UDP" if p.webrtc != "disable" else "已关闭 WebRTC",
        )
    if env.proxy.type in CUSTOM_TYPES:
        return check(
            "webrtc",
            WARNING,
            "未限制",
            "使用自定义代理时 WebRTC 可能经 UDP 暴露真实 IP",
            {"webrtc": "proxy"},
        )
    return check(
        "webrtc", MANUAL, "浏览器默认策略", "直连/系统代理下 WebRTC 暴露的就是本机出口"
    )


def _dns(env) -> dict:
    if env.prefs.dns_privacy:
        return check("dns", CONFIGURED, "加密 DNS (严格)", env.prefs.doh_url)
    return check(
        "dns",
        WARNING,
        "未配置加密 DNS",
        "DNS 查询可能兼容代理暴露所在网络",
        {"dns_privacy": True},
    )


def _tracking(env) -> dict:
    p = env.prefs
    gpc_ok = p.gpc == ON or not is_firefox(env)
    if p.do_not_track == ON and gpc_ok:
        value = "DNT 已开启" + (
            " · GPC 已开启" if is_firefox(env) else " · Chromium 无原生 GPC"
        )
        return check("tracking", CONFIGURED, value)
    fix = {"do_not_track": ON, **({"gpc": ON} if is_firefox(env) else {})}
    return check(
        "tracking", WARNING, "DNT/GPC 未开启", "只表达隐私偏好, 不阻止追踪", fix
    )


def _fonts(env, fonts: list[str] | None) -> dict:
    if env.prefs.font_isolation != "inherit":  # fontconfig 层, 所有内核生效
        return check("fonts", CONFIGURED, "系统字体隔离: %s" % env.prefs.font_isolation)
    cjk = chinese_fonts() if fonts is None else fonts
    if not is_firefox(env):
        return check(
            "fonts", WARNING, "未隔离系统字体", "隔离后网页只能枚举白名单 + 中文字体",
            {"font_isolation": "baseline"},
        )  # fmt: skip
    if env.prefs.firefox_font_restriction:
        return check(
            "fonts", CONFIGURED, "已限制系统字体 · 保留 %d 个中文字体" % len(cjk)
        )
    return check(
        "fonts",
        WARNING,
        "未限制系统字体",
        "限制后网页只能枚举常见字体 + 电脑上的中文字体",
        {"firefox_font_restriction": True},
    )


def _geolocation(env) -> dict:
    p = env.prefs
    if p.geolocation == "allow" and env.proxy.type in CUSTOM_TYPES:
        return check(
            "geolocation",
            WARNING,
            "允许",
            "位置由浏览器提供 (不覆盖坐标), 可能与代理出口地区不一致",
            {"geolocation": "ask"},
        )
    return check(
        "geolocation",
        CONFIGURED,
        {"ask": "询问", "allow": "允许", "block": "禁用"}[p.geolocation],
    )


def _kernel(env, settings) -> dict:
    exe = kernels.resolve_executable(env, settings)
    if not exe or not Path(exe).exists():
        return check(
            "kernel",
            ERROR,
            "未找到可执行文件",
            "内核 %s 不可用, 请在设置中配置路径"
            % (env.fingerprint.kernel or kernels.family_of(env)),
        )
    return check("kernel", HEALTHY, "%s · %s" % (env.fingerprint.kernel or "默认", exe))


def _isolation(env) -> dict:
    if env.prefs.isolation == "sandbox":
        if not sandbox.available():
            return check(
                "isolation",
                ERROR,
                "要求沙箱但缺 bwrap",
                "已拒绝启动, 不会静默降级",
                {"isolation": "process"},
            )
        return check(
            "isolation",
            CONFIGURED,
            "bubblewrap 沙箱",
            "PID/IPC/UTS/网络/家目录/显示/字体隔离 + seccomp; 内核共享"
            + ("; X 显示为共享模式" if env.prefs.sandbox_x11 == "shared" else ""),
        )
    return check(
        "isolation",
        HEALTHY,
        "独立 profile + 独立进程",
        "如需命名空间隔离可切换 sandbox",
    )


async def run(
    env, settings, with_network: bool = True, fonts: list[str] | None = None
) -> dict:
    region: dict = {}
    err = ""
    if with_network:
        try:
            region = await netcheck.lookup_region(env)
        except Exception as e:  # noqa: BLE001
            err = str(e)
    checks = [
        _network(env, region, err)
        if with_network
        else check("network", UNKNOWN, "未检测"),
        _language(env, region),
        _timezone(env, region),
        await netcheck.clock_check(env) if with_network else netcheck.clock_unknown(),
        _webrtc(env),
        _dns(env),
        _tracking(env),
        _fonts(env, fonts),
        _geolocation(env),
        _kernel(env, settings),
        _isolation(env),
    ]
    return {"env_id": env.id, "region": region, "checks": checks}
