"""偏好互斥 / 生效值推导 / Chromium 合并写 / Firefox 托管块 / 时钟判据 / 内核 / 沙箱 argv。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md
"""

from __future__ import annotations

import json

import pytest

import hxbr_testenv  # noqa: F401 — 设 sys.path
from hx_browser import chromium_prefs, firefox_prefs, kernels, netcheck
from hx_browser.models import Environment, Fingerprint, Settings
from hx_browser.prefs import ProfilePrefs
from hx_browser.profile_rules import (
    effective_languages,
    effective_timezone,
    validate_prefs,
)

TOKYO, BERLIN = "Asia/Tokyo", "Europe/Berlin"
BLOCK, CUSTOM, FLOWER, ON, UNKNOWN = "block", "custom", "flower", "on", "unknown"
BLOCKED = 2  # Chromium/Firefox 权限值: 2 = 阻止
FF = next(k.id for k in kernels.KERNELS if k.family == kernels.GECKO)  # Firefox 内核 id


def _env(kernel: str = "", **prefs) -> Environment:
    return Environment(
        id="env_x", fingerprint=Fingerprint(kernel=kernel), prefs=ProfilePrefs(**prefs)
    )


def test_legacy_env_defaults_are_inherit():
    """老 environments.json 没有 prefs 键: 反序列化后全部 inherit, 生效值 = 既有字段。"""
    env = Environment(
        **{
            "id": "env_old",
            "fingerprint": {
                "browser": FLOWER,
                "language": "de-DE,de",
                "timezone": BERLIN,
            },
        }
    )
    assert env.prefs == ProfilePrefs()
    assert effective_languages(env) == "de-DE,de"
    assert effective_timezone(env) == BERLIN
    assert kernels.family_of(env) == kernels.GECKO
    assert validate_prefs(env) == []


def test_rfp_and_ip_language_are_mutually_exclusive():
    errs = validate_prefs(
        _env(FF, firefox_resist_fingerprinting=True, language_mode="ip")
    )
    assert any("互斥" in e for e in errs)
    assert (
        validate_prefs(
            _env(
                FF,
                firefox_resist_fingerprinting=True,
                language_mode=CUSTOM,
                languages=["en-US"],
            )
        )
        == []
    )


def test_timezone_rules():
    assert effective_timezone(_env(FF, firefox_resist_fingerprinting=True)) == "UTC"
    assert effective_timezone(_env(timezone_mode="system")) == ""  # 跟随电脑系统
    assert effective_timezone(_env(timezone_mode="ip"), TOKYO) == TOKYO
    assert validate_prefs(_env(timezone_mode=CUSTOM, timezone="Mars/Base")) != []
    assert (
        validate_prefs(_env(timezone_mode=CUSTOM, timezone="America/Los_Angeles")) == []
    )


def test_chromium_merge_preserves_unrelated_keys(tmp_path):
    prefs_file = tmp_path / chromium_prefs.PREFS_REL
    prefs_file.parent.mkdir(parents=True)
    prefs_file.write_text(
        json.dumps(
            {
                "keep": {"me": 1},
                "profile": {
                    "content_settings": {
                        "exceptions": {
                            "geolocation": {
                                "https://a.example:443,*": {"setting": 1},
                                "https://b.example:443,*": {"setting": 2},
                            }
                        }
                    }
                },
            }
        )
    )
    p = ProfilePrefs(geolocation=BLOCK, camera=BLOCK, webrtc="proxy", do_not_track=ON,
                     startup_url="https://example.com", dns_privacy=True, block_images=True)  # fmt: skip
    assert chromium_prefs.write(tmp_path, p, "ja-JP,ja") == []
    data = json.loads(prefs_file.read_text())
    assert data["keep"] == {"me": 1}
    values = data["profile"]["default_content_setting_values"]
    assert (
        values["geolocation"] == BLOCKED
        and values["media_stream_camera"] == BLOCKED
        and values["notifications"] == 3
    )
    assert values["images"] == BLOCKED
    assert data["webrtc"]["ip_handling_policy"] == "disable_non_proxied_udp"
    assert data["enable_do_not_track"] is True
    assert data["intl"]["accept_languages"] == "ja-JP,ja"
    assert data["session"] == {
        "restore_on_startup": 4,
        "startup_urls": ["https://example.com"],
    }
    geo = data["profile"]["content_settings"]["exceptions"]["geolocation"]
    assert list(geo) == ["https://b.example:443,*"]  # 禁用定位时删掉逐站点的「允许」
    state = json.loads((tmp_path / "Local State").read_text())
    assert state["dns_over_https"] == {"mode": "secure", "templates": p.doh_url}
    # 关掉 DNS 隐私: 只撤销自己写过的 secure
    chromium_prefs.write(tmp_path, ProfilePrefs(), "")
    assert (
        json.loads((tmp_path / "Local State").read_text())["dns_over_https"]["mode"]
        == "automatic"
    )


