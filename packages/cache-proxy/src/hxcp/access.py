"""Connection facts and exports shared by the HTTP API and MCP: how a client plugs in, PAC, HAR.

No web framework imports here so MCP and the CLI can use it without the api extra.
"""

# Agent Note: .agents/notes/implemented/feature/2026-11-22-hxcp-universal-proxy.md
from __future__ import annotations

import time
from typing import Any
from urllib.parse import urlparse

from . import proxy_rules, runtime
from .proxy import DEFAULT_PORT, ca_cert_path


def proxy_instances(root: Any) -> list[dict]:
    return [d for d in runtime.instances(root) if d.get("kind") == "proxy"]


def access_info(root: Any, api_base: str = "") -> dict[str, Any]:
    """Facts plus ready to paste snippets for every kind of client."""
    live = [d for d in proxy_instances(root) if d.get("state") == "running"]
    listeners: list[dict] = []
    for d in live:
        host = d.get("listen") or "127.0.0.1"
        host = "127.0.0.1" if host in ("", "0.0.0.0", "::") else host
        for ln in d.get("listeners") or []:
            listeners.append(dict(ln, host=host, instance=d.get("instance")))
    http_ln = next((x for x in listeners if x.get("mode") != "socks5"), None)
    host = (http_ln or {}).get("host", "127.0.0.1")
    port = int((http_ln or {}).get("port") or DEFAULT_PORT)
    url = "http://" + host + ":" + str(port)
    rules = proxy_rules.load(root)
    decrypting = bool(rules["decrypt"] or rules["decrypt_all"])
    ca = ca_cert_path(root)
    base = api_base.rstrip("/")
    snippets = {
        "chromium": "--proxy-server=" + url,
        "firefox": "network.proxy.type=1, network.proxy.http=" + host + ", network.proxy.http_port="
                   + str(port) + ", network.proxy.ssl=" + host + ", network.proxy.ssl_port=" + str(port),
        "playwright": "browser_type.launch(proxy={\"server\": \"" + url + "\"})",
        "puppeteer": "puppeteer.launch({args: [\"--proxy-server=" + url + "\"]})",
        "curl": "curl -x " + url + (" --cacert " + str(ca) if decrypting else "") + " https://example.com",
        "env": "export HTTP_PROXY=" + url + " HTTPS_PROXY=" + url,
        "pac": base + "/proxy.pac",
        "system": "macOS: networksetup -setwebproxy Wi-Fi " + host + " " + str(port)
                  + " ; networksetup -setsecurewebproxy Wi-Fi " + host + " " + str(port),
        "mcp": "HXCP_CACHE=" + str(root) + " uv run python -m hxcp.mcp_server",
        "start": "uv run hxcp --cache-dir " + str(root) + " proxy --port " + str(port),
    }
    return {"running": bool(live), "proxy_url": url, "listeners": listeners,
            "decrypting": decrypting, "rules": rules,
            "ca": {"exists": ca.exists(), "path": str(ca), "download": base + "/ca.crt"},
            "api": base or "/", "snippets": snippets,
            "notes": {
                "tunnel": "默认隧道: 浏览器自己握手, TLS 指纹不变, 只缓存明文 http",
                "decrypt": "解密的 host 由代理与上游握手, 上游看到的是代理的 TLS 指纹, 浏览器需信任本地 CA",
                "cdp": "Chromium 系可另挂 CDP 档 (hxcp run), 缓存命中不出网也不改握手",
            }}


def pac_text(host: str, port: int, rules: dict) -> str:
    direct = ["localhost", "127.0.0.1"]
    lines = ["function FindProxyForURL(url, host) {"]
    for d in direct:
        lines.append("  if (host == \"" + d + "\") return \"DIRECT\";")
    lines.append("  return \"PROXY " + host + ":" + str(port) + "; DIRECT\";")
    lines.append("}")
    return "\n".join(lines) + "\n"


def to_har(rows: list[dict]) -> dict[str, Any]:
    """Flow records to HAR 1.2, enough for DevTools, Charles or Fiddler to open."""
    entries = []
    for r in rows:
        if r.get("kind") == "tunnel":
            continue
        u = str(r.get("url") or "")
        q = urlparse(u).query
        req: dict[str, Any] = {
            "method": r.get("method") or "GET", "url": u, "httpVersion": r.get("http_version") or "HTTP/1.1",
            "headers": [{"name": k, "value": str(v)} for k, v in (r.get("req_headers") or {}).items()],
            "queryString": [{"name": p.split("=", 1)[0], "value": p.split("=", 1)[1] if "=" in p else ""}
                            for p in q.split("&") if p],
            "cookies": [], "headersSize": -1, "bodySize": len(r.get("post_data") or "")}
        if r.get("post_data"):
            req["postData"] = {"mimeType": "", "text": r["post_data"]}
        rh = r.get("resp_headers") or {}
        entries.append({
            "startedDateTime": time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(float(r.get("at") or 0))) + "Z",
            "time": int(r.get("ms") or 0), "request": req,
            "response": {"status": int(r.get("status") or 0), "statusText": "",
                         "httpVersion": r.get("http_version") or "HTTP/1.1",
                         "headers": [{"name": k, "value": str(v)} for k, v in rh.items()],
                         "cookies": [], "content": {"size": int(r.get("size") or 0),
                                                    "mimeType": r.get("type") or ""},
                         "redirectURL": "", "headersSize": -1, "bodySize": int(r.get("size") or 0)},
            "cache": {}, "timings": {"send": 0, "wait": int(r.get("ms") or 0), "receive": 0},
            "comment": str(r.get("verdict") or "") + " client=" + str(r.get("client") or "")})
    return {"log": {"version": "1.2", "creator": {"name": "hxcp", "version": "0.3"},
                    "pages": [], "entries": entries}}
