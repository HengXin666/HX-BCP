"""沙箱隔离的真浏览器端到端 (opt-in): 网络 / 显示 / 字体 / seccomp 四条边界逐条实测。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md

    HXBR_E2E_CHROMIUM=/path/to/browser HXBR_E2E_SANDBOX=1 uv run pytest tests/test_e2e_isolation.py

每条断言都读**进程/页面的实际状态**, 不信启动参数 (仓库验证纪律):
* 网络: 浏览器进程的 net 命名空间 inode 与宿主不同; 网页访问宿主 127.0.0.1 上的服务失败,
  而经 relay 的代理请求成功
* 显示: 沙箱里只看得见本副本的 X socket, 看不到另一个「桌面」X server
* 字体: 白名单外的系统字体在网页里测量不到, 中文字体仍在
* seccomp: 浏览器进程 /proc/<pid>/status 的 Seccomp 为 2 (filter)
"""

from __future__ import annotations

import asyncio
import os
import shutil
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import pytest
from test_e2e_browser import CHROMIUM, PASSWORD, SYNC_GET, USER, _eval, _Upstream

import hxbr_testenv
from hx_browser.config import settings_manager

pytestmark = pytest.mark.skipif(
    not (CHROMIUM and os.environ.get("HXBR_E2E_SANDBOX") and shutil.which("bwrap")),
    reason="需 HXBR_E2E_CHROMIUM + HXBR_E2E_SANDBOX=1 + bwrap",
)
FONT_PROBE = """(() => {
  const c = document.createElement('canvas').getContext('2d');
  const w = f => { c.font = '72px ' + f; return c.measureText('mmmmmmmmmlli 汉字').width; };
  const base = ['monospace', 'serif', 'sans-serif'].map(w);
  const has = n => ['monospace', 'serif', 'sans-serif'].some((b, i) => w("'" + n + "'," + b) !== base[i]);
  return JSON.stringify(Object.fromEntries(%s.map(n => [n, has(n)])));
})()"""
SANDBOX = "sandbox"
BROWSER_NAMES = (b"chromium", b"chrome", b"headless_shell")  # fmt: skip  # browser-gate: allow-legacy 仅按进程名查找 E2E 浏览器
HOST_ONLY = "(()=>{try{const x=new XMLHttpRequest();x.open('GET','http://127.0.0.1:%d/',false);x.send();return 'reached:'+x.status}catch(e){return 'blocked'}})()"


class _Host(BaseHTTPRequestHandler):
    def do_GET(self):  # noqa: N802
        self.send_response(200)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(b"host-secret")

    def log_message(self, *_a):
        pass


def _children(pid: int) -> list[int]:
    out = []
    for p in Path("/proc").iterdir():
        if p.name.isdigit():
            try:
                stat = (p / "stat").read_text()
            except OSError:
                continue
            if int(stat.rsplit(")", 1)[1].split()[1]) == pid:
                out.append(int(p.name))
    return out


def _browser_pid(root: int) -> int:
    todo, seen = [root], []
    while todo:
        pid = todo.pop()
        seen.append(pid)
        todo += _children(pid)
    for pid in seen:
        cmd = Path("/proc/%d/cmdline" % pid).read_bytes().split(b"\0")
        name = cmd[0].rsplit(b"/", 1)[-1]
        if name in BROWSER_NAMES and not any(a.startswith(b"--type=") for a in cmd):
            return pid
    raise AssertionError("找不到沙箱里的浏览器主进程")


def _start(c, body: dict) -> tuple[str, dict]:
    env_id = c.post("/api/v1/environments", json=body).json()["data"]["id"]
    started = c.post("/api/v1/environments/%s/start" % env_id).json()
    assert started["code"] == 0, started
    return env_id, started["data"]["runtime"]


def _setup(c):
    s = settings_manager.load()
    s.chromium_path = CHROMIUM
    s.cdp_base_port = 46000 + (os.getpid() % 400) * 2
    s.proxy_check_before_launch = False
    settings_manager.save(s)


def test_sandbox_network_fonts_seccomp(tmp_path):
    up = _Upstream()
    host = HTTPServer(("127.0.0.1", 0), _Host)
    threading.Thread(target=host.serve_forever, daemon=True).start()
    with hxbr_testenv.client(tmp_path) as c:
        _setup(c)
        body = {
            "name": "iso", "fingerprint": {"headless": True},
            "proxy": {"link": "http://%s:%s@127.0.0.1:%d" % (USER, PASSWORD, up.proxy_port)},
            "prefs": {"isolation": SANDBOX, "font_isolation": "custom",
                      "fonts": ["DejaVu Sans"], "startup_url": "http://hx-e2e.test/"},
        }  # fmt: skip
        env_id, rt = _start(c, body)
        try:
            port = rt["debug_port"]
            assert "hx-e2e-ok" in asyncio.run(_eval(port, SYNC_GET))  # 经 relay 出网
            # 网络: 宿主回环上的服务在沙箱里不存在
            got = asyncio.run(_eval(port, HOST_ONLY % host.server_address[1]))
            assert got == "blocked", got
            pid = _browser_pid(rt["pid"])
            assert os.readlink("/proc/%d/ns/net" % pid) != os.readlink(
                "/proc/self/ns/net"
            )
            status = Path("/proc/%d/status" % pid).read_text()
            assert "Seccomp:\t2" in status, status  # seccomp 过滤器在浏览器上
            # 字体: 白名单外的系统字体量不到, 白名单与中文字体在
            names = [
                "DejaVu Sans",
                "Liberation Serif",
                "Nimbus Sans",
                "Noto Sans CJK SC",
            ]
            seen = asyncio.run(_eval(port, FONT_PROBE % names))
            assert '"DejaVu Sans":true' in seen and '"Noto Sans CJK SC":true' in seen, (
                seen
            )
            assert (
                '"Liberation Serif":false' in seen and '"Nimbus Sans":false' in seen
            ), seen
        finally:
            c.post("/api/v1/environments/%s/stop" % env_id)
            c.delete("/api/v1/environments/%s?purge=true" % env_id)
    host.shutdown()
    up.loop.call_soon_threadsafe(up.loop.stop)


