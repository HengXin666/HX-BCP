"""HTTP API — 给人和程序用的只读/控制面。

刻意的取舍: **只读 + 少量控制 (启停/清缓存), 不提供"改请求"的接口**。
理由是改请求会引入"API 层与服务层判定不一致"的风险 (本仓库栽过判据散落两处的坑),
而抓包回放需要的是**读**, 不是写。

鉴权: 本地 loopback 绑定 + 可选 Bearer token (部署到非本机时必须给)。
"""

# Agent Note: .agents/notes/implemented/bug-fix/2026-09-26-hxcp-stats-flow-count-source.md
from __future__ import annotations

import base64
import json
import os
import threading
import time
from typing import Any

try:
    from fastapi import FastAPI, HTTPException, Request
    from fastapi.responses import JSONResponse
    from fastapi.staticfiles import StaticFiles
    import uvicorn
except ImportError:  # pragma: no cover
    FastAPI = None  # type: ignore

from .store import Store, SCHEMA_VERSION
from . import flows as flows_mod
from . import policy

WEB_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "web")

STATE: dict[str, Any] = {"engine": None, "store": None, "started_at": time.time(),
                         "events": [], "log": []}
MAX_EVENTS = 2000


def read_runtime(store: Store) -> dict | None:
    """读引擎落盘的运行态 (跨进程观测的唯一来源)。

    引擎与 API 不在同一进程, 所以"引擎在不在跑"必须从磁盘读, 不能从内存变量读 ——
    后者恒为 None, 会让面板上的观测面永远是空的。
    """
    try:
        p = os.path.join(str(store.root), "runtime.json")
        with open(p, encoding="utf-8") as f:
            d = json.load(f)
        # 太旧视为已停 (引擎崩溃/强杀时不会走 stop(), 落盘文件会滞留)
        if time.time() - float(d.get("updated_at", 0)) > 120:
            d["state"] = "stale"
        return d
    except Exception:
        return None


