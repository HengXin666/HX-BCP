"""mitmproxy addon: the shared cache and flow log behind the standard proxy entry.

Any client that speaks HTTP or SOCKS5 proxy (every browser, curl, agents) can use it.
TLS hosts are tunnelled byte for byte unless proxy_rules says decrypt, so the
browser keeps its own handshake by default. Plain HTTP and decrypted hosts go
through the same store and policy as the CDP engine.
"""

# Agent Note: .agents/notes/implemented/feature/2026-11-22-hxcp-universal-proxy.md
from __future__ import annotations

import asyncio
import base64
import time
from typing import Any, Callable
from urllib.parse import unquote

from mitmproxy import http
from mitmproxy.proxy import mode_specs

from . import policy
from .flows import FlowLog
from .proxy_rules import DECRYPT, LiveRules
from .store import Store

_DROP = ("content-length", "content-encoding", "transfer-encoding", "connection")
HOSTS_MAX = 2000


def _basic(user: str, pw: str) -> str:
    return "Basic " + base64.b64encode((user + ":" + pw).encode()).decode()


class Listener:
    """One listening port: who uses it, and the upstream proxy credentials if any."""

    def __init__(self, name: str, spec: str, auth: str = "") -> None:
        self.name = name
        self.spec = spec
        self.auth = auth


def parse_upstream(url: str) -> tuple[str, str]:
    """http://user:pw@host:port -> (spec without credentials, Proxy-Authorization value)."""
    url = (url or "").strip()
    if not url:
        return "", ""
    scheme = url.split("://", 1)[0].lower() if "://" in url else "http"
    if scheme not in ("http", "https"):
        raise ValueError("upstream must be http or https proxy, got " + scheme)
    rest = url.split("://", 1)[1] if "://" in url else url
    auth = ""
    if "@" in rest:
        cred, rest = rest.rsplit("@", 1)
        user, _, pw = cred.partition(":")
        auth = _basic(unquote(user), unquote(pw))
    return scheme + "://" + rest.rstrip("/"), auth


