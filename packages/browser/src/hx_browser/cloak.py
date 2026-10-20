"""CloakBrowser 内核接入 —— 二进制定位、逐环境指纹派生、原生 --fingerprint-* 开关。

为什么换内核 (2026-10-10)
------------------------
hx-browser 原先用 google-chrome-stable, 有两个各自独立、且**都静默**的问题:
  1. browser_gate 判它违规 (本仓只允许 CloakBrowser);
  2. Chrome 136+ 收紧、154 彻底移除 --load-extension ⇒ 扩展里的整套伪装**从未生效**,
     而面板显示一切正常。改走 CDP 注入能补上, 但那是在**没打补丁的** Chromium 上
     硬凑; CloakBrowser 的补丁版原生就有 --fingerprint-* 一整套开关。

★ 关键差异 (实测)
--force-webrtc-ip-handling-policy=disable_non_proxied_udp 在**未打补丁的** Chrome 154 上
**零效果** (四组交叉全部仍吐 srflx); 同参数下 CloakBrowser 146 的 7 个 STUN 全 empty。
CloakBrowser 还有原生的 --fingerprint-webrtc-ip=disable_non_proxied_udp, 不依赖
"命令行开关是否被遵守"。

为什么 UA 主版本取自真实二进制
------------------------------
cloak_launch.py 的 _CHROME_MAJORS = [131..136], 而二进制是 146 ⇒ UA 说 Chrome/133、
引擎是 146, 这是**可被直接检测**的不一致 (两代的特性面不同)。本模块改为探测
真实主版本 —— 与 fingerprint._detect_kernel_major 同一原则: UA 与内核同源最强。

# Agent Note: .agents/notes/implemented/architecture/2026-10-10-hxbrowser-cloakbrowser-kernel.md
"""

# browser-gate: allow-legacy 本模块只**定位并拼参数** CloakBrowser 官方二进制, 不启动别的
# 浏览器; chromix 未装进 hx-browser 的 venv, 无法走 chromix.ensure_binary (同 jev_gate.py)。
from __future__ import annotations

import hashlib
import logging
import json
import random
import re
import subprocess
import sys
from pathlib import Path

from .fingerprint import build_fingerprint_extension

CLOAK_HOME = (
    Path.home() / ".cloakbrowser"
)  # browser-gate: allow-legacy CloakBrowser 官方安装目录
SEED_FILE = ".cloak-seed"

# 与 cloak_launch.py 同源的池子 —— 那套画像是**生产验证过**的 (所有 GitHub 账号
# 都由 CloakBrowser 路径注册), 不用本仓自造那套未验证的。
# 唯一改动: 主版本不再随机取, 见 kernel_major()。
_UA_OS = {
    "windows": "Windows NT 10.0; Win64; x64",
    "macos": "Macintosh; Intel Mac OS X 10_15_7",
    "linux": "X11; Linux x86_64",
}
_PLATFORM_VERSION = {"windows": "10.0.0", "macos": "14.5.0", "linux": "6.8.0"}
_GPU = {
    "windows": [
        (
            "Google Inc. (NVIDIA)",
            "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)",
        ),
        (
            "Google Inc. (Intel)",
            "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)",
        ),
        (
            "Google Inc. (AMD)",
            "ANGLE (AMD, AMD Radeon RX 580 Direct3D11 vs_5_0 ps_5_0, D3D11)",
        ),
    ],
    "macos": [
        (
            "Google Inc. (Apple)",
            "ANGLE (Apple, ANGLE Metal Renderer: Apple M1, Unspecified Version)",
        ),
    ],
    # 与 fingerprint.WEBGL_BY_OS["Linux"] 同一套。原先只有 2 条, 实测两个环境就撞车
    # (同一张 GPU 脸), 扩到 5 条才谈得上"逐环境差异"。
    "linux": [
        ("Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 OpenGL 4.5)"),
        (
            "Google Inc. (NVIDIA)",
            "ANGLE (NVIDIA, NVIDIA GeForce GTX 1660 Ti OpenGL 4.5)",
        ),
        ("Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 6600 OpenGL 4.5)"),
        (
            "Google Inc. (Intel)",
            "ANGLE (Intel, Mesa Intel(R) UHD Graphics 630 (CML), OpenGL 4.6)",
        ),
        (
            "Google Inc. (Intel)",
            "ANGLE (Intel, Mesa Intel(R) Xe Graphics (TGL LP), OpenGL 4.6)",
        ),
    ],
}
_SCREENS = [
    (1920, 1080),
    (2560, 1440),
    (1536, 864),
    (1440, 900),
    (1366, 768),
    (1680, 1050),
]
_CORES = [4, 8, 12, 16]
_MEM = [8, 16, 32]


def cloak_binary() -> str:
    """定位 CloakBrowser 的 chrome 可执行文件 (取版本号最大的那个)。"""
    if not CLOAK_HOME.is_dir():
        return ""
    cands = sorted(
        CLOAK_HOME.glob("chromium-*/chrome")
    )  # browser-gate: allow-legacy 同上, 只定位不启动
    return str(cands[-1]) if cands else ""


def is_cloak(executable: str) -> bool:
    return bool(executable) and str(executable).startswith(str(CLOAK_HOME))


def kernel_major(executable: str) -> int:
    """真实二进制主版本 (146) —— UA 必须跟着它走, 不能随机。"""
    try:
        out = subprocess.run(
            [executable, "--version"], capture_output=True, text=True, timeout=8
        ).stdout.strip()
        m = re.search(r"(\d+)\.(\d+)\.(\d+)", out)
        return int(m.group(1)) if m else 0
    except Exception:  # noqa: BLE001
        return 0


