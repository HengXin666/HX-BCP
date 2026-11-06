"""merge-safarier: Safari 档经 guard._fp_json_for (扩展与 CDP 共用的唯一 FP 字典) 的自洽性.

PR #18 原本把 family / device_scale_factor 写在 fingerprint.build_fingerprint_extension 里,
#19 后字典单一真源迁到 guard._fp_json_for, 合并时移植到这里; 同时补 UA / WebGL 的族感知兜底.
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from hx_browser import guard, mac_models  # noqa: E402
from hx_browser.models import Environment, Fingerprint  # noqa: E402

SAFARI = "safari"  # browser-gate: allow-legacy 内核 id


def _env(**fp) -> Environment:
    env = Environment(name="t", fingerprint=Fingerprint(**fp))
    env.id = "env_test"
    return env


def test_safari_env_without_ua_gets_safari_ua_not_chrome():
    d = guard._fp_json_for(_env(browser=SAFARI, kernel=SAFARI, os="macOS"), False, None, "/usr/bin/chromium")
    assert "Chrome/" not in d["user_agent"]
    assert d["user_agent"] == mac_models.safari_user_agent()
    assert d["family"] == "webkit"


def test_safari_env_without_renderer_gets_apple_gpu():
    d = guard._fp_json_for(_env(browser=SAFARI, kernel=SAFARI, os="macOS"), False)
    assert (d["webgl_vendor"], d["webgl_renderer"]) == (mac_models.WEBGL_VENDOR, mac_models.WEBGL_RENDERER)


def test_device_scale_factor_reaches_pixel_ratio():
    d = guard._fp_json_for(_env(browser=SAFARI, kernel=SAFARI, os="macOS", device_scale_factor=2.0), False)
    assert d["pixel_ratio"] == 2.0


def test_blink_env_unchanged():
    d = guard._fp_json_for(_env(browser="sun", os="Windows"), False)
    assert d["family"] == "blink"
    assert d["webgl_renderer"] != mac_models.WEBGL_RENDERER
