"""出口防泄露与指纹注入 —— 从 browser.py / fingerprint.py 拆出的两个守护面。

拆出来两个理由:
  1. 行数棘轮: 两个源文件都已顶格, 新逻辑塞进去必然超限;
  2. 同一职责 (页面看不到真实出口 + 指纹伪装) 不该散在两个模块里。

# Agent Note: .agents/notes/implemented/bug-fix/2026-10-10-hxbrowser-webrtc-block-and-proxy-invariants.md
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import re
import subprocess
import sys
from pathlib import Path

from . import cdp
from .fingerprint import WEBGL_BY_OS, _CONTENT_JS, _noise_js, parse_resolution, platform_for, ua_for
from .models import Environment

log = logging.getLogger(__name__)

# ---------- 凭据注入器 (常驻独立进程) ----------

# Chromium 拒收内嵌 user:pass@ 的 --proxy-server (一律 ERR_NO_SUPPORTED_PROXIES),
# 所以凭据只能走 CDP Fetch 域注入。这里按文件路径复用 gh-registrar 的**唯一**实现,
# 不另写一份 —— "同一事实两处定义必然不同同步"是本仓栽过的坑。
_PROXY_AUTH_RUNNER = (
    Path(__file__).resolve().parents[4]
    / "workspaces/browser-auto/gh-registrar/server/app/adapters/proxy_auth_runner.py"
)
# env_id -> 注入器进程。Fetch 域一旦 enable, 未决请求会挂起到有 enable 方 respond,
# 注入器必须活得和浏览器一样久, 由 stop_auth_injector 显式回收。
_AUTH_PROCS: dict[str, subprocess.Popen] = {}
# 注入器日志: 独立进程唯一的可观测点。2026-10-10 它因缺 websocket-client 静默死掉,
# 服务端零报错, 排查了很久 —— 所以 stderr 必须落盘。
_AUTH_LOG = str(
    Path(__file__).resolve().parents[2] / "data" / "proxy-auth-injector.log"
)


def start_auth_injector(env: Environment, debug_port: int) -> None:
    """代理带账号密码时, 起常驻凭据注入器。

    必须常驻: Fetch 域一旦 enable, 命中 pattern 的请求会被**暂停**到有 enable 方 respond;
    注入器随浏览器退出则所有未决请求永久挂起 (表现为页面全空)。
    """
    px = env.proxy
    if not px or not px.username:
        return
    runner = _PROXY_AUTH_RUNNER
    if not runner.exists():
        log.warning("proxy auth runner missing at %s; proxy creds NOT injected", runner)
        return
    old = _AUTH_PROCS.pop(env.id, None)
    if old is not None:
        try:
            old.terminate()
        except Exception:  # noqa: BLE001
            pass
    try:
        Path(_AUTH_LOG).parent.mkdir(parents=True, exist_ok=True)
        logf = open(_AUTH_LOG, "a")  # noqa: SIM115
        proc = subprocess.Popen(
            [
                sys.executable,
                str(runner),
                "--port",
                str(debug_port),
                "--user",
                px.username,
                "--password",
                px.password,
            ],
            stdout=logf,
            stderr=logf,
            stdin=subprocess.DEVNULL,
            start_new_session=True,
        )
        _AUTH_PROCS[env.id] = proc
        log.info("env=%s proxy auth injector started (pid=%s)", env.id, proc.pid)
    except Exception as e:  # noqa: BLE001
        log.warning("start proxy auth injector failed for %s: %s", env.id, e)


def stop_auth_injector(env_id: str) -> None:
    """回收注入器。必须先于浏览器退出 —— 反过来会让 Fetch 未决请求挂死。"""
    proc = _AUTH_PROCS.pop(env_id, None)
    if proc is None:
        return
    try:
        proc.terminate()
        proc.wait(timeout=5)
    except Exception:  # noqa: BLE001
        try:
            proc.kill()
        except Exception:  # noqa: BLE001
            pass


# ---------- 指纹注入 (CDP 主世界) ----------


async def inject_fingerprint(
    env: Environment,
    debug_port: int,
    dark: bool = False,
    rounds: int = 24,
    profile_dir: Path | None = None,
    exe: str = "",
) -> None:
    """经 CDP 把指纹脚本注进**主世界**。

    Chrome 136+ 收紧、154 彻底移除 --load-extension ⇒ 扩展里的伪装全部空转
    (实测四组开关组合, window.__hx_fp_injected__ 恒 false)。这里用官方通道补上:
      · Page.addScriptToEvaluateOnNewDocument —— 之后每个新文档在解析前就注入;
      · Runtime.evaluate                   —— 已经加载完的那一页立刻补一次。
    新标签页在轮询窗口内被补上; 之后靠新文档钩子 (未做长期验证)。
    """
    script = fingerprint_script(env, dark=dark, profile_dir=profile_dir)
    done: set[str] = set()
    for _ in range(rounds):
        try:
            targets = await cdp.http_json(
                "http://127.0.0.1:%d/json/list" % debug_port, 5
            )
        except Exception:  # noqa: BLE001
            targets = []
        for t in targets or []:
            if t.get("type") != "page" or not t.get("webSocketDebuggerUrl"):
                continue
            key = t.get("id") or t["webSocketDebuggerUrl"]
            if key in done:
                continue
            done.add(key)
            try:
                conn = await cdp.CdpConnection(t["webSocketDebuggerUrl"]).connect(
                    timeout=8
                )
            except Exception:  # noqa: BLE001
                done.discard(key)
                continue
            try:
                await conn.send("Page.enable", {}, timeout=10)
                await conn.send(
                    "Page.addScriptToEvaluateOnNewDocument",
                    {"source": script},
                    timeout=10,
                )
                await conn.send("Runtime.evaluate", {"expression": script}, timeout=10)
            except Exception as e:  # noqa: BLE001
                log.warning("fingerprint inject failed (%s): %s", env.id, e)
            finally:
                try:
                    await conn.close()
                except Exception:  # noqa: BLE001
                    pass
        await asyncio.sleep(1.0)
    log.info("env=%s fingerprint injected into %d page(s)", env.id, len(done))


# ---------- FP 字典与指纹脚本 ----------


_MAJOR: dict[str, str] = {}


def kernel_major(exe: str) -> str:
    """exe --version 里的主版本号, 读不出返回空串; 按路径缓存, 每次启动不重复起进程"""
    if exe not in _MAJOR:
        try:
            out = subprocess.run([exe, "--version"], capture_output=True, text=True, timeout=20, check=False).stdout
        except (OSError, subprocess.SubprocessError):
            out = ""
        m = re.search(r"(\d+)\.\d+", out)
        _MAJOR[exe] = m.group(1) if m else ""
    return _MAJOR[exe]


def effective_user_agent(env: Environment, exe: str) -> str:
    """配置了 UA 就用配置; 没配时按内核真实主版本与 fp.os 拼正常 UA, 读不出版本返回空串, 不编版本号"""
    if env.fingerprint.user_agent:
        return env.fingerprint.user_agent
    major = kernel_major(exe) if exe else ""
    return ua_for(env.fingerprint.os, major) if major else ""


def webgl_pair(env: Environment) -> tuple[str, str]:
    """配置了 renderer 就用配置; 没配时按 fp.os 从 WEBGL_BY_OS 里按 env.id 稳定挑一对, 同一环境每次同一块卡"""
    fp = env.fingerprint
    if fp.webgl_renderer:
        return fp.webgl_vendor or "", fp.webgl_renderer
    pool = WEBGL_BY_OS.get(fp.os, WEBGL_BY_OS["Linux"])
    return pool[int(hashlib.sha256(env.id.encode()).hexdigest()[:8], 16) % len(pool)]


def default_dpr(os_name: str, width: int) -> float:
    """按 os 与屏宽给一个像真机的缩放: macOS 走 Retina, 高分屏不会是 1;
    Windows/Linux 常见 1, 大屏可能是 1.25/1.5. 恒为 1 本身就是可聚类特征"""
    if os_name == "macOS":
        return 2.0 if width >= 1920 else 1.5
    if width >= 2560:
        return 1.5
    if width >= 1920:
        return 1.25
    return 1.0


def _fp_json_for(
    env: Environment, dark: bool, profile_dir: Path | None = None, exe: str = ""
) -> dict:
    """构造注入用的 FP 字典 —— 扩展路径与 CDP 路径共用同一份定义 (单一真源)。"""
    from .cloak import seed_for  # 局部导入, 与 build_fingerprint_extension 同一个 seed 来源

    fp = env.fingerprint
    vendor, renderer = webgl_pair(env)
    resolution = parse_resolution(fp.resolution) or (1280, 820)
    width, height = resolution
    inner = getattr(fp, "_fp", None) or {}
    return {
        "dark_mode": bool(dark),
        "user_agent": effective_user_agent(env, exe),
        "app_version": inner.get("app_version", ""),
        "platform": platform_for(fp.os),
        "language": (fp.language or "en-US").split(",")[0],
        "languages": [x for x in (fp.language or "en-US,en").split(",") if x]
        or ["en-US", "en"],
        "hardware_concurrency": fp.hardware_concurrency or 0,
        "device_memory": fp.device_memory or 0,
        "max_touch_points": 0 if fp.device_type == "desktop" else 5,
        "width": width,
        "height": height,
        "color_depth": fp.color_depth or 24,
        "pixel_ratio": inner.get("pixel_ratio") or default_dpr(fp.os, width),
        "orientation": inner.get("orientation", "landscape-primary"),
        "timezone_offset": None,
        "timezone_name": fp.timezone or "",
        "canvas_noise": bool(fp.canvas_noise),
        "audio_noise": bool(fp.audio_noise),
        "webgl_noise": bool(fp.webgl_noise),
        "webgl_vendor": vendor,
        "webgl_renderer": renderer,
        "noise_seed": seed_for(env.id, profile_dir) if profile_dir else 0,
        "webrtc_public_ip": fp.webrtc_public_ip or "",
        "hide_webrtc": fp.webrtc_mode == "proxy",
        "fake_media_devices": bool(inner.get("fake_media_devices", False)),
        "do_not_track": inner.get("do_not_track"),
        "notification_permission": inner.get("notification_permission", ""),
        "client_hints_brands": inner.get("client_hints_brands", []),
        "hint_platform": inner.get("hint_platform", "Windows"),
        "hint_platform_version": inner.get("hint_platform_version", ""),
        "hint_architecture": inner.get("hint_architecture", ""),
        "ua_full_version": inner.get("ua_full_version", ""),
    }


def fingerprint_script(
    env: Environment, dark: bool = False, profile_dir: Path | None = None, exe: str = ""
) -> str:
    """返回可直接注入主世界的指纹脚本 (FP_JSON 已代入)。"""

    # 两个占位符都要替换: 只换 FP_JSON 会让 %NOISE_JS% 原样留下, 整段脚本语法错误
    return _CONTENT_JS.replace("%NOISE_JS%", _noise_js()).replace(
        "%FP_JSON%", json.dumps(_fp_json_for(env, dark, profile_dir, exe))
    )
