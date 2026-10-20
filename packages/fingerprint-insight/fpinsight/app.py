"""fp-insight 服务端: 探针入站 -> 事件中枢 -> dashboard 实时流 + 历史查询。

路由分三面:
- /ws/fp    探针专属入站 (页内 WebSocket), 只收不发
- /ws/ui    dashboard 实时流 (广播扇出)
- /api/*    控制面 + 证据查询 (含按 API 聚合的画像与变量纠察)
"""
from __future__ import annotations

import asyncio
import json
import shutil
import time
import uuid
from pathlib import Path
from typing import Any

from fastapi import Body, FastAPI, HTTPException, Query, WebSocket, WebSocketDisconnect
from fastapi.responses import FileResponse, HTMLResponse, JSONResponse
from fastapi.staticfiles import StaticFiles

from .hub import Hub
from .session import FpSession, LaunchConfig, find_chromium

ROOT = Path(__file__).resolve().parent.parent
DATA = Path(__import__("os").environ.get("FPI_DATA_DIR") or (ROOT / "data"))
DATA.mkdir(parents=True, exist_ok=True)
HUB = Hub(DATA / "fp-insight.db")
_APP = FastAPI(title="fp-insight", version="0.1.0")
SESSIONS: dict[str, FpSession] = {}

_APP.mount("/static", StaticFiles(directory=str(ROOT / "dashboard")), name="static")


# --------------------------------------------------------------------- 探针入站
@_APP.websocket("/ws/fp")
async def ws_fp(ws: WebSocket) -> None:
    await ws.accept()
    sid = ""
    try:
        while True:
            raw = await ws.receive_text()
            try:
                msg = json.loads(raw)
            except Exception:
                continue
            kind = msg.get("kind") or "ev"
            sid = msg.get("sid") or sid
            HUB.on_client_message(sid, kind, msg.get("payload"), msg.get("frame"), msg.get("inst"))
    except WebSocketDisconnect:
        pass
    except Exception:
        pass
    finally:
        if sid:
            HUB.broadcast({"ch": "fpi", "kind": "meta", "sid": sid,
                           "payload": {"type": "probe_disconnected"}})


# ----------------------------------------------------------------- dashboard 流
@_APP.websocket("/ws/ui")
async def ws_ui(ws: WebSocket) -> None:
    await ws.accept()
    q = HUB.subscribe()
    try:
        await ws.send_text(json.dumps({"ch": "fpi", "kind": "hello", "payload": HUB.stats},
                                      ensure_ascii=False))
        while True:
            msg = await q.get()
            await ws.send_text(json.dumps(msg, ensure_ascii=False))
    except WebSocketDisconnect:
        pass
    except Exception:
        pass
    finally:
        HUB.unsubscribe(q)


# ------------------------------------------------------------------- 基础面
@_APP.get("/health")
async def health() -> dict:
    """必须毫秒级: 注册中心的探测窗口远小于"你觉得不快"的几百毫秒。"""
    return {"ok": True, "sessions": len(SESSIONS)}


@_APP.get("/", response_class=HTMLResponse)
async def index() -> Any:
    p = ROOT / "dashboard" / "index.html"
    if not p.exists():
        return JSONResponse({"error": "dashboard 未构建", "path": str(p)}, status_code=500)
    return HTMLResponse(p.read_text(encoding="utf-8"))


@_APP.get("/api/state")
async def api_state() -> dict:
    return {"ok": True, "sessions": [s.describe() for s in SESSIONS.values()],
            "stats": HUB.stats, "db": str(DATA / "fp-insight.db"),
            "chromium": find_chromium(), "now": time.time()}


