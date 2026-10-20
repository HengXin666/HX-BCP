"""数据模型（对应 PRD §5）。"""

# Agent Note: .agents/notes/implemented/bug-fix/2026-09-19-hx-browser-zombie-and-runtime-persistence.md

from __future__ import annotations

import time
from typing import Any, Literal

from pydantic import BaseModel, Field

from .prefs import ProfilePrefs

Platform = Literal[
    "custom", "facebook", "google", "youtube", "tiktok", "x", "amazon", "shopify"
]
BrowserType = Literal["sun", "flower"]  # sun=Chromium, flower=Firefox
ProxyType = Literal["no_proxy", "direct", "system", "http", "https", "socks5"]


def now_ms() -> int:
    return int(time.time() * 1000)


class Account(BaseModel):
    username: str = ""
    password: str = ""
    two_factor: str = ""
    cookies: str = ""  # JSON / Netscape / raw string，启动时注入


class ProxyConfig(BaseModel):
    type: ProxyType = "no_proxy"
    host: str = ""
    port: int = 0
    username: str = ""
    password: str = ""
    label: str = ""


class Fingerprint(BaseModel):
    browser: BrowserType = "sun"
    # 内核 id (cloak/chrome/chromium/edge/brave/firefox/librewolf); 空 = 按 browser 推断 (旧环境)
    kernel: str = ""
    kernel_version: str = "system"
    os: str = "Windows"
    device_type: str = "desktop"
    user_agent: str = ""
    language: str = ""
    timezone: str = ""
    resolution: str = "1280x820"
    color_depth: int = 24
    hardware_concurrency: int = 0
    device_memory: float = 0
    webgl_vendor: str = ""
    webgl_renderer: str = ""
    canvas_noise: bool = False
    audio_noise: bool = False
    webgl_noise: bool = False
    webrtc_mode: str = "real"  # real | proxy | disable
    webrtc_public_ip: str = ""
    headless: bool = False  # 环境级可见性; 与全局 headless_default 取或 (desktop.resolve_display)
    tls_mode: str = "none"  # none | chrome(tls-proxy, v0.2)
    dark_mode: bool | None = None  # None=跟随全局设置；True=强制暗色主题


class Launch(BaseModel):
    urls: list[str] = Field(default_factory=list)
    extra_args: list[str] = Field(default_factory=list)
    extensions: list[str] = Field(default_factory=list)


class Storage(BaseModel):
    cloud_sync: bool = False  # 本地标记，不上传
    clear_cache_after_close: bool = False


class Environment(BaseModel):
    id: str = ""
    name: str = ""
    group: str = "默认"
    platform: str = "custom"
    tags: list[str] = Field(default_factory=list)
    country: str = ""
    region: str = ""
    city: str = ""
    location_mode: str = "match_ip"  # match_ip | custom
    account: Account = Field(default_factory=Account)
    proxy: ProxyConfig = Field(default_factory=ProxyConfig)
    fingerprint: Fingerprint = Field(default_factory=Fingerprint)
    launch: Launch = Field(default_factory=Launch)
    storage: Storage = Field(default_factory=Storage)
    notes: str = ""
    # 副本级区域/网站设置/隔离 (2026-10-16); 缺省值与改动前行为一致
    prefs: ProfilePrefs = Field(default_factory=ProfilePrefs)
    # 最近删除: 非 0 = 已移入回收站的时间戳 (ms); 彻底删除才清数据与凭据
    deleted_at: int = 0
    # 最近一次按出口 IP 匹配到的区域 {ip,country,timezone,languages,checked_at}
    last_region: dict[str, Any] = Field(default_factory=dict)
    serial_no: int = 0
    created_at: int = 0
    updated_at: int = 0
    last_open_at: int = 0
    open_count: int = 0
    # 阶段 1: 可选自动挂载 hxcp 缓存代理 (CDP Fetch 层)
    hxcp_enabled: bool = False
    # 运行时（不持久化）
    runtime: dict[str, Any] = Field(default_factory=dict, exclude=True)


