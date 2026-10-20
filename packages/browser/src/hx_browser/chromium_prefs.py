"""Chromium 系副本的网站设置写入 —— Default/Preferences 与 Local State 的**合并**写。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 只合并, 不覆盖

Preferences 里同时有 Cookie 之外的大量登录相关状态 (站点例外、同步、扩展设置)。
本模块只改下表列出的键, 其它键原样保留; 文件解析失败时**拒绝写入**并返回告警,
而不是用空 dict 覆盖 —— 覆盖等于抹掉用户在该副本里攒下的一切站点设置。

    键                                                  来源字段
    intl.accept_languages / intl.selected_languages     effective_languages
    profile.default_content_setting_values.*            notifications/geolocation/camera/microphone/images
    webrtc.ip_handling_policy                           webrtc (inherit 不写)
    enable_do_not_track                                 do_not_track (inherit 不写)
    session.restore_on_startup / session.startup_urls   startup_url
    Local State: dns_over_https.mode / templates        dns_privacy / doh_url

GPC 在 Chromium 没有偏好开关 (只能靠扩展), 这里不伪装支持 —— 检测页会如实报告。
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from .prefs import ProfilePrefs

PREFS_REL = Path("Default") / "Preferences"
LOCAL_STATE = "Local State"
CONTENT_KEYS = ("profile", "default_content_setting_values")
# Chromium 内容设置值: 1=允许 2=阻止 3=询问
_PERMISSION = {"allow": 1, "block": 2, "ask": 3}
_WEBRTC = {
    "default": "default",
    "public": "default_public_interface_only",
    "proxy": "disable_non_proxied_udp",
    # Chromium 没有"完全关闭 WebRTC"的偏好; 最严的是只走代理 (配合 relay 即无 UDP 直连)
    "disable": "disable_non_proxied_udp",
}
DOH_MODE = "dns_over_https.mode"
DOH_TEMPLATES = "dns_over_https.templates"
START_URLS = 4  # session.restore_on_startup: 4 = 打开指定网页


class PrefsFileError(Exception):
    """偏好文件存在但无法安全解析 —— 调用方应保留原文件并提示用户。"""


def read_json(path: Path) -> dict:
    if not path.exists():
        return {}
    try:
        data = json.loads(path.read_text("utf-8") or "{}")
    except (OSError, ValueError) as e:
        raise PrefsFileError("%s 无法解析, 已保留原文件未覆盖" % path.name) from e
    if not isinstance(data, dict):
        raise PrefsFileError("%s 结构无效, 已保留原文件未覆盖" % path.name)
    return data


def write_json(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".hxtmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False), "utf-8")
    os.chmod(tmp, 0o600)
    tmp.replace(path)


def _node(data: dict, *keys: str) -> dict:
    cur = data
    for k in keys:
        nxt = cur.get(k)
        if not isinstance(nxt, dict):
            nxt = {}
            cur[k] = nxt
        cur = nxt
    return cur


def plan_preferences(p: ProfilePrefs, languages: str) -> dict[str, object]:
    """偏好 -> {"a.b.c": value} 平铺变更表 (纯函数, 便于测试与修复记录)。"""
    out: dict[str, object] = {}
    if languages:
        out["intl.accept_languages"] = languages
        out["intl.selected_languages"] = languages
    base = ".".join(CONTENT_KEYS) + "."
    for key, mode in (
        ("notifications", p.notifications),
        ("geolocation", p.geolocation),
        ("media_stream_camera", p.camera),
        ("media_stream_mic", p.microphone),
    ):
        out[base + key] = _PERMISSION[mode]
    out[base + "images"] = 2 if p.block_images else 1
    if p.webrtc != "inherit":
        out["webrtc.ip_handling_policy"] = _WEBRTC[p.webrtc]
    if p.do_not_track != "inherit":
        out["enable_do_not_track"] = p.do_not_track == "on"
    if p.startup_url:
        out["session.restore_on_startup"] = START_URLS
        out["session.startup_urls"] = [p.startup_url]
    return out


def plan_local_state(p: ProfilePrefs, current: dict | None = None) -> dict[str, object]:
    """DoH。关闭时只撤销**本模块写过的**严格模式, 不动用户自己在浏览器里设的 DoH。"""
    if p.dns_privacy:
        return {DOH_MODE: "secure", DOH_TEMPLATES: p.doh_url}
    cur = current or {}
    if (
        get_path(cur, DOH_MODE) == "secure"
        and get_path(cur, DOH_TEMPLATES) == p.doh_url
    ):
        return {DOH_MODE: "automatic"}
    return {}


def get_path(data: dict, dotted: str):
    cur: object = data
    for k in dotted.split("."):
        if not isinstance(cur, dict) or k not in cur:
            return None
        cur = cur[k]
    return cur


def _apply(data: dict, changes: dict[str, object]) -> None:
    for dotted, value in changes.items():
        *parents, leaf = dotted.split(".")
        _node(data, *parents)[leaf] = value


def write(profile_dir: Path, p: ProfilePrefs, languages: str) -> list[str]:
    """把偏好合并写入 profile。返回告警列表 (空 = 全部写入)。"""
    warnings: list[str] = []
    for rel in (PREFS_REL, Path(LOCAL_STATE)):
        path = profile_dir / rel
        try:
            data = read_json(path)
        except PrefsFileError as e:
            warnings.append(str(e))
            continue
        if rel == PREFS_REL:
            changes = plan_preferences(p, languages)
            if p.geolocation == "block":
                _drop_allowed_geo(data)
        else:
            changes = plan_local_state(p, data)
        if changes:
            _apply(data, changes)
            write_json(path, data)
    return warnings


def _drop_allowed_geo(data: dict) -> None:
    """定位改成禁用时, 去掉此前逐站点"允许"的例外, 否则禁用只对新站点生效。"""
    sites = get_path(data, "profile.content_settings.exceptions.geolocation")
    if isinstance(sites, dict):
        for origin in [
            o for o, v in sites.items() if isinstance(v, dict) and v.get("setting") == 1
        ]:
            sites.pop(origin, None)
