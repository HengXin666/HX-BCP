"""WebKit 档 (safari 内核) 纯函数测试: 不起进程, 不碰网络.

Agent Note: .agents/notes/implemented/feature/2026-10-29-hxbrowser-webkit-safari-kernel.md

覆盖: kernels.family_of / kernels.legacy_browser_for / kernels.resolve_executable,
webkit.resolve_executable / build_argv / child_env / ensure_not_headless.
环境与设置用 SimpleNamespace 伪造, 只给被测函数实际读取的字段.
"""

from __future__ import annotations

import sys
from pathlib import Path
from types import SimpleNamespace

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from hx_browser import kernels, webkit  # noqa: E402

SAFARI = "safari"  # browser-gate: allow-legacy 内核 id
FIREFOX = "firefox"  # browser-gate: allow-legacy 内核 id
CHROME = "chrome"  # browser-gate: allow-legacy 内核 id


def _proxy(kind="no_proxy", host="", port=0):
    return SimpleNamespace(type=kind, host=host, port=port, username="", password="")


def _env(kernel=SAFARI, browser="sun", headless=False, ua="", proxy=None, urls=()):
    fp = SimpleNamespace(
        kernel=kernel, browser=browser, headless=headless, user_agent=ua
    )
    return SimpleNamespace(
        fingerprint=fp,
        proxy=proxy if proxy is not None else _proxy(),
        launch=SimpleNamespace(urls=list(urls)),
    )


def _settings(**paths):
    return SimpleNamespace(kernel_paths=dict(paths), chromium_path="", firefox_path="")


# ---- kernels.family_of ----


def test_family_of_safari_env_is_webkit():
    assert kernels.family_of(_env(kernel=SAFARI)) == kernels.WEBKIT


def test_family_of_kernel_id_is_case_insensitive():
    assert kernels.family_of(_env(kernel=" Safari ")) == kernels.WEBKIT


def test_family_of_legacy_env_unchanged():
    assert kernels.family_of(_env(kernel="", browser="flower")) == kernels.GECKO
    assert kernels.family_of(_env(kernel="", browser="sun")) == kernels.BLINK


def test_speaks_cdp_false_only_for_webkit():
    assert kernels.speaks_cdp(_env(kernel=SAFARI)) is False
    assert kernels.speaks_cdp(_env(kernel=CHROME)) is True
    assert kernels.speaks_cdp(_env(kernel=FIREFOX)) is True


# ---- resolve_executable ----


def test_kernels_resolve_executable_prefers_kernel_paths(tmp_path, monkeypatch):
    exe = tmp_path / "MiniBrowser"
    exe.write_text("")
    monkeypatch.setattr(kernels, "detect", lambda _kid: "/should/not/be/used")
    got = kernels.resolve_executable(_env(), _settings(safari=str(exe)))
    assert got == str(exe)


def test_webkit_resolve_executable_prefers_kernel_paths(tmp_path, monkeypatch):
    exe = tmp_path / "MiniBrowser"
    exe.write_text("")
    monkeypatch.setattr(kernels, "detect", lambda _kid: "/should/not/be/used")
    assert webkit.resolve_executable(_settings(safari=str(exe))) == str(exe)


def test_webkit_resolve_executable_falls_back_to_detect(tmp_path, monkeypatch):
    seen = []
    monkeypatch.setattr(kernels, "detect", lambda kid: seen.append(kid) or "/x/Mini")
    missing = tmp_path / "nope"
    assert webkit.resolve_executable(_settings(safari=str(missing))) == "/x/Mini"
    assert seen == [SAFARI]


def test_webkit_resolve_executable_raises_when_nothing_found(monkeypatch):
    monkeypatch.setattr(kernels, "detect", lambda _kid: "")
    with pytest.raises(webkit.WebKitLaunchError):
        webkit.resolve_executable(_settings())
    with pytest.raises(webkit.WebKitLaunchError):
        webkit.resolve_executable(None)


# ---- build_argv ----


def test_build_argv_without_proxy_has_no_proxy_flag(tmp_path):
    argv = webkit.build_argv(_env(), tmp_path / "p", 0)
    assert not any(a.startswith("--proxy") for a in argv)
    assert argv[0] == "--profile-dir=%s" % (tmp_path / "p")
    assert argv[1] == "--user-agent=%s" % webkit.DEFAULT_SAFARI_UA


def test_build_argv_ignores_port_when_no_proxy(tmp_path):
    argv = webkit.build_argv(_env(), tmp_path, 12345)
    assert not any(a.startswith("--proxy") for a in argv)


def test_build_argv_with_proxy_points_to_local_relay(tmp_path):
    env = _env(proxy=_proxy("socks5", "1.2.3.4", 1080), ua="UA-X", urls=["https://a"])
    argv = webkit.build_argv(env, tmp_path, 40123)
    assert "--proxy=http://127.0.0.1:40123" in argv
    assert "--user-agent=UA-X" in argv
    assert argv[-1] == "https://a"
    assert not any("1.2.3.4" in a for a in argv)


def test_build_argv_proxy_without_relay_port_refuses(tmp_path):
    env = _env(proxy=_proxy("http", "1.2.3.4", 8080))
    with pytest.raises(webkit.WebKitLaunchError):
        webkit.build_argv(env, tmp_path, 0)


# ---- child_env ----


def test_child_env_pins_x11_and_drops_wayland():
    base = {"WAYLAND_DISPLAY": "wayland-0", "GDK_BACKEND": "wayland", "HOME": "/h"}
    plan = SimpleNamespace(env={"DISPLAY": ":99"}, unset=("XAUTHORITY",))
    out = webkit.child_env(base, plan)
    assert out["GDK_BACKEND"] == "x11"
    assert "WAYLAND_DISPLAY" not in out
    assert out["DISPLAY"] == ":99" and out["HOME"] == "/h"
    assert base["WAYLAND_DISPLAY"] == "wayland-0"  # 不修改入参


def test_child_env_plan_cannot_reintroduce_wayland():
    plan = SimpleNamespace(
        env={"WAYLAND_DISPLAY": "w-1", "GDK_BACKEND": "wayland"}, unset=()
    )
    out = webkit.child_env({}, plan)
    assert out["GDK_BACKEND"] == "x11"
    assert "WAYLAND_DISPLAY" not in out


def test_child_env_tolerates_none_plan():
    out = webkit.child_env(None, None)
    assert out == {"GDK_BACKEND": "x11"}


# ---- headless ----


def test_headless_raises():
    with pytest.raises(webkit.WebKitLaunchError, match="headless"):
        webkit.ensure_not_headless(_env(headless=True))


def test_build_argv_headless_raises(tmp_path):
    with pytest.raises(webkit.WebKitLaunchError):
        webkit.build_argv(_env(headless=True), tmp_path, 0)


def test_not_headless_passes():
    webkit.ensure_not_headless(_env(headless=False))


# ---- legacy_browser_for ----


@pytest.mark.parametrize(
    "kernel_id, expected",
    [
        (SAFARI, "safari"),
        (FIREFOX, "flower"),
        ("librewolf", "flower"),
        ("cloak", "sun"),
        (CHROME, "sun"),
        ("", "sun"),
        ("unknown-kernel", "sun"),
    ],
)
def test_legacy_browser_for_three_families(kernel_id, expected):
    assert kernels.legacy_browser_for(kernel_id) == expected
