"""UA 与内核真实握手一致性: 判据分支, 启动前拦截, tls_mode 归一. 全部进程内, 不出网.

Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-ua-kernel-coherence.md
"""

from __future__ import annotations

import asyncio
from types import SimpleNamespace

import pytest

import hxbr_testenv  # noqa: F401
from hx_browser import launch_hooks, relay, tlsprobe
from hx_browser.models import Environment, Fingerprint

CHROME_UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36"
FIREFOX_UA = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
SAFARI_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"


def fake_kernel(tmp_path, kernel_id, version_line):
    exe = tmp_path / ("fake-" + kernel_id + "-" + str(abs(hash(version_line))))
    exe.write_text("#!/bin/sh\necho '%s'\n" % version_line)
    exe.chmod(0o755)
    return SimpleNamespace(kernel_paths={kernel_id: str(exe)}, chromium_path="", firefox_path="")


def env_with(kernel_id, ua, env_id="env_uak"):
    return Environment(id=env_id, name=env_id, fingerprint=Fingerprint(kernel=kernel_id, user_agent=ua))


def test_criterion_branches():
    assert tlsprobe.ua_conflicts("", "blink", 146) == []
    assert tlsprobe.ua_conflicts(CHROME_UA % 146, "blink", 146) == []
    assert tlsprobe.ua_conflicts(CHROME_UA % 146, "blink", 0) == []
    assert tlsprobe.ua_conflicts(FIREFOX_UA, "gecko", 140) == []
    bad = tlsprobe.ua_conflicts(CHROME_UA % 150, "blink", 146)
    assert len(bad) == 1 and "150" in bad[0] and "146" in bad[0]
    assert "gecko" in tlsprobe.ua_conflicts(CHROME_UA % 146, "gecko", 0)[0]
    assert tlsprobe.ua_conflicts(SAFARI_UA, "blink", 146)


def test_prepare_refuses_stale_major_before_relay(tmp_path):
    settings = fake_kernel(tmp_path, "chromium", "Chromium 146.0.7680.31")
    env = env_with("chromium", CHROME_UA % 150)
    env.proxy.type, env.proxy.host, env.proxy.port = "http", "127.0.0.1", 9
    with pytest.raises(ValueError) as err:
        asyncio.run(launch_hooks.prepare(env, tmp_path / "p", settings=settings))
    assert "150" in str(err.value) and "146" in str(err.value)
    assert env.id not in relay.RELAYS


def test_matching_major_passes(tmp_path):
    settings = fake_kernel(tmp_path, "chromium", "Chromium 146.0.7680.31")
    assert launch_hooks.ua_kernel_conflicts(env_with("chromium", CHROME_UA % 146), settings) == []
    assert launch_hooks.ua_kernel_conflicts(env_with("chromium", ""), settings) == []


def test_gecko_kernel_with_chrome_ua_is_refused(tmp_path):
    settings = fake_kernel(tmp_path, "firefox", "Mozilla Firefox 140.3.0")
    out = launch_hooks.ua_kernel_conflicts(env_with("firefox", CHROME_UA % 146), settings)
    assert out and "gecko" in out[0]
    assert launch_hooks.ua_kernel_conflicts(env_with("firefox", FIREFOX_UA), settings) == []


def test_unreadable_version_only_checks_family(tmp_path):
    settings = fake_kernel(tmp_path, "chromium", "no version here")
    assert launch_hooks.ua_kernel_conflicts(env_with("chromium", CHROME_UA % 150), settings) == []
    assert launch_hooks.ua_kernel_conflicts(env_with("chromium", FIREFOX_UA), settings)


def test_tls_mode_is_always_native():
    assert Fingerprint(tls_mode="chrome").tls_mode == "none"
    assert Fingerprint().tls_mode == "none"
