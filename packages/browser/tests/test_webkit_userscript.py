"""WebKit 档指纹注入 (--user-script-at-start) 纯函数测试: 不起 MiniBrowser 进程, 不碰网络.

覆盖: webkit.user_script_path / webkit.build_argv 的 user script 参数.
J3a 参数存在 + 文件非空 + 排在第一个 URL 之前
J3b user script 与 build_fingerprint_extension 生成的 content.js 逐字节相等 (同源)
J3c 注入准备失败 (生成抛异常 / 文件为空) 抛 WebKitLaunchError, 不静默起无指纹窗口
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from hx_browser import fingerprint, webkit  # noqa: E402
from hx_browser.models import Environment, Fingerprint, Launch  # noqa: E402

SAFARI = "safari"  # browser-gate: allow-legacy 内核 id
FLAG = "--user-script-at-start="
URLS = ["https://example.com/", "https://example.org/"]


def _env(urls=URLS) -> Environment:
    fp = Fingerprint(**fingerprint.gen_random_fingerprint(SAFARI, "macOS"))
    fp.kernel = SAFARI
    fp.headless = False
    return Environment(id="env-webkit-test", fingerprint=fp, launch=Launch(urls=list(urls)))


def _argv(env, profile_dir):
    return webkit.build_argv(env, profile_dir, 0, webkit.user_script_path(env, profile_dir))


# ---- J3a ----


def test_argv_has_user_script_before_first_url(tmp_path):
    argv = _argv(_env(), tmp_path)
    idx = [i for i, a in enumerate(argv) if a.startswith(FLAG)]
    assert len(idx) == 1
    path = Path(argv[idx[0]][len(FLAG):])
    assert path.is_file() and path.stat().st_size > 0
    assert idx[0] < argv.index(URLS[0])


def test_user_script_lives_in_profile_dir(tmp_path):
    path = Path(webkit.user_script_path(_env(), tmp_path))
    assert path.parent == tmp_path
    assert path.name == webkit.USER_SCRIPT_NAME


def test_no_user_script_keeps_old_argv(tmp_path):
    env = _env()
    argv = webkit.build_argv(env, tmp_path, 0)
    assert not any(a.startswith(FLAG) for a in argv)
    assert argv[-len(URLS):] == URLS


def test_user_script_flag_still_before_urls_with_proxy(tmp_path, monkeypatch):
    monkeypatch.setattr(webkit, "proxy_url", lambda _env, _port: "http://127.0.0.1:9")
    argv = _argv(_env(), tmp_path)
    i_flag = next(i for i, a in enumerate(argv) if a.startswith(FLAG))
    assert argv.index("--proxy=http://127.0.0.1:9") < i_flag < argv.index(URLS[0])


# ---- J3b ----


def test_user_script_bytes_equal_extension_content_js(tmp_path):
    env = _env()
    path = Path(webkit.user_script_path(env, tmp_path))
    ext_dir = fingerprint.build_fingerprint_extension(env, tmp_path, False)
    assert path.read_bytes() == (Path(ext_dir) / "content.js").read_bytes()


def test_user_script_carries_env_fingerprint(tmp_path):
    env = _env()
    text = Path(webkit.user_script_path(env, tmp_path)).read_text("utf-8")
    assert env.fingerprint.user_agent in text
    assert "__hx_fp_injected__" in text


# ---- J3c ----


def test_extension_failure_raises(tmp_path, monkeypatch):
    def boom(*_a, **_k):
        raise OSError("disk full")

    monkeypatch.setattr(fingerprint, "build_fingerprint_extension", boom)
    with pytest.raises(webkit.WebKitLaunchError):
        webkit.user_script_path(_env(), tmp_path)


def test_empty_content_js_raises(tmp_path, monkeypatch):
    def empty(_env, profile_dir, _dark=False):
        d = Path(profile_dir) / "fingerprint-extension"
        d.mkdir(parents=True, exist_ok=True)
        (d / "content.js").write_bytes(b"")
        return d

    monkeypatch.setattr(fingerprint, "build_fingerprint_extension", empty)
    with pytest.raises(webkit.WebKitLaunchError):
        webkit.user_script_path(_env(), tmp_path)


def test_missing_content_js_raises(tmp_path, monkeypatch):
    monkeypatch.setattr(
        fingerprint, "build_fingerprint_extension", lambda *_a, **_k: tmp_path / "nope"
    )
    with pytest.raises(webkit.WebKitLaunchError):
        webkit.user_script_path(_env(), tmp_path)
