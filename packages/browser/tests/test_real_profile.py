"""gate/real_profile.py 里不依赖浏览器的部分。"""

import importlib.util
from pathlib import Path

_PATH = Path(__file__).resolve().parents[1] / "gate" / "real_profile.py"
_SPEC = importlib.util.spec_from_file_location("real_profile", _PATH)
rp = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(rp)


def test_ua_follows_chrome_major_and_drops_headless():
    ua = rp.chrome_ua("Google Chrome 156.0.7311.2 ")
    assert "Chrome/156.0.0.0" in ua
    assert "Headless" not in ua


def test_unknown_version_gives_no_ua():
    assert rp.chrome_ua("") == ""
    assert rp.chrome_ua("not a version") == ""


def test_language_is_managed_not_inherited():
    assert rp.PREFS["language_mode"] == "custom"
    assert rp.PREFS["languages"][0] == "en-US"
    assert rp.PREFS["font_isolation"] == "strict"
