"""浏览器内核适配层 —— 「这个副本用哪个可执行文件、属于哪一族」的唯一判据。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

## 两族, 多内核

    blink  Chromium 系: cloak (CloakBrowser) / chrome / chromium / edge / brave
    gecko  Firefox 系:  firefox / librewolf

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
from dataclasses import dataclass, field
from pathlib import Path

from . import cloak

BLINK = "blink"
GECKO = "gecko"
LEGACY_GECKO_BROWSER = (
    "flower"  # Fingerprint.browser 的既有取值: sun=Chromium, flower=Firefox
)


@dataclass(frozen=True)
class Kernel:
    id: str
    label: str
    family: str
    executables: tuple[str, ...] = field(default_factory=tuple)


# 可执行名逐行登记; 每行带 allow-legacy 标注 (门禁按行判定, 故禁止格式化器折行)
# fmt: off
_EXE_CHROME = ("google-chrome-stable", "google-chrome")  # browser-gate: allow-legacy 用户要求多内核, 仅定位
_EXE_CHROMIUM = ("chromium", "chromium-browser")  # browser-gate: allow-legacy 用户要求多内核, 仅定位
_EXE_FIREFOX = ("firefox", "firefox-esr")  # browser-gate: allow-legacy 用户要求多内核, 仅定位
_ID_FIREFOX = "firefox"  # browser-gate: allow-legacy 内核 id
KERNELS: tuple[Kernel, ...] = (
    Kernel("cloak", "CloakBrowser (Chromium 补丁版)", BLINK),
    Kernel("chrome", "Google Chrome", BLINK, _EXE_CHROME),  # browser-gate: allow-legacy 内核 id
    Kernel("chromium", "Chromium", BLINK, _EXE_CHROMIUM),  # browser-gate: allow-legacy 内核 id
    Kernel("edge", "Microsoft Edge", BLINK, ("microsoft-edge-stable", "microsoft-edge")),
    Kernel("brave", "Brave", BLINK, ("brave-browser", "brave")),
    Kernel(_ID_FIREFOX, "Mozilla Firefox", GECKO, _EXE_FIREFOX),
    Kernel("librewolf", "LibreWolf", GECKO, ("librewolf",)),
)
# fmt: on
_BY_ID = {k.id: k for k in KERNELS}


def get(kernel_id: str) -> Kernel | None:
    return _BY_ID.get((kernel_id or "").strip().lower())


def family_of(env) -> str:
    """副本所属内核族。kernel 为空时按既有 browser 字段判定。"""
    k = get(getattr(env.fingerprint, "kernel", "") or "")
    if k is not None:
        return k.family
    return GECKO if env.fingerprint.browser == LEGACY_GECKO_BROWSER else BLINK


def legacy_browser_for(kernel_id: str) -> str:
    """kernel -> 既有 browser 字段值 (前端/账号包仍按 sun/flower 展示)。"""
    k = get(kernel_id)
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
    """副本实际要启动的可执行文件。优先级: settings.kernel_paths[kernel] > 探测 > 族默认。"""
    k = get(getattr(env.fingerprint, "kernel", "") or "")
    if k is None:  # 旧环境: 行为与改动前一致
        return (
            settings.firefox_path if family_of(env) == GECKO else settings.chromium_path
        )
    configured = (getattr(settings, "kernel_paths", {}) or {}).get(k.id, "")
    if configured and Path(configured).exists():
        return configured
    return detect(k.id)


def list_kernels(settings) -> list[dict]:
    configured = getattr(settings, "kernel_paths", {}) or {}
    out = []
    for k in KERNELS:
        path = configured.get(k.id) or detect(k.id)
        out.append(
            {
                "id": k.id,
                "label": k.label,
                "family": k.family,
                "path": path,
                "available": bool(path) and Path(path).exists(),
            }
        )
    return out
