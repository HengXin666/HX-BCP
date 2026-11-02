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


def test_spawn_passes_serve_subcommand():
    """run.py 只是 hxb 的壳; 少了 serve, argparse 会把 127.0.0.1 当成非法子命令 (CI 实测 exit 3)."""
    cmd = rp.spawn_command(8325)
    assert cmd[1:3] == ["run.py", "serve"]
    assert cmd[cmd.index("--host") + 1] == "127.0.0.1"
    assert cmd[cmd.index("--port") + 1] == "8325"


def test_wait_api_gives_up_when_child_already_exited():
    """API 子进程立刻退出时不能空等满 --wait 秒."""
    import subprocess
    import sys
    import time

    proc = subprocess.Popen([sys.executable, "-c", "pass"])
    proc.wait()
    start = time.monotonic()
    assert rp.wait_api("http://127.0.0.1:9", 30, proc) is False
    assert time.monotonic() - start < 5

def test_ci_profile_hides_webrtc():
    """CI profile must use proxy so hide_webrtc is true and STUN cannot leak the exit IP (srflx)."""
    import importlib.util
    from pathlib import Path

    from hx_browser.models import Fingerprint

    path = Path(__file__).resolve().parents[1] / "gate" / "real_profile.py"
    spec = importlib.util.spec_from_file_location("real_profile_webrtc", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    assert mod.FINGERPRINT.get("webrtc_mode") == "proxy"
    fp = Fingerprint(**{**Fingerprint().model_dump(), **mod.FINGERPRINT})
    assert fp.webrtc_mode == "proxy"