def build_app(store: Store, token: str = "") -> "FastAPI":
    if FastAPI is None:
        raise RuntimeError("需要 API 依赖: uv sync --extra api")
    app = FastAPI(title="HX-Capture-Proxy", version="0.1.0")

    def gate(req: Request) -> None:
        if not token:
            return
        got = (req.headers.get("authorization") or "").replace("Bearer ", "").strip()
        if got != token:
            raise HTTPException(status_code=401, detail="bad token")

    @app.get("/health")
    def health() -> dict:
        rt = read_runtime(store)
        return {"ok": True, "uptime_s": int(time.time() - STATE["started_at"]),
                "engine": bool(rt and rt.get("state") == "running"),
                "engine_state": (rt or {}).get("state", "absent"),
                "schema_version": SCHEMA_VERSION}

    @app.get("/stats")
    def stats(req: Request) -> dict:
        gate(req)
        eng = STATE["engine"]
        rt = read_runtime(store)
        # 同进程引擎优先; 否则读落盘的运行态 (跨进程场景的常态)。
        # **engine 段必须保持扁平形态** —— 消费方 (rotator/hxcp.py view()) 直接读
        # `engine.bytes_served`, 嵌套一层就是破坏契约。flow 统计作为**兄弟字段**加,
        # 不动既有字段。
        live = eng.stats.to_dict() if eng else (rt or {}).get("stats")
        # flow 段的三级来源, 判据是"**谁此刻是权威**":
        #   · 同进程引擎在跑 -> 引擎计数器 (未落盘的那些也已计数, 最准也最便宜);
        #   · 跨进程引擎在跑 -> runtime.json 的 flows 段 (引擎刚刚写的, 新鲜);
        #   · 引擎不在跑   -> **扫磁盘**。这一步不能省: runtime.json 是死进程留下的快照,
        #     引擎一停它的 written 就永远冻在最后一次, 面板会一直显示过期的 (或不存在的) 计数 ——
        #     实测就踩到: 库里 50418 条记录, 面板显示"记录 0"。
        rec = eng._flow_stats() if eng else None
        if not rec and (rt or {}).get("state") == "running":
            rec = (rt or {}).get("flows")
        if not rec:
            rec = flows_mod.flow_stats(store.root / "flows")
        fl = rec
        # 引擎不在跑时 runtime.json 里的 flow 段没有 forensic 子段 (或压根是磁盘统计),
        # 于是取证池的规模在这里补上 —— 前端的状态栏靠它显示"完整体存了多少/上限多少"。
        # 池统计是现算的 (glob 一次目录), 与"谁在跑"无关, 这才是跨进程也准的来源。
        if isinstance(fl, dict) and "forensic" not in fl:
            fl = dict(fl)
            fl["forensic"] = flows_mod.open_forensic(store.root).stats()
        return {"engine": live, "engine_state": (rt or {}).get("state", "absent"),
                "store": store.stats(), "flows": fl,
                "events": len(STATE["events"])}

    @app.get("/entries")
    def entries(req: Request, limit: int = 100, q: str = "") -> dict:
        gate(req)
        sql = "SELECT url,method,status,size,hit_count,source,stored_at,fresh_until,etag FROM entries"
        args: list = []
        if q:
            sql += " WHERE url LIKE ?"
            args.append("%" + q + "%")
        sql += " ORDER BY hit_count DESC, size DESC LIMIT ?"
        args.append(int(limit))
        rows = store._conn.execute(sql, args).fetchall()
        return {"count": len(rows), "entries": [dict(r) for r in rows]}

    @app.get("/lookup")
    def lookup(req: Request, url: str) -> dict:
        gate(req)
        e = store.lookup(url)
        if e is None:
            return {"hit": False, "url": url}
        return {"hit": True, "url": url, "fresh": e.is_fresh(), "size": e.size,
                "source": e.source, "hit_count": e.hit_count, "key": e.key,
                "etag": e.etag, "stored_at": e.stored_at, "fresh_until": e.fresh_until}

    @app.get("/body")
    def body(req: Request, url: str) -> dict:
        gate(req)
        e = store.lookup(url)
        if e is None:
            raise HTTPException(404, "no entry")
        b = store.get_blob(e.body_hash)
        if b is None:
            raise HTTPException(410, "blob missing")
        return {"url": url, "size": len(b), "sha256": e.body_hash,
                "preview": b[:512].decode("utf-8", "replace")}

    @app.get("/flows")
    def flows(req: Request, limit: int = 200, host: str = "", status: int = 0,
              q: str = "", kind: str = "", days: float = 0, offset: int = 0) -> dict:
        """查请求级 flow 历史 (请求头/响应头/时序/判定)。

        与 /entries 的区别: entries 是**缓存索引** (只含被缓存成功的静态资源),
        flows 是**全量观测** (含被 policy 拒缓存的 API/登录响应)。要摸接口、找签名参数,
        看 flows; 要看省了多少字节, 看 entries。
        """
        gate(req)
        it = flows_mod.iter_flows(
            store.root / "flows", days=(days or None), host=host,
            status=(int(status) if status else None), q=q, kind=kind,
            limit=max(1, min(int(limit), 5000)), offset=max(0, int(offset)))
        rows = list(it)
        # count = 本页条数 (与 /entries 同形); 前端要用它判"还有没有下一页"。
        return {"count": len(rows), "flows": rows, "offset": max(0, int(offset))}

    @app.get("/flow/body")
    def flow_body(req: Request, sha256: str = "", offset: int = 0, limit: int = 65536) -> dict:
        """按 body_ref.sha256 取回**完整请求体** (取证档)。

        与 /flows 的关系: /flows 返回的 post_data 是 8K 摘要, 这里是它的全量来源。
        分段返回 (offset/limit) 是刻意的 —— 前端拿它当"文本预览 + 续读", 而不是一次吞 16MB。
        """
        gate(req)
        fs = flows_mod.open_forensic(store.root)
        b = fs.get(sha256)
        if b is None:
            # 不区分"没这个人"与"池里没有": 两种都是"取不到", 而写清原因比区分更有用。
            raise HTTPException(404, "payload 不在池里 (未开启取证档 / 超上限 / 已轮转删除)")
        off = max(0, int(offset))
        lim = max(1, min(int(limit), 4 * 1024 * 1024))
        chunk = b[off:off + lim]
        return {"sha256": sha256, "size": len(b), "offset": off,
                "returned": len(chunk), "eof": off + len(chunk) >= len(b),
                "base64": base64.b64encode(chunk).decode(),
                "text": chunk.decode("utf-8", "replace")}

    @app.post("/purge")
    def purge(req: Request, flows_keep_days: float = -1) -> dict:
        gate(req)
        out = {"purged": store.purge_expired()}
        # flow 保留期独立于缓存 TTL: 缓存过期是"该回源了", flow 过期是"不需要这段历史了"。
        # -1 = 不动 flow(默认), 0 = 只留当天。
        if float(flows_keep_days) >= 0:
            out["flows_purged"] = flows_mod.purge(store.root / "flows", flows_keep_days)
            # 取证池与 flow 同一保留期: 池里的体只在记录引用它时才有意义,
            # 记录删了体就是孤儿 —— 留着白占磁盘, 且让人误以为还能取回。
            out["payloads_purged"] = flows_mod.open_forensic(store.root).purge(flows_keep_days)
        return out

    @app.post("/engine/stop")
    def engine_stop(req: Request) -> dict:
        gate(req)
        eng = STATE["engine"]
        if eng:
            eng.stop()
        return {"stopped": bool(eng)}

    # ── 静态前端 ──────────────────────────────────────────
    # 挂载在最后: / 是个 catch-all, 先注册它会把上面的路由全吞掉。
    # 单文件原生 JS (web/index.html), 不引入 npm 构建链 —— 理由见该文件里的注释。
    # **页面本身不设闸** (它不含任何数据), 数据端点各自 gate(): 浏览器发不出 Bearer,
    # 若连 HTML 都要 token, 用户只能看到一片白屏且不知道为什么。
    if os.path.isdir(WEB_DIR):
        app.mount("/", StaticFiles(directory=WEB_DIR, html=True), name="web")

    return app


def record_event(ev: dict) -> None:
    evs = STATE["events"]
    evs.append(ev)
    if len(evs) > MAX_EVENTS:
        del evs[: len(evs) - MAX_EVENTS]


def serve(cache_dir: str, host: str = "127.0.0.1", port: int = 8898,
          token: str = "") -> int:
    store = Store(cache_dir)
    STATE["store"] = store
    app = build_app(store, token)
    uvicorn.run(app, host=host, port=port, log_level="warning")
    return 0


def main(argv: list[str] | None = None) -> int:
    """`python -m hxcp.api` 的入口。

    没有这个入口时 `python -m` 会**静默什么都不做** (模块被 import 后立即返回 0),
    进程立刻退出且没有任何输出 —— 极难排查。任何"用 -m 启动"的模块都必须有它。
    """
    import argparse
    ap = argparse.ArgumentParser(prog="hxcp.api", description="HX-Capture-Proxy HTTP API")
    ap.add_argument("--cache-dir", default=os.path.expanduser("~/loli_data/hxcp"))
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8898)
    ap.add_argument("--token", default=os.environ.get("HXCP_API_TOKEN", ""))
    a = ap.parse_args(argv)
    return serve(a.cache_dir, a.host, a.port, a.token)


if __name__ == "__main__":
    import sys as _sys
    _sys.exit(main())
