"""浏览器内核适配层 —— 「这个副本用哪个可执行文件、属于哪一族」的唯一判据。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 三族, 多内核

    blink   Chromium 系: cloak (CloakBrowser) / chrome / chromium / edge / brave
    gecko   Firefox 系:  firefox / librewolf
    webkit  WebKit 系:   safari (Linux 上为 WebKitGTK MiniBrowser, 见 webkit.py)
                         webkit (Playwright 的 Linux WebKit 构建, 本机 runner, 见 webkit_local.py)
                         safarier (macOS 原生 WebKit, 经它自己的 CLI 驱动, 见 safarier.py)

族决定**怎么配置** (Preferences JSON vs user.js、CDP vs 远程调试协议); 内核只决定
**用哪个二进制**。新增一个内核 = 在 KERNELS 里加一行, 不需要改启动逻辑。

## 兼容性 (保留既有行为)

``fingerprint.kernel`` 为空 = 旧环境: 族仍由 ``fingerprint.browser`` (sun/flower) 决定,
可执行文件仍取 ``settings.chromium_path / firefox_path`` —— 与改动前完全相同。

## browser-gate

本仓门禁只允许 Chromix/CloakBrowser。用户 2026-10-16 明确要求「兼容多种浏览器内核」,
故本模块**只定位**其他内核的可执行文件 (逐行 allow-legacy 标注, 可审计); 默认内核
仍是 CloakBrowser (探测到时优先)。
"""

from __future__ import annotations

import shutil
import sys
from dataclasses import dataclass, field
from pathlib import Path

from . import cloak, safarier, webkit_local

BLINK = "blink"
GECKO = "gecko"
WEBKIT = "webkit"
LEGACY_GECKO_BROWSER = (
    "flower"  # Fingerprint.browser 的既有取值: sun=Chromium, flower=Firefox
)


@dataclass(frozen=True)
class Kernel:
    id: str
    label: str
    family: str
    executables: tuple[str, ...] = field(default_factory=tuple)
    mode: str = "binary"  # binary: 我们起进程 + profile 目录; runner: 本机 runner 进程; external: 外部应用自管
    features: dict | None = None  # 为空 = 用族的 FEATURES
    how: str = ""  # 为空 = 用族的 HOW
    platforms: tuple[str, ...] = ()  # 只能跑在这些宿主系统上 (sys.platform 前缀), 为空 = 不限


