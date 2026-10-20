"""本机 CDP 请求不受环境代理变量影响 (回环口请求被送去代理会误判 CDP 未就绪)。"""

from __future__ import annotations

import asyncio
import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

from hx_browser import cdp


class _Version(BaseHTTPRequestHandler):
    def do_GET(self):  # noqa: N802
        body = json.dumps({"webSocketDebuggerUrl": "ws://127.0.0.1/x"}).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_a):
        pass


def test_http_json_ignores_env_proxy(monkeypatch):
    srv = HTTPServer(("127.0.0.1", 0), _Version)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    try:
        # 指向一个不存在的代理: 若 http_json 读环境变量, 这里必然连接失败
        for k in ("HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"):
            monkeypatch.setenv(k, "http://127.0.0.1:9")
        for k in ("NO_PROXY", "no_proxy"):
            monkeypatch.delenv(k, raising=False)
        url = "http://127.0.0.1:%d/json/version" % srv.server_address[1]
        got = asyncio.run(cdp.http_json(url, timeout=3))
        assert got["webSocketDebuggerUrl"] == "ws://127.0.0.1/x"
    finally:
        srv.shutdown()