class RuntimeInfo(BaseModel):
    pid: int = 0
    debug_port: int = 0
    profile_dir: str = ""
    executable: str = ""
    started_at: int = 0
    cdp_http: str = ""
    cdp_ws: str = ""
    login_state: str = "unknown"  # ok | expired | empty | unknown
    # ---- 可见窗口契约 (2026-10-13, T2/T4 依赖; 判据在 display.desktop/window) ----
    # visible 是**断言真值**, 不是意图: headless=false 且真探到桌面时先置 True,
    # 窗口起来后由 window.window_verdict 复核; 复核不过就改回 False 并写 reason。
    # 绝不拿「进程活着」冒充「窗口可见」(gh-pool 2026-10-02 实测过二者不一致)。
    visible: bool = False
    display: str = (
        ""  # ":1" = 用户真实桌面; ":7x" = Xvfb 虚拟屏 (用户看不见); "" = 无 X
    )
    xauth: str = ""  # 该 display 的 XAUTHORITY (每会话随机, 断言窗口时要用)
    window_title: str = ""  # 可见顶层窗口标题; 未断言到则为空
    window_id: str = ""  # X11 窗口 id (xdotool); 上层可据此截图/操作
    window_pos: list[int] = Field(default_factory=list)  # 实测客户区坐标 [x, y]
    # display_source 是**类别** (desktop|headless|xvfb|xvfb-failed) —— 断言比的就是它;
    # display_source_detail 是细节 ("Xwayland#2760"), 只供排查, 不参与任何判断。
    display_source: str = ""
    display_source_detail: str = ""
    reason: str = ""  # visible=false 时**必须**非空, 说明为什么用户看不见
    # ---- 2026-10-16 ----
    kernel: str = ""  # 实际启动的内核 id
    relay_port: int = 0  # 本地代理 relay 端口 (0 = 未用 relay)
    isolation: str = "process"  # process | sandbox
    config_hash: str = ""  # 启动时的配置摘要; 与当前不同 = 待重启
    # ---- 阶段 1: hxcp ----
    hxcp_pid: int = 0
    hxcp_status: str = ""  # running | stopped | failed | ""


class Proxy(BaseModel):
    id: str = ""
    label: str = ""
    type: ProxyType = "http"
    host: str = ""
    port: int = 0
    username: str = ""
    password: str = ""
    country: str = ""
    asn: str = ""
    latency_ms: int = 0
    checked_at: int = 0


class Template(BaseModel):
    id: str = ""
    name: str = ""
    description: str = ""
    fingerprint: dict[str, Any] = Field(default_factory=dict)
    proxy_strategy: dict[str, Any] = Field(default_factory=dict)
    extensions: list[str] = Field(default_factory=list)
    launch: dict[str, Any] = Field(default_factory=dict)
    created_at: int = 0


class Extension(BaseModel):
    id: str = ""
    name: str = ""
    path: str = ""
    categories: list[str] = Field(default_factory=list)
    platforms: list[str] = Field(default_factory=list)
    bind_env_ids: list[str] = Field(default_factory=list)
    created_at: int = 0


class Settings(BaseModel):
    chromium_path: str = ""
    firefox_path: str = ""
    data_dir: str = ""
    cdp_base_port: int = 43000
    api_port: int = 8325
    api_key: str = ""
    proxy_check_before_launch: bool = True
    headless_default: bool = False  # 全局关窗开关: 与 fingerprint.headless **取或**
    default_homepage: str = "about:blank"
    auto_match_timezone: bool = True
    dark_mode: bool = False  # 浏览器实例默认暗色主题（prefers-color-scheme: dark）
    # 内核 id -> 可执行文件路径 (覆盖自动探测); 例 {"chrome": "/opt/google/chrome/chrome"}
    kernel_paths: dict[str, str] = Field(default_factory=dict)