def host_platform() -> str:
    """宿主平台 —— 在 Linux 上声称 Windows 会让 UA/platform 与真实字体、GPU 矛盾
    (本仓 2026-09-18 实测直接导致 /signup 被 DataDome 拦), 所以派生只取宿主这一档。
    """
    if sys.platform.startswith("linux"):
        return "linux"
    if sys.platform == "darwin":
        return "macos"
    return "windows"


def seed_for(env_id: str, profile_dir: Path) -> int:
    """逐环境稳定 seed —— 同账号每次启动同一张脸, 不同账号之间差异显著。

    落盘到 profile_dir/.cloak-seed: env.id 变了也能找回, 且与 cloak_launch 同约定。
    """
    f = profile_dir / SEED_FILE
    try:
        if f.exists():
            return int(f.read_text("utf-8").strip())
    except (OSError, ValueError):
        pass
    h = hashlib.sha256(env_id.encode()).hexdigest()
    seed = int(h[:8], 16) % 80000
    try:
        profile_dir.mkdir(parents=True, exist_ok=True)
        f.write_text(str(seed), "utf-8")
    except OSError:
        pass
    return seed


def derive(seed: int, executable: str, os_name: str = "linux") -> dict:
    """由 seed 确定性派生一整套自洽指纹。

    os_name 固定由调用方给 (本仓宿主是 Linux ⇒ 只派生 linux 档: 在 Linux 上声称
    Windows 会让 UA/platform 与真实字体、GPU 矛盾, 实测直接被 DataDome 拦)。
    """
    r = random.Random(int(seed) * 7919 + 104729)
    major = kernel_major(executable) or 146
    ua = (
        "Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) "
        "Chrome/%d.0.%d.%d Safari/537.36"
        % (
            _UA_OS.get(os_name, _UA_OS["linux"]),
            major,
            r.randint(0, 9),
            r.randint(1000, 6999),
        )
    )
    vendor, renderer = r.choice(_GPU.get(os_name) or _GPU["linux"])
    w, h = r.choice(_SCREENS)
    return {
        "platform": os_name,
        "platform_version": _PLATFORM_VERSION.get(os_name, "6.8.0"),
        "ua": ua,
        "brand": r.choice(["Chrome", "Google Chrome"]),
        "brand_version": str(major),
        "gpu_vendor": vendor,
        "gpu_renderer": renderer,
        "screen_w": w,
        "screen_h": h,
        "cores": r.choice(_CORES),
        "mem": r.choice(_MEM),
        "kernel_major": major,
    }


def args(fp: dict, timezone: str = "", locale: str = "") -> list[str]:
    """展开成 CloakBrowser 原生开关。

    --fingerprint-webrtc-ip=disable_non_proxied_udp 是**原生**实现的 WebRTC 防泄露,
    与命令行 --force-webrtc-ip-handling-policy 不是一回事 (后者在本机 Chrome 154 上无效)。
    """
    out = [
        f"--fingerprint-platform={fp['platform']}",
        f"--fingerprint-platform-version={fp['platform_version']}",
        f"--user-agent={fp['ua']}",
        f"--fingerprint-brand={fp['brand']}",
        f"--fingerprint-brand-version={fp['brand_version']}",
        f"--fingerprint-gpu-vendor={fp['gpu_vendor']}",
        f"--fingerprint-gpu-renderer={fp['gpu_renderer']}",
        f"--fingerprint-hardware-concurrency={fp['cores']}",
        f"--fingerprint-device-memory={fp['mem']}",
        f"--fingerprint-screen-width={fp['screen_w']}",
        f"--fingerprint-screen-height={fp['screen_h']}",
        "--fingerprint-webrtc-ip=disable_non_proxied_udp",
    ]
    if timezone:
        out.append(f"--fingerprint-timezone={timezone}")
    if locale:
        out.append(f"--fingerprint-locale={locale}")
    # 窗口宽高对齐 screen: 恒定 1050 而 screen 派生 1366/2560 是廉价并行度检测器
    # (cloak_launch 2026-10-10 实测同因)。
    out.append("--window-size=%d,%d" % (fp["screen_w"], max(480, fp["screen_h"] - 90)))
    return out


def launch_args(env, profile_dir: Path, exe: str, dark: bool) -> list[str]:
    """按内核类型给出指纹相关启动参数。

    CloakBrowser  → 原生 --fingerprint-* (权威, 不再叠加 JS 注入);
    其他内核      → 指纹扩展 (Chrome 136+ 起必须同时给 --disable-extensions-except)。
    """
    fp = env.fingerprint
    if is_cloak(exe):
        cf = derive(seed_for(env.id, profile_dir), exe, os_name=host_platform())
        logging.getLogger(__name__).info(
            "env=%s cloak fingerprint %s", env.id, summary(cf)
        )
        return args(cf, timezone=fp.timezone, locale=(fp.language or "").split(",")[0])

    out = ["--window-size=" + (fp.resolution or "1280x820")]
    if fp.user_agent:
        out.append("--user-agent=" + fp.user_agent)
    if fp.language:
        out.append("--lang=" + fp.language.split(",")[0])
    # 裸 --load-extension 自 Chrome 136+ 已失效 (实测页内 __hx_fp_injected__ 恒 false)。
    ext_dirs = [build_fingerprint_extension(env, profile_dir, dark=dark)]
    for ext in env.launch.extensions:
        if Path(ext).exists():
            ext_dirs.append(Path(ext))
    joined = ",".join(str(d) for d in ext_dirs)
    return out + ["--load-extension=" + joined, "--disable-extensions-except=" + joined]


def summary(fp: dict) -> str:
    return json.dumps({k: v for k, v in fp.items() if k != "ua"}, ensure_ascii=False)
