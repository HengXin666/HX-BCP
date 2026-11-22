"""HTTP routes for access, TLS rules, CA, PAC, hosts and HAR export (facts live in access.py)."""

# Agent Note: .agents/notes/implemented/feature/2026-11-22-hxcp-universal-proxy.md
from __future__ import annotations

import time
from typing import Callable

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse, PlainTextResponse, Response

from . import flows as flows_mod
from . import proxy_rules, runtime
from .access import access_info, pac_text, proxy_instances, to_har
from .proxy import DEFAULT_PORT, ca_cert_path
from .store import Store


def build_router(store: Store, gate: Callable[[Request], None]) -> APIRouter:
    r = APIRouter()
    root = store.root

    def api_base(req: Request) -> str:
        pre = req.headers.get("x-forwarded-prefix") or ""
        return str(req.base_url).rstrip("/") + pre

    @r.get("/access")
    def access(req: Request) -> dict:
        gate(req)
        return access_info(root, api_base(req))

    @r.get("/runtimes")
    def runtimes(req: Request) -> dict:
        gate(req)
        return {"instances": runtime.instances(root)}

    @r.get("/ca.crt")
    def ca() -> Response:
        p = ca_cert_path(root)
        if not p.exists():
            raise HTTPException(404, "CA 还没生成: 先启动一次 hxcp proxy")
        return Response(p.read_bytes(), media_type="application/x-x509-ca-cert",
                        headers={"content-disposition": "attachment; filename=hxcp-ca.pem"})

    @r.get("/proxy.pac")
    def pac(host: str = "", port: int = 0) -> PlainTextResponse:
        info = access_info(root)
        ln = next((x for x in info["listeners"] if x.get("mode") != "socks5"), {})
        return PlainTextResponse(pac_text(host or ln.get("host", "127.0.0.1"),
                                          int(port or ln.get("port") or DEFAULT_PORT), info["rules"]),
                                 media_type="application/x-ns-proxy-autoconfig")

    @r.get("/rules")
    def rules_get(req: Request) -> dict:
        gate(req)
        return proxy_rules.load(root)

    @r.put("/rules")
    async def rules_put(req: Request) -> dict:
        gate(req)
        return proxy_rules.save(root, await req.json())

    @r.post("/rules/host")
    async def rules_host(req: Request) -> dict:
        gate(req)
        body = await req.json()
        try:
            return proxy_rules.set_host(root, str(body.get("host") or ""), str(body.get("mode") or ""))
        except ValueError as e:
            raise HTTPException(400, str(e))

    @r.get("/hosts")
    def hosts(req: Request) -> dict:
        """Per host view: TLS mode seen by the proxy plus what the shared store holds."""
        gate(req)
        rules = proxy_rules.load(root)
        seen: dict[str, dict] = {}
        for d in proxy_instances(root):
            for h in d.get("hosts") or []:
                cur = seen.setdefault(h["host"], {"host": h["host"], "conns": 0, "clients": [], "last": 0})
                cur["conns"] += int(h.get("conns") or 0)
                cur["last"] = max(cur["last"], float(h.get("last") or 0))
                cur["clients"] = sorted(set(cur["clients"]) | set(h.get("clients") or []))
        for s in store.host_summary(500):
            seen.setdefault(s["host"], {"host": s["host"], "conns": 0, "clients": [], "last": 0}).update(s)
        rows = []
        for h in seen.values():
            mode, why = proxy_rules.decide(rules, h["host"])
            rows.append(dict({"entries": 0, "bytes": 0, "hits": 0, "saved": 0}, **h, mode=mode, reason=why))
        rows.sort(key=lambda x: (-(x.get("saved") or 0), -(x.get("conns") or 0)))
        return {"count": len(rows), "hosts": rows[:1000]}

    @r.get("/clients")
    def clients(req: Request) -> dict:
        gate(req)
        out: dict[str, dict] = {}
        for d in runtime.instances(root):
            name = str(d.get("client") or d.get("instance"))
            c = out.setdefault(name, {"client": name, "kinds": [], "state": d.get("state"),
                                      "updated_at": d.get("updated_at")})
            if d.get("kind") and d["kind"] not in c["kinds"]:
                c["kinds"].append(d["kind"])
            if d.get("state") == "running":
                c["state"] = "running"
            for ln in d.get("listeners") or []:
                lc = out.setdefault(str(ln.get("name")), {"client": str(ln.get("name")), "kinds": [],
                                                          "state": d.get("state"), "updated_at": d.get("updated_at")})
                kind = "proxy:" + str(ln.get("port"))
                if kind not in lc["kinds"]:
                    lc["kinds"].append(kind)
        return {"clients": sorted(out.values(), key=lambda c: c["client"])}

    @r.get("/flows.har")
    def har(req: Request, host: str = "", status: int = 0, q: str = "", client: str = "",
            days: float = 1, limit: int = 2000) -> JSONResponse:
        gate(req)
        rows = list(flows_mod.iter_flows(root / "flows", days=(days or None), host=host,
                                         status=(int(status) if status else None), q=q,
                                         client=client, limit=max(1, min(int(limit), 20000))))
        rows.reverse()
        return JSONResponse(to_har(rows), headers={
            "content-disposition": "attachment; filename=hxcp-" + time.strftime("%Y%m%d-%H%M%S") + ".har"})

    @r.delete("/entry")
    def entry_delete(req: Request, url: str) -> dict:
        gate(req)
        return {"deleted": store.delete(url)}

    return r
