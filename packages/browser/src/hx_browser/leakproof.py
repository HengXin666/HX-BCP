"""Leak-proof invariant for every browser at creation time, the single place that decides leak defaults and exposures.

Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-leakproof-by-default.md

A new browser starts with WebRTC limited to the proxy and, when this host can run it, the bwrap sandbox, whose only
way out is the host relay. Leaving either guard off is an exposure, and an exposure is only accepted when the caller
names it in leak_ack. Exposures that this host cannot avoid are not blocked but reported by status().
"""

from __future__ import annotations

import shutil
import time

from . import desktop, display_iso, kernels, sandbox

WEBRTC_REAL = "webrtc_real"
HOST_LOOPBACK = "host_loopback"
EXPOSURES = (WEBRTC_REAL, HOST_LOOPBACK)
ACK_FIELD = "leak_ack"
SAFE_WEBRTC = "proxy"
GUARDED_WEBRTC = ("proxy", "disable")
SANDBOX, PROCESS = "sandbox", "process"
TEXT = {
    WEBRTC_REAL: "WebRTC 未限制, 网页可经 UDP 直接拿到本机真实出口 IP",
    HOST_LOOPBACK: "未开沙箱, 网页可访问本机 127.0.0.1 上的服务, 且存在绕开代理的直连路径",
}
DESKTOP_TTL_S = 30.0
_desktop_seen: list[float] = []  # [checked_at, ok], probe is a subprocess, env lists call status() per row
FIX = {
    WEBRTC_REAL: "去掉 webrtc_mode=real 用默认的 proxy",
    HOST_LOOPBACK: "去掉 isolation=process 用默认的 sandbox",
}


def acks_of(payload: dict | None) -> set[str]:
    """Exposure names the caller acknowledged in the request body, unknown names dropped."""
    raw = (payload or {}).get(ACK_FIELD) or []
    if isinstance(raw, str):
        raw = raw.split(",")
    return {str(x).strip() for x in raw if str(x).strip() in EXPOSURES}


def blink_hook(env) -> bool:
    """Blink: whether the main world RTCPeerConnection hook is injected, the only WebRTC guard on unpatched Chrome."""
    return env.fingerprint.webrtc_mode in GUARDED_WEBRTC or env.prefs.webrtc in GUARDED_WEBRTC


def webrtc_guarded(env) -> bool:
    """True when the launch path keeps WebRTC off the real address, Blink by the hook, Gecko by effective prefs."""
    if kernels.family_of(env) == kernels.GECKO:
        return effective_webrtc(env) in GUARDED_WEBRTC
    return blink_hook(env)


def exposures(env) -> list[str]:
    """Leak exposures of this configuration, in EXPOSURES order."""
    out = []
    if not webrtc_guarded(env):
        out.append(WEBRTC_REAL)
    if env.prefs.isolation != SANDBOX:
        out.append(HOST_LOOPBACK)
    return out


def sandbox_blocker(env, headless_default: bool | None = None) -> str:
    """Why this host cannot start env in the sandbox, empty when it can. Mirrors the refusals in launch_hooks."""
    if not sandbox.available():
        return "本机未安装 bubblewrap (bwrap)"
    if headless_default is None:
        from .config import settings_manager

        headless_default = settings_manager.load().headless_default
    if env.fingerprint.headless or headless_default:
        return ""
    if display_iso.wants_wayland(env):
        return "" if display_iso.wayland_socket() else "选了 Wayland 但找不到 Wayland socket"
    if env.prefs.sandbox_x11 == display_iso.NESTED and not _has_xephyr():
        if _desktop_visible():
            return "有可见桌面但缺 Xephyr, 沙箱窗口无法嵌套显示"
    return ""


def _has_xephyr() -> bool:
    return bool(shutil.which(display_iso.TOOL_XEPHYR))


def _desktop_visible() -> bool:
    """Whether a user visible desktop exists, cached for DESKTOP_TTL_S."""
    now = time.monotonic()
    if not _desktop_seen or now - _desktop_seen[0] > DESKTOP_TTL_S:
        _desktop_seen[:] = [now, float(bool(desktop.detect_user_desktop().get("ok")))]
    return bool(_desktop_seen[1])


def unavoidable(env, headless_default: bool | None = None) -> dict[str, str]:
    """Exposures this host cannot remove for env, name to reason."""
    out: dict[str, str] = {}
    if env.prefs.isolation != SANDBOX:
        why = sandbox_blocker(env, headless_default)
        if why:
            out[HOST_LOOPBACK] = why
    return out


def effective_webrtc(env) -> str:
    """The WebRTC policy the profile prefs writers should apply, prefs.webrtc wins, else a guarded fingerprint mode."""
    if env.prefs.webrtc != "inherit":
        return env.prefs.webrtc
    return env.fingerprint.webrtc_mode if env.fingerprint.webrtc_mode in GUARDED_WEBRTC else "inherit"


def apply_defaults(env, payload: dict | None, headless_default: bool | None = None) -> None:
    """Fill the guards into every leak field the caller left out. Values the caller sent are kept as sent."""
    payload = payload or {}
    fp = payload.get("fingerprint") or {}
    prefs = payload.get("prefs") or {}
    if "webrtc_mode" not in fp and prefs.get("webrtc", "inherit") == "inherit":
        env.fingerprint.webrtc_mode = SAFE_WEBRTC
    if "isolation" not in prefs:
        env.prefs.isolation = PROCESS if sandbox_blocker(env, headless_default) else SANDBOX


def force_safe(
    env, payload: dict | None, before: list[str] | None = None, headless_default: bool | None = None
) -> list[str]:
    """Import paths: put every new, unacknowledged exposure back to its guard, return the names that were changed.

    Used where the incoming config comes from a file (account pack, profile dir) rather than from a person choosing.
    """
    acked = acks_of(payload) | set(env.leak_ack or [])
    changed = []
    for name in exposures(env):
        if name in acked or (before is not None and name in before):
            continue
        if name == WEBRTC_REAL:
            env.fingerprint.webrtc_mode = SAFE_WEBRTC
            if env.prefs.webrtc not in GUARDED_WEBRTC:
                env.prefs.webrtc = "inherit"
            changed.append(name)
        elif not sandbox_blocker(env, headless_default):
            env.prefs.isolation = SANDBOX
            changed.append(name)
    now = exposures(env)
    env.leak_ack = [n for n in EXPOSURES if n in now and n in acked]
    return changed


def check(env, payload: dict | None, before: list[str] | None = None, headless_default: bool | None = None) -> list[str]:
    """Errors for exposures that are new compared with before and neither acknowledged nor unavoidable.

    before is None on create (every exposure is new). On success env.leak_ack keeps the acks still in force.
    """
    now = exposures(env)
    acked = acks_of(payload) | set(env.leak_ack or [])
    forced = unavoidable(env, headless_default)
    errors = [
        "%s. 确认要这样做请在 %s 里带上 %s, 否则%s" % (TEXT[n], ACK_FIELD, n, FIX[n])
        for n in now
        if n not in acked and n not in forced and (before is None or n not in before)
    ]
    if not errors:
        env.leak_ack = [n for n in EXPOSURES if n in now and n in acked]
    return errors


def status(env, headless_default: bool | None = None) -> dict:
    """Shape for env_dict: current exposures, which were acknowledged, which the host forces and why."""
    now = exposures(env)
    forced = unavoidable(env, headless_default)
    return {
        "exposures": now,
        "acked": [n for n in now if n in (env.leak_ack or [])],
        "unavoidable": forced,
        "guarded": not now,
    }