# ------------------------------------------------------------------- 会话控制
@_APP.post("/api/sessions")
async def create_session(cfg: dict = Body(default={})) -> dict:
    sid = cfg.get("sid") or ("s" + uuid.uuid4().hex[:10])
    if sid in SESSIONS:
        raise HTTPException(409, f"会话已存在: {sid}")
    workdir = DATA / "sessions" / sid
    workdir.mkdir(parents=True, exist_ok=True)
    lc = LaunchConfig(
        headless=bool(cfg.get("headless", True)),
        chromium=cfg.get("chromium"),
        proxy=cfg.get("proxy"),
        width=int(cfg.get("width", 1440)),
        height=int(cfg.get("height", 900)),
        screen=(int(cfg["screen"][0]), int(cfg["screen"][1])) if cfg.get("screen") else None,
        dsf=float(cfg.get("dsf", 1.0)),
        timezone=cfg.get("timezone"),
        locale=cfg.get("locale"),
        user_agent=cfg.get("user_agent"),
        extra_args=list(cfg.get("extra_args") or []),
        keep_profile=bool(cfg.get("keep_profile", False)),
    )
    s = FpSession(sid, HUB, workdir, lc, label=cfg.get("label") or "",
                  target=cfg.get("target") or "", mode="cdp")
    try:
        info = await s.start()
    except Exception as e:
        HUB.submit(("session", sid, cfg.get("label") or sid, cfg.get("target") or "", "cdp",
                    "failed", {"error": str(e)}, None))
        raise HTTPException(500, f"启动失败: {e}") from e
    SESSIONS[sid] = s
    if cfg.get("target"):
        await s.navigate(str(cfg["target"]), int(cfg.get("wait_ms", 3000)))
    return {"ok": True, "session": info}


def _sess(sid: str) -> FpSession:
    s = SESSIONS.get(sid)
    if not s:
        raise HTTPException(404, f"会话不存在: {sid}")
    return s


@_APP.post("/api/sessions/{sid}/navigate")
async def s_navigate(sid: str, body: dict = Body(default={})) -> dict:
    return await _sess(sid).navigate(str(body.get("url") or ""), int(body.get("wait_ms", 2500)))


@_APP.get("/api/sessions/{sid}/dom")
async def s_dom(sid: str, limit: int = Query(200)) -> dict:
    return await _sess(sid).dom(limit)


@_APP.post("/api/sessions/{sid}/click")
async def s_click(sid: str, body: dict = Body(default={})) -> dict:
    import random
    s = _sess(sid)
    x, y = body.get("x"), body.get("y")
    if x is None or y is None:
        raise HTTPException(400, "需要 x/y (像素坐标)")
    return await s.click(int(x), int(y), bool(body.get("jitter", True)))


@_APP.post("/api/sessions/{sid}/type")
async def s_type(sid: str, body: dict = Body(default={})) -> dict:
    s = _sess(sid)
    text = str(body.get("text") or "")
    out = await s.insert_text(text)
    if body.get("enter"):
        await s.press("Enter")
    return out


@_APP.post("/api/sessions/{sid}/key")
async def s_key(sid: str, body: dict = Body(default={})) -> dict:
    return await _sess(sid).press(str(body.get("key") or "Enter"))


@_APP.post("/api/sessions/{sid}/mark")
async def s_mark(sid: str, body: dict = Body(default={})) -> dict:
    return await _sess(sid).mark(str(body.get("label") or "mark"), str(body.get("note") or ""))


@_APP.post("/api/sessions/{sid}/clear")
async def s_clear(sid: str, body: dict = Body(default={})) -> dict:
    return await _sess(sid).clear(str(body.get("what") or "all"))


@_APP.post("/api/sessions/{sid}/shot")
async def s_shot(sid: str) -> dict:
    s = _sess(sid)
    p = await s.save_shot()
    await s.commit_counts()
    return {"ok": True, "file": Path(p).name, "url": f"/api/sessions/{sid}/shots/{Path(p).name}"}