def test_chromium_refuses_to_overwrite_corrupt_prefs(tmp_path):
    f = tmp_path / chromium_prefs.PREFS_REL
    f.parent.mkdir(parents=True)
    f.write_text("{not json")
    warnings = chromium_prefs.write(tmp_path, ProfilePrefs(), "")
    assert warnings and f.read_text() == "{not json"


def test_firefox_managed_block_roundtrip(tmp_path):
    (tmp_path / "user.js").write_text(
        'user_pref("privacy.resistFingerprinting", false);\n'
    )
    p = ProfilePrefs(firefox_font_restriction=True, firefox_resist_fingerprinting=True, firefox_disable_webgl=True,
                     gpc=ON, do_not_track=ON, webrtc="disable", geolocation=BLOCK, dns_privacy=True)  # fmt: skip
    values = firefox_prefs.plan(
        p,
        "en-US,en",
        {"network.proxy.type": 0},
        fonts=["Noto Sans CJK SC", "WenQuanYi Zen Hei"],
    )
    firefox_prefs.write(tmp_path, values)
    firefox_prefs.write(tmp_path, values)  # 幂等: 不会叠出两个托管块
    text = (tmp_path / "user.js").read_text()
    assert text.count(firefox_prefs.MANAGED_BEGIN) == 1
    assert text.index("false") < text.index(
        firefox_prefs.MANAGED_BEGIN
    )  # 托管块在后 -> 覆盖
    got = firefox_prefs.read_managed(tmp_path)
    assert got["privacy.resistFingerprinting"] is True and got["webgl.disabled"] is True
    assert "Noto Sans CJK SC" in got["font.system.whitelist"]  # 保留中文字体
    assert got["media.peerconnection.enabled"] is False
    assert got["geo.enabled"] is False and got["permissions.default.geo"] == BLOCKED
    assert got["privacy.globalprivacycontrol.enabled"] is True
    assert got["network.trr.mode"] == 3 and got["network.proxy.type"] == 0
    assert "geo.wifi.uri" not in got and not any(
        "geo.provider" in k for k in got
    )  # 不覆盖坐标


@pytest.mark.parametrize(
    ("server_offset", "status"),
    [(0, "healthy"), (60, "healthy"), (-300, "warning"), (400, "warning")],
)
def test_clock_assess(server_offset, status):
    now = 1_791_600_000.0
    body = "fl=1\nh=www.cloudflare.com\nts=%.3f\n" % (now + server_offset)
    assert netcheck.assess_clock(body, now, now + 0.2, 0.2)["status"] == status


def test_clock_unreliable_reference_is_unknown():
    now = 1_791_600_000.0
    assert netcheck.assess_clock("no timestamp", now, now, 0.1)["status"] == UNKNOWN
    assert (
        netcheck.assess_clock("ts=%f" % now, now, now + 30, 30)["status"] == UNKNOWN
    )  # 太慢
    assert (
        netcheck.assess_clock("ts=%f" % now, now, now + 9, 0.2)["status"] == UNKNOWN
    )  # 期间被调过表


def test_region_parsing():
    r = netcheck.region_from(
        {
            "success": True,
            "ip": "1.2.3.4",
            "country_code": "JP",
            "timezone": {"id": TOKYO},
        }
    )
    assert r["languages"].startswith("ja-JP") and r["timezone"] == TOKYO
    r2 = netcheck.region_from({"ip": "5.6.7.8", "country": "de", "timezone": ""})
    assert r2["timezone"] == BERLIN


def test_kernel_resolution_keeps_legacy_paths(tmp_path):
    exe = tmp_path / "fake-browser"
    exe.write_text("")
    s = Settings(
        chromium_path="/legacy/c",
        firefox_path="/legacy/f",
        kernel_paths={"edge": str(exe)},
    )
    assert kernels.resolve_executable(_env(), s) == "/legacy/c"
    legacy_ff = Environment(fingerprint=Fingerprint(browser=FLOWER))
    assert kernels.resolve_executable(legacy_ff, s) == "/legacy/f"
    assert kernels.resolve_executable(_env("edge"), s) == str(exe)
    assert kernels.legacy_browser_for("librewolf") == FLOWER
