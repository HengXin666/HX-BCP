"""副本级「区域 / 网站设置 / Firefox 设置 / 隔离」偏好 —— 字段定义与互斥校验的唯一处。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 为什么单独一个模型 (而不是继续往 Fingerprint 里塞)

Fingerprint 描述的是「网页能读到的设备特征」(UA/GPU/屏幕...), 而这里描述的是
「浏览器本身的配置」(权限默认值 / WebRTC 策略 / DoH / 字体白名单 ...)。两者的落点不同:
前者走启动参数与注入脚本, 后者写进 profile 的 Preferences / user.js。
混在一起会让「改了某字段要不要重启」「哪个内核支持」这些判据散落各处。

## 兼容性 (保留全部既有行为)

每个会改变既有行为的字段都有 ``inherit`` 档且**默认就是 inherit**:
老环境 (environments.json 里没有 prefs 键) 反序列化后与改动前**逐字节同样启动**。
"""

from __future__ import annotations

from typing import Literal
from zoneinfo import ZoneInfo

from pydantic import BaseModel, Field

Permission = Literal["ask", "allow", "block"]
LanguageMode = Literal["inherit", "ip", "custom"]
TimezoneMode = Literal["inherit", "system", "ip", "custom"]
WebRtcPolicy = Literal["inherit", "default", "public", "proxy", "disable"]
Tristate = Literal["inherit", "on", "off"]
Isolation = Literal["process", "sandbox"]
FontIsolation = Literal["inherit", "baseline", "custom"]
WindowSystem = Literal["auto", "x11", "wayland"]
SandboxX11 = Literal["nested", "shared"]

# Firefox 严格指纹保护 (privacy.resistFingerprinting) 会把时区统一报成 UTC
RFP_TIMEZONE = "UTC"
INHERIT, MODE_IP, MODE_CUSTOM, ON = "inherit", "ip", "custom", "on"
DEFAULT_DOH_URL = "https://cloudflare-dns.com/dns-query"


class ProfilePrefs(BaseModel):
    """副本偏好。所有 inherit / 空值 = 不写, 沿用浏览器或既有逻辑。"""

    # ---- 语言 / 时区 ----
    language_mode: LanguageMode = (
        "inherit"  # inherit=沿用 fingerprint.language + location_mode
    )
    languages: list[str] = Field(default_factory=list)  # custom 模式: ["en-US", "en"]
    timezone_mode: TimezoneMode = "inherit"  # system=跟随电脑系统 (不设 TZ)
    timezone: str = ""  # custom 模式的 IANA 时区
    # ---- 定位: 只给权限, 不覆盖坐标 (位置由浏览器自己提供) ----
    geolocation: Permission = "ask"
    # ---- 网站设置 ----
    startup_url: str = ""
    webrtc: WebRtcPolicy = "inherit"
    dns_privacy: bool = False  # True = 严格 DoH (Chromium secure / Firefox TRR mode 3)
    doh_url: str = DEFAULT_DOH_URL
    do_not_track: Tristate = "inherit"
    gpc: Tristate = "inherit"  # Global Privacy Control (仅 Firefox 有原生开关)
    notifications: Permission = "ask"
    camera: Permission = "ask"
    microphone: Permission = "ask"
    block_images: bool = False
    # ---- Firefox 专属 ----
    firefox_font_restriction: bool = False  # 限制可见系统字体 (保留电脑上的中文字体)
    firefox_resist_fingerprinting: bool = False  # 严格指纹保护 (时区恒 UTC)
    firefox_disable_webgl: bool = False
    # ---- 隔离 ----
    isolation: Isolation = (
        "process"  # sandbox = bubblewrap 命名空间沙箱 (Linux, 需 bwrap)
    )
    window_system: WindowSystem = (
        "auto"  # auto/x11 = 既有 X 行为; wayland = 原生 Wayland
    )
    sandbox_x11: SandboxX11 = (
        "nested"  # 沙箱 + X 桌面: Xephyr 嵌套 / 显式共享 (见 display_iso)
    )
    sandbox_gpu: bool = True  # False = 沙箱内拿掉 /dev/dri (软件渲染, WebGL 指纹会变)
    sandbox_seccomp: bool = True  # 沙箱内 seccomp 拒绝表 (见 seccomp_bpf)
    # ---- 系统字体隔离 (所有内核, 见 fontiso) ----
    font_isolation: FontIsolation = "inherit"
    fonts: list[str] = Field(
        default_factory=list
    )  # custom: 允许的字体族 (中文字体总保留)


def valid_timezone(value: str) -> bool:
    if not value or len(value) > 100:
        return False
    try:
        ZoneInfo(value)
        return True
    except Exception:  # noqa: BLE001
        return False


def valid_lang(tag: str) -> bool:
    tag = tag.strip()
    if not tag or len(tag) > 35:
        return False
    return all(part.isalnum() and len(part) <= 8 for part in tag.split("-"))


def valid_startup(url: str) -> bool:
    return url == "about:blank" or url.startswith(("http://", "https://"))