class CacheAddon:
    def __init__(self, store: Store, rules: policy.Rules, live: LiveRules, flows: FlowLog,
                 listeners: list[Listener], *, log: Callable[[str], None] | None = None) -> None:
        self.store = store
        self.rules = rules
        self.live = live
        self.flows = flows
        self.by_spec = {ln.spec: ln for ln in listeners}
        self._log = log or (lambda m: None)
        self.stats: dict[str, Any] = {k: 0 for k in (
            "requests", "hits", "misses", "passed", "filled", "skipped", "bytes_served",
            "bytes_network", "errors", "tunnels", "decrypted", "revalidated")}
        self.stats["reasons"] = {}
        self.hosts: dict[str, dict[str, Any]] = {}

    # helpers
    def _note(self, reason: str) -> None:
        r = self.stats["reasons"]
        r[reason] = r.get(reason, 0) + 1

    def _listener(self, client: Any) -> Listener | None:
        mode = getattr(client, "proxy_mode", None)
        return self.by_spec.get(getattr(mode, "full_spec", "")) if mode is not None else None

    def _client_name(self, client: Any) -> str:
        ln = self._listener(client)
        return ln.name if ln else "proxy"

    def _host(self, host: str, mode: str, reason: str, client: str) -> None:
        if not host:
            return
        h = self.hosts.get(host)
        if h is None:
            if len(self.hosts) >= HOSTS_MAX:
                oldest = min(self.hosts, key=lambda k: self.hosts[k]["last"])
                self.hosts.pop(oldest, None)
            h = self.hosts[host] = {"host": host, "conns": 0, "mode": mode, "reason": reason,
                                    "clients": [], "last": 0.0}
        h["conns"] += 1
        h["mode"], h["reason"], h["last"] = mode, reason, time.time()
        if client not in h["clients"] and len(h["clients"]) < 20:
            h["clients"].append(client)

    def _emit(self, rec: dict) -> None:
        rec.setdefault("at", time.time())
        rec.setdefault("via", "proxy")
        self.flows.write(rec)

    # mitmproxy hooks
    def tls_clienthello(self, data: Any) -> None:
        """Decide per TLS connection: tunnel keeps the client handshake, decrypt opens it."""
        ctx = data.context
        sni = data.client_hello.sni or ""
        addr = getattr(ctx.server, "address", None) or ("", 0)
        host = sni or str(addr[0] or "")
        mode, reason = self.live.decide(host)
        client = self._client_name(ctx.client)
        self._host(host, mode, reason, client)
        if mode == DECRYPT:
            self.stats["decrypted"] += 1
            return
        data.ignore_connection = True
        self.stats["tunnels"] += 1
        self._emit({"kind": "tunnel", "verdict": "tunnel:" + reason, "method": "CONNECT",
                    "url": "https://" + host + ":" + str(addr[1] or 443), "status": 0,
                    "size": 0, "client": client, "tls": "passthrough",
                    "alpn": [p.decode("latin-1") for p in (data.client_hello.alpn_protocols or [])]})

    def http_connect_upstream(self, flow: http.HTTPFlow) -> None:
        ln = self._listener(flow.client_conn)
        if ln and ln.auth:
            flow.request.headers["Proxy-Authorization"] = ln.auth

    def requestheaders(self, flow: http.HTTPFlow) -> None:
        ln = self._listener(flow.client_conn)
        mode = getattr(flow.client_conn, "proxy_mode", None)
        if ln and ln.auth and isinstance(mode, mode_specs.UpstreamMode) and flow.request.scheme == "http":
            flow.request.headers["Proxy-Authorization"] = ln.auth

    def request(self, flow: http.HTTPFlow) -> None:
        """Answer from the shared store when possible, never touching the network."""
        req = flow.request
        flow.metadata["hxcp_t0"] = time.time()
        self.stats["requests"] += 1
        url = req.pretty_url
        why = policy.request_cacheable(req.method, dict(req.headers))
        if not why and not self.rules.may_handle(url):
            why = "host_filtered"
        if why:
            flow.metadata["hxcp_skip"] = why
            self.stats["passed"] += 1
            return
        entry = self.store.lookup(url, "GET")
        if entry is not None and entry.is_fresh():
            body = self.store.get_blob(entry.body_hash)
            if body is not None:
                self.store.record_hit(entry.key)
                self.stats["hits"] += 1
                self.stats["bytes_served"] += len(body)
                self._note("hit")
                flow.metadata["hxcp_hit"] = entry.key
                flow.response = self._make(entry, body, "HIT-SEED" if entry.source == "seed" else "HIT")
                return
        self.stats["misses"] += 1
        self._note("miss")
        if entry is not None and entry.etag and not _has_conditional(req.headers):
            name = "if-modified-since" if "GMT" in entry.etag else "if-none-match"
            req.headers[name] = entry.etag
            flow.metadata["hxcp_reval"] = entry.key

    async def response(self, flow: http.HTTPFlow) -> None:
        if flow.metadata.get("hxcp_hit"):
            self._emit(self._record(flow, "hit", "hit", key=flow.metadata["hxcp_hit"]))
            return
        resp = flow.response
        if resp is None:
            return
        url = flow.request.pretty_url
        key = flow.metadata.get("hxcp_reval")
        if key and resp.status_code == 304:
            entry = self.store.get(key)
            body = self.store.get_blob(entry.body_hash) if entry else None
            if entry is not None and body is not None:
                dec = policy.decide(url, entry.status, {h[0]: h[1] for h in entry.headers},
                                    size=len(body), max_bytes=self.rules.max_bytes)
                await asyncio.to_thread(self.store.put, url, "GET", entry.status, entry.headers,
                                        body, ttl=dec.ttl or policy.TTL_CONDITIONAL,
                                        etag=entry.etag, source=entry.source, tags=["revalidated"])
                self.stats["revalidated"] += 1
                self.stats["bytes_served"] += len(body)
                flow.response = self._make(entry, body, "REVALIDATED")
                self._emit(self._record(flow, "hit", "revalidated", key=key))
                return
        raw = resp.raw_content or b""
        self.stats["bytes_network"] += len(raw)
        skip = flow.metadata.get("hxcp_skip")
        if skip:
            self._emit(self._record(flow, "flow", "pass:" + skip))
            return
        if resp.stream:
            self._skip(flow, "too_large")
            return
        hdrs = {k: v for k, v in resp.headers.items(multi=True)}
        dec = policy.decide(url, resp.status_code, hdrs, size=len(raw), max_bytes=self.rules.max_bytes)
        if not dec:
            self._skip(flow, dec.reason or "skip")
            return
        body = resp.get_content(strict=False) or b""
        if not body:
            self._skip(flow, "empty_body")
            return
        stored = [[k, v] for k, v in resp.headers.items(multi=True) if k.lower() not in _DROP]
        e = await asyncio.to_thread(self.store.put, url, "GET", resp.status_code, stored, body,
                                    ttl=dec.ttl, etag=dec.etag, source="net", tags=["proxy"])
        if e is None:
            self._skip(flow, "too_large")
            return
        self.stats["filled"] += 1
        self._note("fill:" + dec.reason)
        self._emit(self._record(flow, "flow", "fill:" + dec.reason))

    def error(self, flow: http.HTTPFlow) -> None:
        self.stats["errors"] += 1
        msg = str(getattr(flow.error, "msg", "") or "error")[:160]
        self._emit(self._record(flow, "flow", "error:" + msg))

    # building blocks
    def _skip(self, flow: http.HTTPFlow, reason: str) -> None:
        self.stats["skipped"] += 1
        self._note(reason)
        self._emit(self._record(flow, "flow", "skip:" + reason))

    def _make(self, entry: Any, body: bytes, tag: str) -> http.Response:
        headers = [(str(h[0]).encode(), str(h[1]).encode()) for h in entry.headers
                   if str(h[0]).lower() not in _DROP]
        headers.append((b"x-hx-cache", tag.encode()))
        return http.Response.make(entry.status or 200, body, headers)

    def _record(self, flow: http.HTTPFlow, kind: str, verdict: str, key: str = "") -> dict:
        req, resp = flow.request, flow.response
        t0 = float(flow.metadata.get("hxcp_t0") or req.timestamp_start or time.time())
        rec: dict[str, Any] = {
            "kind": kind, "verdict": verdict, "url": req.pretty_url, "method": req.method,
            "status": int(resp.status_code) if resp else 0,
            "size": len(resp.raw_content or b"") if resp and not resp.stream else 0,
            "type": policy.content_type(dict(resp.headers)) if resp else "",
            "req_headers": dict(req.headers.items(multi=True)),
            "resp_headers": dict(resp.headers.items(multi=True)) if resp else {},
            "client": self._client_name(flow.client_conn),
            "tls": "decrypted" if req.scheme == "https" else "plain",
            "http_version": req.http_version, "ms": int((time.time() - t0) * 1000), "at": t0,
        }
        if req.method not in ("GET", "HEAD") and req.raw_content:
            rec["post_data"] = req.get_text(strict=False) or ""
        if key:
            rec["key"] = key
        return rec

    def snapshot(self) -> dict[str, Any]:
        hosts = sorted(self.hosts.values(), key=lambda h: -h["last"])[:300]
        return {"stats": dict(self.stats, reasons=dict(self.stats["reasons"])),
                "hosts": hosts}


def _has_conditional(headers: Any) -> bool:
    return any(k.lower() in ("if-none-match", "if-modified-since") for k in headers.keys())