@_APP.get("/api/sessions/{sid}/shots/{name}")
async def s_shot_get(sid: str, name: str) -> Any:
    s = _sess(sid)
    p = s.shots / name
    if not p.exists() or ".." in name:
        raise HTTPException(404, "截图不存在")
    return FileResponse(str(p), media_type="image/png")


@_APP.delete("/api/sessions/{sid}")
async def s_stop(sid: str) -> dict:
    s = _sess(sid)
    out = await s.stop()
    SESSIONS.pop(sid, None)
    return out


# ------------------------------------------------------------------- 证据查询
def _q(sql: str, args: tuple = ()) -> list[dict]:
    con = HUB._init_con  # noqa: SLF001 - 只读连接, 不跨线程写
    con.row_factory = __import__("sqlite3").Row
    cur = con.execute(sql, args)
    rows = [dict(r) for r in cur.fetchall()]
    con.row_factory = None
    return rows


async def _settle(timeout: float = 3.0) -> None:
    """所有"读证据"的端点入口都必须先过这里。

    事件是页内探针**异步回传**的: 写入队列可能还没被 writer 线程消费。
    不等落盘就查库, 会读到一份"看起来站点什么都没采"的空快照 ——
    而这正是最容易被误当成结论的那种假象 (实测踩过: 154 条事件在库, 汇总却报精确读取 0)。
    用 to_thread 以免阻塞事件循环。"""
    if not HUB.drain(timeout):
        return


@_APP.get("/api/sessions/{sid}/calls")
async def s_calls(sid: str, after: int = Query(0), path: str = Query(""), cat: str = Query(""),
                  q: str = Query(""), surface: str = Query(""), limit: int = Query(300)) -> dict:
    await _settle()
    sql = "SELECT * FROM calls WHERE sid = ? AND id > ?"
    args: list[Any] = [sid, after]
    if path:
        sql += " AND path LIKE ?"
        args.append(f"%{path}%")
    if cat:
        sql += " AND cat = ?"
        args.append(cat)
    if surface:
        sql += " AND surface = ?"
        args.append(surface)
    if q:
        sql += " AND (path LIKE ? OR args LIKE ? OR ret LIKE ? OR top_url LIKE ?)"
        args += [f"%{q}%"] * 4
    sql += " ORDER BY id LIMIT ?"
    args.append(min(limit, 5000))
    rows = _q(sql, tuple(args))
    return {"ok": True, "calls": rows, "last_id": rows[-1]["id"] if rows else after}


@_APP.get("/api/sessions/{sid}/summary")
async def s_summary(sid: str) -> dict:
    """站点采集行为的**技术画像**: 每个指纹面被读了几次、什么时候第一次读、从哪个帧读。"""
    await _settle()
    agg = _q(
        "SELECT path, cat, surface, COUNT(*) AS events, MAX(n) AS reads, MIN(id) AS first_id,"
        " MAX(id) AS last_id, AVG(ms) AS avg_ms, MAX(t) AS last_t"
        " FROM calls WHERE sid = ? GROUP BY path, surface ORDER BY reads DESC", (sid,))
    cnt = _q("SELECT path, surf, n FROM counters WHERE sid = ? ORDER BY n DESC", (sid,))
    cmap = {f"{r['path']}|{r['surf']}": r["n"] for r in cnt}
    for r in agg:
        r["reads_precise"] = cmap.get(f"{r['path']}|{r['surface']}", r["reads"])
    by_cat = _q("SELECT cat, COUNT(*) AS events, SUM(0+1) AS n FROM calls WHERE sid = ? GROUP BY cat ORDER BY events DESC",
                (sid,))
    top_frames = _q(
        "SELECT top_url, COUNT(*) AS n FROM calls WHERE sid = ? AND top_url IS NOT NULL AND top_url != ''"
        " GROUP BY top_url ORDER BY n DESC LIMIT 40", (sid,))
    pages = _q("SELECT * FROM pages WHERE sid = ?", (sid,))
    total = _q("SELECT COUNT(*) AS n FROM calls WHERE sid = ?", (sid,))
    return {"ok": True, "sid": sid, "total_events": total[0]["n"] if total else 0,
            "apis": agg, "by_cat": by_cat, "top_frames": top_frames, "pages": pages,
            "counters_total": sum(r["n"] for r in cnt)}


