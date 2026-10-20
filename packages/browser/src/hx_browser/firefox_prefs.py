"""Firefox 系副本的偏好 —— 生成 user.js 的托管块 (语言/代理/网站设置/字体/严格指纹保护)。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 托管块

fingerprint.build_firefox_userjs 每次启动都**重写** user.js (既有行为, 不改);
本模块在其后追加一个以 MANAGED_BEGIN/END 包围的块。Firefox 按行序读 user.js,
后写的同名偏好覆盖先写的 —— 所以托管块里的值是最终值, 而既有那几行保持原样。

## 字体限制 (font.system.whitelist)

只让副本看到一份常见字体 + **电脑上已装的中文字体** (fc-list :lang=zh 实测枚举)。
不保留中文字体会让中文页面全部变成豆腐块; 而白名单里列出却未安装的字体 Firefox 直接忽略,
所以列表可以写宽, 不会凭空"多出"字体。网页通过 @font-face 下载的字体不受此限制。
"""

from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path

from .prefs import INHERIT, ON, RFP_TIMEZONE, ProfilePrefs

MANAGED_BEGIN = "// >>> hx-browser managed prefs (do not edit) >>>"
MANAGED_END = "// <<< hx-browser managed prefs <<<"
USER_JS = "user.js"
TOOL_FC_LIST = "fc-list"
BASE_FONTS = (
    "Arial", "Courier New", "Georgia", "Helvetica", "Times New Roman", "Trebuchet MS", "Verdana",
    "DejaVu Sans", "DejaVu Sans Mono", "DejaVu Serif", "Liberation Mono", "Liberation Sans",
    "Liberation Serif", "Noto Sans", "Noto Serif", "Noto Sans Mono", "Noto Color Emoji",
)  # fmt: skip
# Firefox 权限默认值: 0=询问 1=允许 2=阻止
_PERMISSION = {"ask": 0, "allow": 1, "block": 2}
_PEER = "media.peerconnection."


def chinese_fonts() -> list[str]:
    """电脑上已装的中文字体族名 (去重保序)。没有 fc-list 时返回空列表。"""
    if not shutil.which(TOOL_FC_LIST):
        return []
    try:
        out = subprocess.run(
            [TOOL_FC_LIST, ":lang=zh", "family"],
            capture_output=True,
            text=True,
            timeout=8,
        ).stdout
    except (OSError, subprocess.SubprocessError):
        return []
    seen: dict[str, None] = {}
    for line in out.splitlines():
        for name in line.split(","):
            name = name.strip()
            if name and name.isprintable():
                seen.setdefault(name, None)
    return list(seen)


def font_whitelist(extra: list[str] | None = None) -> str:
    names = list(
        dict.fromkeys([*BASE_FONTS, *(extra if extra is not None else chinese_fonts())])
    )
    return ",".join(n for n in names if "," not in n)


def plan(
    p: ProfilePrefs, languages: str, proxy_prefs: dict, fonts: list[str] | None = None
) -> dict:
    """偏好 -> {firefox pref: value}。纯函数 (fonts 可注入, 便于测试)。"""
    out: dict[str, object] = dict(proxy_prefs)
    if languages:
        out["intl.accept_languages"] = languages
        out["intl.locale.requested"] = languages.split(",")[0].strip()
        out["intl.regional_prefs.use_os_locales"] = False
    for key, mode in (
        ("desktop-notification", p.notifications),
        ("geo", p.geolocation),
        ("camera", p.camera),
        ("microphone", p.microphone),
    ):
        out["permissions.default." + key] = _PERMISSION[mode]
    out["geo.enabled"] = p.geolocation != "block"  # 只给权限, 不覆盖坐标
    out["permissions.default.image"] = 2 if p.block_images else 1
    out.update(_webrtc(p.webrtc))
    if p.dns_privacy:
        out["network.trr.mode"] = 3
        out["network.trr.uri"] = p.doh_url
    if p.do_not_track != INHERIT:
        out["privacy.donottrackheader.enabled"] = p.do_not_track == ON
    if p.gpc != INHERIT:
        out["privacy.globalprivacycontrol.enabled"] = p.gpc == ON
        out["privacy.globalprivacycontrol.functionality.enabled"] = p.gpc == ON
    if p.startup_url:
        out["browser.startup.page"] = 1
        out["browser.startup.homepage"] = p.startup_url
    out["font.system.whitelist"] = (
        font_whitelist(fonts) if p.firefox_font_restriction else ""
    )
    out["privacy.resistFingerprinting"] = p.firefox_resist_fingerprinting
    out["webgl.disabled"] = p.firefox_disable_webgl
    return out


def _webrtc(policy: str) -> dict:
    if policy == INHERIT:
        return {}
    if policy == "disable":
        return {_PEER + "enabled": False}
    strict = policy in ("public", "proxy")
    return {
        _PEER + "enabled": True,
        _PEER + "ice.default_address_only": strict,
        _PEER + "ice.no_host": strict,
        _PEER + "ice.proxy_only": policy == "proxy",
    }


def render(values: dict) -> str:
    lines = [MANAGED_BEGIN]
    for key, value in values.items():
        lines.append(
            "user_pref(%s, %s);"
            % (json.dumps(key), json.dumps(value, ensure_ascii=False))
        )
    lines.append(MANAGED_END)
    return "\n".join(lines) + "\n"


def strip_managed(text: str) -> str:
    if MANAGED_BEGIN not in text:
        return text
    head, _, rest = text.partition(MANAGED_BEGIN)
    _, _, tail = rest.partition(MANAGED_END)
    return head.rstrip("\n") + ("\n" if head.strip() else "") + tail.lstrip("\n")


def write(profile_dir: Path, values: dict) -> Path:
    """把托管块写到 user.js 末尾 (替换旧托管块, 其余行原样保留)。"""
    path = profile_dir / USER_JS
    text = path.read_text("utf-8") if path.exists() else ""
    body = strip_managed(text)
    if body and not body.endswith("\n"):
        body += "\n"
    path.write_text(body + render(values), "utf-8")
    return path


def read_managed(profile_dir: Path) -> dict:
    """读回托管块 (检测页用: 判"配置是否已写入")。"""
    path = profile_dir / USER_JS
    if not path.exists():
        return {}
    text = path.read_text("utf-8", "replace")
    if MANAGED_BEGIN not in text:
        return {}
    block = text.split(MANAGED_BEGIN, 1)[1].split(MANAGED_END, 1)[0]
    out: dict = {}
    for line in block.splitlines():
        line = line.strip()
        if not (line.startswith("user_pref(") and line.endswith(");")):
            continue
        name, _, value = line[len("user_pref(") : -2].partition(",")
        try:
            out[json.loads(name)] = json.loads(value)
        except ValueError:
            continue
    return out


def reported_timezone(p: ProfilePrefs) -> str:
    """严格指纹保护开启时网页看到的时区恒为 UTC; 否则空串 (= 由 TZ 决定)。"""
    return RFP_TIMEZONE if p.firefox_resist_fingerprinting else ""