# 可执行名逐行登记; 每行带 allow-legacy 标注 (门禁按行判定, 故禁止格式化器折行)
# fmt: off
_EXE_CHROME = ("google-chrome-stable", "google-chrome")  # browser-gate: allow-legacy 用户要求多内核, 仅定位
_EXE_CHROMIUM = ("chromium", "chromium-browser")  # browser-gate: allow-legacy 用户要求多内核, 仅定位
_EXE_FIREFOX = ("firefox", "firefox-esr")  # browser-gate: allow-legacy 用户要求多内核, 仅定位
_ID_FIREFOX = "firefox"  # browser-gate: allow-legacy 内核 id
_EXE_SAFARI = ("/usr/lib/webkit2gtk-4.1/MiniBrowser", "/usr/libexec/webkit2gtk-4.1/MiniBrowser", "/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/MiniBrowser", "/usr/lib/aarch64-linux-gnu/webkit2gtk-4.1/MiniBrowser")  # browser-gate: allow-legacy 用户要求多内核, 仅定位 (Arch / Fedora / Debian amd64 / Debian arm64)
_ID_SAFARI = "safari"  # browser-gate: allow-legacy 内核 id
# 我们自己起的 MiniBrowser 进程: 走 _spawn (沙箱与代理 relay 同 Chromium), 但不说 CDP
_SAFARI_FEATURES = {"cdp": False, "live_view": False, "cookies": False, "sandbox": True, "fingerprint_check": False, "proxy": True, "agent_cli": False}
KERNELS: tuple[Kernel, ...] = (
    Kernel("cloak", "CloakBrowser (Chromium 补丁版)", BLINK),
    Kernel("chrome", "Google Chrome", BLINK, _EXE_CHROME),  # browser-gate: allow-legacy 内核 id
    Kernel("chromium", "Chromium", BLINK, _EXE_CHROMIUM),  # browser-gate: allow-legacy 内核 id
    Kernel("edge", "Microsoft Edge", BLINK, ("microsoft-edge-stable", "microsoft-edge")),
    Kernel("brave", "Brave", BLINK, ("brave-browser", "brave")),
    Kernel(_ID_FIREFOX, "Mozilla Firefox", GECKO, _EXE_FIREFOX),
    Kernel("librewolf", "LibreWolf", GECKO, ("librewolf",)),
    Kernel(_ID_SAFARI, "Safari (WebKitGTK MiniBrowser)", WEBKIT, _EXE_SAFARI, features=_SAFARI_FEATURES, how="MiniBrowser 参数 + user script (见 webkit.py)"),  # browser-gate: allow-legacy 内核 id
    Kernel(webkit_local.KERNEL_ID, "WebKit (Linux 本机, Playwright 构建)", WEBKIT, mode="runner", features=webkit_local.FEATURES, how=webkit_local.HOW),
    Kernel(safarier.KERNEL_ID, "Safarier (macOS WebKit)", WEBKIT, mode="external", platforms=("darwin",)),
)
# fmt: on
_BY_ID = {k.id: k for k in KERNELS}
# 每族能用的能力, 控制台内核页与环境详情据此说明 "这个内核不支持什么"; 判据只在这里
_BINARY_FEATURES = {"cdp": True, "live_view": True, "cookies": True, "sandbox": True, "fingerprint_check": True,
                    "proxy": True, "agent_cli": False}
FEATURES = {
    BLINK: _BINARY_FEATURES,
    GECKO: {**_BINARY_FEATURES, "cdp": False, "live_view": False, "fingerprint_check": False},
    WEBKIT: safarier.FEATURES,
}
HOW = {BLINK: "Preferences JSON + CDP", GECKO: "user.js + 远程调试协议", WEBKIT: "外部应用 CLI (profile/launch/agent)"}  # webkit_local 用 Kernel.how 覆盖


def features_of(k: Kernel) -> dict:
    return k.features or FEATURES[k.family]


def get(kernel_id: str) -> Kernel | None:
    return _BY_ID.get((kernel_id or "").strip().lower())


def family_of(env) -> str:
    """副本所属内核族 (blink / gecko / webkit), kernel 为空时按既有 browser 字段判定"""
    k = get(getattr(env.fingerprint, "kernel", "") or "")
    if k is not None:
        return k.family
    if env.fingerprint.browser == _ID_SAFARI:  # kernel 为空的 safari 旧环境兜底
        return WEBKIT
    return GECKO if env.fingerprint.browser == LEGACY_GECKO_BROWSER else BLINK


def speaks_cdp(env) -> bool:
    """副本是否走 CDP. 纯函数: 只看内核族, 不探测, 不读文件; 仅 webkit 族返回 False."""
    return family_of(env) != WEBKIT


def legacy_browser_for(kernel_id: str) -> str:
    """kernel -> 既有 browser 字段值 (前端/账号包按 sun/flower/safari 展示)."""
    k = get(kernel_id)
    if k is not None and k.family == WEBKIT:
        return _ID_SAFARI
    return LEGACY_GECKO_BROWSER if k is not None and k.family == GECKO else "sun"


def detect(kernel_id: str) -> str:
    """探测内核可执行文件路径; 找不到返回空串。"""
    k = get(kernel_id)
    if k is None:
        return ""
    if k.id == "cloak":
        return cloak.cloak_binary()
    for name in k.executables:
        found = shutil.which(name)
        if found:
            return found
    return ""