@pytest.mark.skipif(not shutil.which("Xvfb"), reason="需要 Xvfb")
def test_sandbox_sees_only_its_own_display(tmp_path, monkeypatch):
    decoy_n = 99  # 假装是用户桌面的另一个 X server
    decoy = subprocess.Popen(["Xvfb", ":%d" % decoy_n, "-nolisten", "tcp"],
                             stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)  # fmt: skip
    up = _Upstream()
    try:
        with hxbr_testenv.client(tmp_path) as c:
            _setup(c)
            body = {
                "name": "iso-x", "fingerprint": {"headless": False},
                "proxy": {"link": "http://%s:%s@127.0.0.1:%d" % (USER, PASSWORD, up.proxy_port)},
                "prefs": {"isolation": SANDBOX},
            }  # fmt: skip
            env_id, rt = _start(c, body)
            try:
                assert rt["display_source"] == "xvfb", rt
                assert asyncio.run(_eval(rt["debug_port"], "1+1")) == 2
                pid = _browser_pid(rt["pid"])
                visible = sorted(os.listdir("/proc/%d/root/tmp/.X11-unix" % pid))
                assert visible == ["X%s" % rt["display"].lstrip(":")], visible
            finally:
                c.post("/api/v1/environments/%s/stop" % env_id)
                c.delete("/api/v1/environments/%s?purge=true" % env_id)
    finally:
        decoy.terminate()
        up.loop.call_soon_threadsafe(up.loop.stop)


@pytest.fixture
def weston(tmp_path, monkeypatch):
    if not shutil.which("weston"):
        pytest.skip("需要 weston (无头 Wayland compositor)")
    run = tmp_path / "xdg"
    run.mkdir(mode=0o700)
    monkeypatch.setenv("XDG_RUNTIME_DIR", str(run))
    monkeypatch.delenv("WAYLAND_DISPLAY", raising=False)
    proc = subprocess.Popen(
        ["weston", "--backend=headless", "--socket=wayland-7", "--idle-time=0"],
        env=dict(os.environ, XDG_RUNTIME_DIR=str(run)),
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )  # fmt: skip
    for _ in range(50):
        if (run / "wayland-7").is_socket():
            break
        time.sleep(0.1)
    yield run / "wayland-7"
    proc.terminate()


@pytest.mark.parametrize("isolation", ["process", SANDBOX])
def test_native_wayland(tmp_path, weston, isolation):
    up = _Upstream()
    with hxbr_testenv.client(tmp_path) as c:
        _setup(c)
        body = {
            "name": "wl", "fingerprint": {"headless": False},
            "proxy": {"link": "http://%s:%s@127.0.0.1:%d" % (USER, PASSWORD, up.proxy_port)},
            "prefs": {"isolation": isolation, "window_system": "wayland"},
        }  # fmt: skip
        env_id, rt = _start(c, body)
        try:
            assert rt["display_source_detail"] == "wayland:%s" % weston, rt
            assert asyncio.run(_eval(rt["debug_port"], "window.outerWidth")) > 0
            pid = _browser_pid(rt["pid"])
            cmd = Path("/proc/%d/cmdline" % pid).read_bytes()
            assert (
                b"--ozone-platform=wayland" in cmd
                and b"--ozone-platform=x11" not in cmd
            )
            envs = Path("/proc/%d/environ" % pid).read_bytes().split(b"\0")
            assert not any(e.startswith(b"DISPLAY=") for e in envs)  # 不给 X
            if isolation == SANDBOX:
                wl = Path("/proc/%d/root/run/hx-wayland" % pid)
                socks = [p.name for p in wl.iterdir() if p.is_socket()]
                assert socks == ["wayland-0"], socks  # 只有 compositor 这一个 socket
                assert not os.path.exists("/proc/%d/root/tmp/.X11-unix" % pid)
        finally:
            c.post("/api/v1/environments/%s/stop" % env_id)
            c.delete("/api/v1/environments/%s?purge=true" % env_id)
    up.loop.call_soon_threadsafe(up.loop.stop)
