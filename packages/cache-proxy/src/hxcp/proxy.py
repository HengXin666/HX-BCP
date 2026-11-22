"""`hxcp proxy`: the standard HTTP / SOCKS5 proxy entry, usable by any browser or agent.

One process can serve many browsers. Each --client gets its own port and its
own upstream egress proxy, every client shares the same cache store, and every
flow is tagged with the client name.
"""

# Agent Note: .agents/notes/implemented/feature/2026-11-22-hxcp-universal-proxy.md
from __future__ import annotations

import asyncio
import os
import signal
import time
from pathlib import Path
from typing import Any

from . import policy, runtime
from .flows import FlowLog, ForensicStore, forensic_path
from .proxy_rules import LiveRules
from .store import Store

DEFAULT_PORT = 8899


def ca_dir(cache_dir: str | Path) -> Path:
    return Path(cache_dir).expanduser() / "ca"


def ca_cert_path(cache_dir: str | Path) -> Path:
    return ca_dir(cache_dir) / "mitmproxy-ca-cert.pem"


def parse_client(text: str) -> tuple[str, int, str]:
    """name=port or name=port,upstream_url"""
    if "=" not in text:
        raise ValueError("--client wants name=port[,upstream], got " + text)
    name, rest = text.split("=", 1)
    port_s, _, up = rest.partition(",")
    return runtime.safe_name(name), int(port_s), up.strip()


def build_modes(a: Any) -> tuple[list[str], list[Any]]:
    from .proxy_addon import Listener, parse_upstream
    modes: list[str] = []
    listeners: list[Listener] = []

    def add(name: str, port: int, upstream: str) -> None:
        spec, auth = parse_upstream(upstream)
        mode = ("upstream:" + spec if spec else "regular") + "@" + str(port)
        modes.append(mode)
        listeners.append(Listener(name, mode, auth))

    add(a.name, int(a.port), a.upstream)
    if a.socks_port:
        if a.upstream:
            raise ValueError("socks5 listener can not chain an upstream, drop --socks-port or --upstream")
        mode = "socks5@" + str(int(a.socks_port))
        modes.append(mode)
        listeners.append(Listener(a.name + "-socks", mode))
    for c in a.client or []:
        name, port, up = parse_client(c)
        add(name, port, up)
    return modes, listeners


async def _serve(a: Any) -> int:
    from mitmproxy.tools.dump import DumpMaster
    from mitmproxy import options

    from .proxy_addon import CacheAddon
    store = Store(a.cache_dir, max_entries_bytes=int(a.max_bytes))
    root = store.root
    modes, listeners = build_modes(a)
    rules = policy.Rules(
        allow_hosts=frozenset(x.strip() for x in (a.allow_host or "").split(",") if x.strip()),
        deny_hosts=frozenset(x.strip() for x in (a.deny_host or "").split(",") if x.strip()),
        max_bytes=int(a.max_bytes))
    overrides = {"decrypt": [x for x in (a.decrypt or "").split(",") if x.strip()],
                 "decrypt_all": bool(a.decrypt_all)}
    live = LiveRules(root, overrides if (overrides["decrypt"] or a.decrypt_all) else None)
    forensic = ForensicStore(forensic_path(root), enabled=bool(a.forensic))
    flows = FlowLog(root / "flows", enabled=not a.no_flows,
                    forensic=forensic if a.forensic else None)
    addon = CacheAddon(store, rules, live, flows, listeners, log=print)
    ca_dir(root).mkdir(parents=True, exist_ok=True)

    master = DumpMaster(options.Options(), with_termlog=bool(a.verbose), with_dumper=False)
    master.options.update(listen_host=a.listen, mode=modes, confdir=str(ca_dir(root)),
                          connection_strategy="lazy", ssl_insecure=bool(a.insecure),
                          stream_large_bodies=str(int(a.max_bytes)))
    master.addons.add(addon)
    instance = "proxy-" + a.name

    def publish(state: str) -> None:
        snap = addon.snapshot()
        runtime.publish(root, instance, {
            "state": state, "kind": "proxy", "client": a.name, "stats": snap["stats"],
            "hosts": snap["hosts"], "flows": flows.stats(), "listen": a.listen,
            "listeners": [{"name": ln.name, "mode": ln.spec.split("@")[0],
                           "port": int(ln.spec.rsplit("@", 1)[1]),
                           "upstream": ln.spec.split("@")[0].replace("upstream:", "")
                           if ln.spec.startswith("upstream:") else "",
                           "upstream_auth": bool(ln.auth)} for ln in listeners],
            "ca": str(ca_cert_path(root)), "rules": live.current(), "updated_at": time.time()})

    async def ticker() -> None:
        while True:
            publish("running")
            await asyncio.sleep(2.0)

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, master.shutdown)
        except (NotImplementedError, RuntimeError):
            pass
    print("[hxcp] proxy " + ", ".join(m + " on " + (a.listen or "*") for m in modes)
          + "  cache=" + str(root), flush=True)
    print("[hxcp] 已挂载 proxy", flush=True)
    tick = asyncio.create_task(ticker())
    try:
        await master.run()
    finally:
        tick.cancel()
        flows.close()
        publish("stopped")
        store.close()
    return 0


def cmd_proxy(a: Any) -> int:
    try:
        import mitmproxy  # noqa: F401
    except ImportError:
        print("[hxcp] 需要代理依赖: uv sync --extra proxy")
        return 2
    try:
        return asyncio.run(_serve(a))
    except ValueError as e:
        print("[hxcp] " + str(e))
        return 2
    except KeyboardInterrupt:
        return 0


def add_parser(sub: Any) -> None:
    p = sub.add_parser("proxy", help="标准 HTTP/SOCKS5 代理入口, 任意浏览器与 Agent 可用")
    p.add_argument("--listen", default=os.environ.get("HXCP_PROXY_LISTEN", "127.0.0.1"))
    p.add_argument("--port", type=int, default=DEFAULT_PORT)
    p.add_argument("--socks-port", type=int, default=0, help="另开一个 SOCKS5 入口 (0=不开)")
    p.add_argument("--name", default="proxy", help="默认入口的客户端名, 记进每条 flow")
    p.add_argument("--upstream", default="", help="默认入口的上游出口代理 http://user:pw@host:port")
    p.add_argument("--client", action="append", default=[],
                   help="每个浏览器一个入口: name=port[,upstream], 可重复")
    p.add_argument("--decrypt", default="", help="逗号分隔 host 后缀, 解密并缓存 (改变 TLS 指纹)")
    p.add_argument("--decrypt-all", action="store_true", help="全部解密 (仅调试用)")
    p.add_argument("--insecure", action="store_true", help="解密时不校验上游证书")
    p.add_argument("--no-flows", action="store_true", help="不落 flow 记录")
    p.add_argument("--forensic", action="store_true", help="请求体整份进取证池")
    p.add_argument("--verbose", action="store_true")
    p.set_defaults(fn=cmd_proxy)