@_APP.get("/api/sessions/{sid}/inv")
async def s_inv(sid: str) -> dict:
    """变量纠察: stable=1 的是"运行期从不变化的数据" —— 反爬用来认人的锚点。"""
    await _settle()
    rows = _q("SELECT * FROM invariants WHERE sid = ? ORDER BY stable DESC, uniq ASC, n DESC", (sid,))
    stable = [r for r in rows if r["stable"]]
    varying = [r for r in rows if not r["stable"]]
    return {"ok": True, "total": len(rows), "stable": stable, "varying": varying}


@_APP.get("/api/sessions/{sid}/snaps")
async def s_snaps(sid: str, limit: int = Query(200)) -> dict:
    rows = _q("SELECT * FROM snaps WHERE sid = ? ORDER BY id DESC LIMIT ?", (sid, min(limit, 2000)))
    for r in rows:
        try:
            r["data"] = json.loads(r["data"])
        except Exception:
            pass
    return {"ok": True, "snaps": rows}


@_APP.get("/api/sessions/{sid}/moments")
async def s_moments(sid: str, limit: int = Query(400)) -> dict:
    await _settle()
    rows = _q("SELECT * FROM moments WHERE sid = ? ORDER BY id DESC LIMIT ?", (sid, min(limit, 2000)))
    for r in rows:
        try:
            r["data"] = json.loads(r["data"])
        except Exception:
            pass
    return {"ok": True, "moments": rows}


@_APP.get("/api/sessions/{sid}/stack")
async def s_stack(sid: str, path: str = Query(...)) -> dict:
    """某个指纹面的**完整调用栈** —— 回答"站点在哪一行读的它"。"""
    rows = _q("SELECT id,seq,n,t,args,ret,ms,stack,top_fn,top_url,top_line FROM calls"
              " WHERE sid = ? AND path = ? ORDER BY id DESC LIMIT 1", (sid, path))
    if not rows:
        return {"ok": False, "error": "没有该 API 的调用记录"}
    r = rows[0]
    try:
        r["stack"] = json.loads(r["stack"] or "[]")
    except Exception:
        r["stack"] = []
    return {"ok": True, "call": r}


@_APP.get("/api/sessions")
async def list_sessions() -> dict:
    rows = _q("SELECT sid,label,target,mode,started,ended,status FROM sessions ORDER BY started DESC LIMIT 200")
    return {"ok": True, "sessions": rows}


@_APP.get("/api/sessions/{sid}/report")
async def s_report(sid: str) -> dict:
    """一键技术画像导出 (给分析工程师看的结论面)。"""
    await _settle()
    s = SESSIONS.get(sid)
    meta = _q("SELECT * FROM sessions WHERE sid = ?", (sid,))
    summ = await s_summary(sid)
    inv = await s_inv(sid)
    return {"ok": True, "session": meta[0] if meta else None,
            "live": s.describe() if s else None,
            "summary": summ, "invariants": {"stable": inv["stable"][:60], "total": inv["total"]},
            "generated": time.time()}


# ------------------------------------------------------------------------ 启动
@_APP.on_event("startup")
async def _startup() -> None:
    HUB.attach_loop(asyncio.get_running_loop())
    # 只在未显式指定时才回落到环境变量 —— 覆盖已设定的端口会把探针引到别的进程上。
    if not HUB.port:
        HUB.port = int(__import__("os").environ.get("FPI_PORT", "28360"))
    await HUB.start()


@_APP.on_event("shutdown")
async def _shutdown() -> None:
    for s in list(SESSIONS.values()):
        try:
            await s.stop()
        except Exception:
            pass