def resolve_executable(env, settings) -> str:
    """副本实际要启动的可执行文件。优先级: settings.kernel_paths[kernel] > 探测 > 族默认。

    webkit 族返回 WebKit 构建路径或 Safarier 命令行, 只作展示与体检, 启动走 external.start
    """
    k = get(getattr(env.fingerprint, "kernel", "") or "")
    if k is None and family_of(env) == WEBKIT:  # kernel 为空的 safari 旧环境: 按 safari 内核解析
        k = _BY_ID[_ID_SAFARI]
    if k is None:  # 旧环境: 行为与改动前一致
        return (
            settings.firefox_path if family_of(env) == GECKO else settings.chromium_path
        )
    if k.id == webkit_local.KERNEL_ID:
        return webkit_local.executable(settings)
    if k.id == safarier.KERNEL_ID:
        return " ".join(safarier.command(settings))
    configured = (getattr(settings, "kernel_paths", {}) or {}).get(k.id, "")
    if configured and Path(configured).exists():
        return configured
    return detect(k.id)


def _row(k: Kernel, settings) -> dict:
    configured = (getattr(settings, "kernel_paths", {}) or {}).get(k.id, "")
    if k.id == webkit_local.KERNEL_ID:
        detected = webkit_local.detect()
        path = webkit_local.executable(settings)
        ok = webkit_local.available(settings)
        source = "configured" if configured and Path(configured).exists() else "detected" if detected else "missing"
        note = "" if ok else ("配置的路径不存在" if configured and not Path(configured).exists() else webkit_local.REQUIREMENT)
    elif k.id == safarier.KERNEL_ID:
        path = " ".join(safarier.command(settings))
        ok = safarier.available(settings)
        source = "configured" if configured else "detected" if path else "missing"
        note = "" if ok else safarier.REQUIREMENT
    else:
        detected = detect(k.id)
        path = configured or detected
        ok = bool(path) and Path(path).exists()
        source = "configured" if configured else "detected" if detected else "missing"
        note = "" if ok else ("配置的路径不存在" if configured else "未在 PATH 中找到, 可在这里填可执行文件路径")
    return {"id": k.id, "label": k.label, "family": k.family, "mode": k.mode, "path": path, "configured": configured,
            "source": source, "available": ok, "note": note, "how": k.how or HOW[k.family], "features": features_of(k),
            "platforms": list(k.platforms), "host_supported": host_supported(k)}


def host_supported(k: Kernel) -> bool:
    """本宿主能否直接跑这个内核; 不能时仍可经远程命令使用 (如 ssh 到 Mac 跑 Safarier)"""
    return not k.platforms or sys.platform.startswith(k.platforms)


def default_kernel(settings, rows: list[dict] | None = None) -> str:
    """新建环境的默认内核: settings.default_kernel 可用就用它, 否则 cloak, 否则第一个可用的 binary 内核"""
    rows = rows if rows is not None else [_row(k, settings) for k in KERNELS]
    usable = {r["id"] for r in rows if r["available"]}
    want = (getattr(settings, "default_kernel", "") or "").strip().lower()
    if want in usable:
        return want
    if "cloak" in usable:
        return "cloak"
    return next((r["id"] for r in rows if r["id"] in usable and r["mode"] == "binary"), "")


def list_kernels(settings) -> list[dict]:
    rows = [_row(k, settings) for k in KERNELS]
    chosen = default_kernel(settings, rows)
    for r in rows:
        r["default"] = r["id"] == chosen
    return rows


def set_path(settings, kernel_id: str, path: str):
    """写 settings.kernel_paths[kernel_id]; 空串 = 清掉, 回到自动探测, 返回改过的 settings, 调用方负责保存"""
    k = get(kernel_id)
    if k is None:
        raise ValueError("unknown kernel: %s" % kernel_id)
    paths = dict(getattr(settings, "kernel_paths", {}) or {})
    if path.strip():
        paths[k.id] = path.strip()
    else:
        paths.pop(k.id, None)
    settings.kernel_paths = paths
    return settings
