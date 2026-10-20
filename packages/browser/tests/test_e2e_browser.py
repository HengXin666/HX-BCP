"""真浏览器端到端 (opt-in): 起 headless Chromium, 经本地 relay -> 需认证的上游代理 访问页面。

Agent Note: .agents/notes/implemented/feature/2026-10-16-hxbrowser-profile-ops-nodecloak.md

默认跳过。运行方式 (只用 headless, 用后即清, 符合仓库「禁止本机桌面起浏览器」):

    HXBR_E2E_CHROMIUM=/path/to/chrome-or-headless_shell uv run pytest tests/test_e2e_browser.py
    # 另设 HXBR_E2E_SANDBOX=1 (且 PATH 里有 bwrap) 则同一用例在 bubblewrap 沙箱里再跑一遍

断言的都是**采集到的实际值** (仓库验证纪律: 传了参数 != 生效了):
页面真的经代理取回、上游真的收到注入的凭据、命令行里没有密码、
navigator.language 真的是配置值、Preferences 真的写入、停止后 relay 端口真的关闭。
"""

from __future__ import annotations

import asyncio
import base64
import os
import shutil
import socket
import threading
import time

import pytest

import hxbr_testenv
from hx_browser import cdp
from hx_browser.config import settings_manager
from hx_browser.runtime import proc_cmdline

CHROMIUM = os.environ.get("HXBR_E2E_CHROMIUM", "")
pytestmark = pytest.mark.skipif(
    not CHROMIUM, reason="设置 HXBR_E2E_CHROMIUM 才跑真浏览器用例"
)
USER, PASSWORD = "e2e-user", "e2e-Secret-42"
SYNC_GET = "(()=>{const x=new XMLHttpRequest();x.open('GET','http://hx-e2e.test/x',false);x.send();return x.responseText})()"
PAGE = b"<html><head><title>hx-e2e-ok</title></head><body>proxied</body></html>"


class _Upstream:
    """独立线程里的假上游: 一个需认证的 HTTP 代理 + 一个目标网站。"""

    def __init__(self):
        self.seen: list[bytes] = []
        self.loop = asyncio.new_event_loop()
        self.ready = threading.Event()
        threading.Thread(target=self._run, daemon=True).start()
        self.ready.wait(10)

    def _run(self):
        asyncio.set_event_loop(self.loop)
        self.loop.run_until_complete(self._start())
        self.ready.set()
        self.loop.run_forever()

    async def _start(self):
        token = b"Basic " + base64.b64encode(("%s:%s" % (USER, PASSWORD)).encode())

        async def site(r, w):
            await r.readuntil(b"\r\n\r\n")
            w.write(
                b"HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nAccess-Control-Allow-Origin: *\r\nContent-Length: %d\r\nConnection: close\r\n\r\n"
                % len(PAGE)
                + PAGE
            )
            await w.drain()
            w.close()

        async def proxy(r, w):
            head = await r.readuntil(b"\r\n\r\n")
            self.seen.append(head)
            if token not in head:
                w.write(
                    b"HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n"
                )
                await w.drain()
                w.close()
                return
            ur, uw = await asyncio.open_connection(
                "127.0.0.1", self.site_port
            )  # 所有请求都落到假站点
            uw.write(head)
            await uw.drain()
            while chunk := await ur.read(65536):
                w.write(chunk)
            await w.drain()
            w.close()

        s1 = await asyncio.start_server(site, "127.0.0.1", 0)
        s2 = await asyncio.start_server(proxy, "127.0.0.1", 0)
        self.site_port = s1.sockets[0].getsockname()[1]
        self.proxy_port = s2.sockets[0].getsockname()[1]


def _port_open(port: int) -> bool:
    with socket.socket() as s:
        s.settimeout(0.5)
        return s.connect_ex(("127.0.0.1", port)) == 0


async def _eval(port: int, expr: str, timeout: float = 20.0):
    deadline = time.time() + timeout
    while True:
        try:
            return await cdp.evaluate(port, expr, timeout=10)
        except Exception:  # noqa: BLE001
            if time.time() > deadline:
                raise
            await asyncio.sleep(0.5)


@pytest.mark.parametrize("isolation", ["process", "sandbox"])
def test_real_browser_through_relay(tmp_path, isolation):
    if isolation == "sandbox" and not (
        os.environ.get("HXBR_E2E_SANDBOX") and shutil.which("bwrap")
    ):
        pytest.skip("沙箱用例需 HXBR_E2E_SANDBOX=1 且 PATH 中有 bwrap")
    up = _Upstream()
    with hxbr_testenv.client(tmp_path) as c:
        s = settings_manager.load()
        s.chromium_path = CHROMIUM
        s.cdp_base_port = 45000 + (os.getpid() % 500) * 2
        s.proxy_check_before_launch = False  # 假上游不通外网; 出口检测另有用例
        settings_manager.save(s)
        body = {
            "name": "e2e", "fingerprint": {"headless": True, "language": "de-DE,de"},
            "proxy": {"link": "http://%s:%s@127.0.0.1:%d" % (USER, PASSWORD, up.proxy_port)},
            "prefs": {"language_mode": "custom", "languages": ["ja-JP", "ja"], "webrtc": "proxy",
                      "startup_url": "http://hx-e2e.test/", "isolation": isolation},
        }  # fmt: skip
        env_id = c.post("/api/v1/environments", json=body).json()["data"]["id"]
        started = c.post("/api/v1/environments/%s/start" % env_id).json()
        assert started["code"] == 0, started
        rt = started["data"]["runtime"]
        try:
            assert rt["relay_port"] and rt["isolation"] == isolation
            cmd = proc_cmdline(rt["pid"])
            assert (
                PASSWORD not in cmd
                and "--proxy-server=http://127.0.0.1:%d" % rt["relay_port"] in cmd
            )
            port = rt["debug_port"]
            title = asyncio.run(_eval(port, SYNC_GET))
            assert "hx-e2e-ok" in title  # 页面真的经 relay -> 上游取回
            lang = asyncio.run(_eval(port, "navigator.language"))
            sent = [h for h in up.seen if b"hx-e2e.test/x" in h][-1].lower()
            # 完整 Chrome 读 Preferences 的 intl.accept_languages; 精简版 headless_shell
            # 不读 Preferences 但遵守 --lang -> 两条路径至少一条须落地成 ja
            assert lang.startswith("ja") or b"accept-language: ja" in sent, (lang, sent)
            assert any(
                b"Proxy-Authorization: Basic" in h for h in up.seen
            )  # 凭据由 relay 注入
            prefs = (
                tmp_path / "data" / "profiles" / env_id / "Default" / "Preferences"
            ).read_text()
            assert '"ja-JP,ja"' in prefs or "ja-JP,ja" in prefs
            # 只改名称: 不触发待重启; 改代理偏好: 触发
            c.put("/api/v1/environments/" + env_id, json={"name": "renamed"})
            assert (
                c.get("/api/v1/environments/" + env_id).json()["data"][
                    "pending_restart"
                ]
                is False
            )
            c.put("/api/v1/environments/%s/prefs" % env_id, json={"block_images": True})
            assert (
                c.get("/api/v1/environments/" + env_id).json()["data"][
                    "pending_restart"
                ]
                is True
            )
        finally:
            c.post("/api/v1/environments/%s/stop" % env_id)
        assert not _port_open(rt["relay_port"])  # 停止后本地代理入口关闭
        c.delete("/api/v1/environments/%s?purge=true" % env_id)
    up.loop.call_soon_threadsafe(up.loop.stop)
