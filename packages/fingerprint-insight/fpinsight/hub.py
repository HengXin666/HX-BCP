"""fp-insight 事件中枢: 指纹事件全文入库 (SQLite) + 实时扇出给 dashboard。

设计要点 (为什么不是内存里堆事件):
- 事件是**证据**, 必须可回溯。dashboard 晚连、断线、刷新都不该丢数据 → 落库为准, 推送为辅。
- CDP 把 main world 变成"可信事件源"; 入库时显式标记来源, 不自我欺骗。
- 每个调用都带真实"查看次数"计数: 被令牌桶抑制的事件计数仍然精确 (见 inject.js)。
"""

# Agent Note: .agents/notes/implemented/feature/2026-09-22-fp-insight-live-fingerprint-forensics.md
from __future__ import annotations

import asyncio
import contextlib
import json
import os
import sqlite3
import threading
import time
from pathlib import Path
from typing import Any

SCHEMA = """
CREATE TABLE IF NOT EXISTS calls (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  sid         TEXT NOT NULL,
  frame       TEXT,
  inst        TEXT,
  seq         INTEGER,
  path        TEXT NOT NULL,
  surface     TEXT,
  cat         TEXT,
  n           INTEGER,
  key         TEXT,
  args        TEXT,
  ret         TEXT,
  ms          REAL,
  top_fn      TEXT,
  top_url     TEXT,
  top_line    INTEGER,
  stack       TEXT,
  t           INTEGER,
  src         TEXT DEFAULT 'cdp',
  recv_at     REAL
);
CREATE INDEX IF NOT EXISTS idx_calls_sid  ON calls(sid, id);
CREATE INDEX IF NOT EXISTS idx_calls_path ON calls(sid, path);
CREATE TABLE IF NOT EXISTS counters (
  sid   TEXT NOT NULL,
  path  TEXT NOT NULL,
  surf  TEXT NOT NULL,
  n     INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (sid, path, surf)
);
CREATE TABLE IF NOT EXISTS pages (
  sid TEXT PRIMARY KEY, url TEXT, href0 TEXT, ua TEXT, tz TEXT, lang TEXT,
  first_seen REAL, last_seen REAL, top INTEGER DEFAULT 1, src TEXT DEFAULT 'cdp'
);
CREATE TABLE IF NOT EXISTS invariants (
  sid TEXT NOT NULL, frame TEXT, path TEXT NOT NULL, n INTEGER, uniq INTEGER,
  stable INTEGER, vals TEXT, first TEXT, last TEXT, err INTEGER, updated REAL,
  PRIMARY KEY (sid, path)
);
CREATE TABLE IF NOT EXISTS snaps (
  id INTEGER PRIMARY KEY AUTOINCREMENT, sid TEXT, frame TEXT, t INTEGER, recv_at REAL, data TEXT
);
CREATE INDEX IF NOT EXISTS idx_snaps_sid ON snaps(sid, id);
CREATE TABLE IF NOT EXISTS moments (
  id INTEGER PRIMARY KEY AUTOINCREMENT, sid TEXT, label TEXT, note TEXT,
  url TEXT, data TEXT, ts REAL
);
CREATE INDEX IF NOT EXISTS idx_moments_sid ON moments(sid, id);
CREATE TABLE IF NOT EXISTS sessions (
  sid TEXT PRIMARY KEY, label TEXT, target TEXT, mode TEXT, started REAL,
  ended REAL, status TEXT, meta TEXT, artifact TEXT
);
"""

_MUTABLE = {"counters", "invariants", "pages", "sessions"}


class Hub:
    """单写线程的 SQLite 中枢 + asyncio 扇出。

    - 写入全部经 queue.Queue 交给唯一 writer 线程 (sqlite 连接不跨线程)。
    - 每条事件带单调 id (== rowid) 作为游标, 前端断线重连后按 id 增量补齐。
    """

    def __init__(self, db_path: str | Path, port: int = 0) -> None:
        self.db_path = str(db_path)
        # 探针回传端口。0 = 未定, 由服务启动时按实际监听端口补上。
        # 绝不能在这里硬编码默认值: 测试/多实例下探针会连到别的进程的端口上,
        # 现象是"页面里事件都有、库里一条没有", 极易被误判成注入失败。
        self.port = port
        Path(self.db_path).parent.mkdir(parents=True, exist_ok=True)
        self._q: "asyncio.Queue[tuple]" = asyncio.Queue(maxsize=200_000)
        self._subs: set[asyncio.Queue] = set()
        self._loop: asyncio.AbstractEventLoop | None = None
        self._writer: threading.Thread | None = None
        self._stop = False
        self._flushes: list[threading.Event] = []
        self._stats = {"calls": 0, "snaps": 0, "inv": 0, "dropped": 0, "errors": 0}

    # ------------------------------------------------------------- 生命周期
    def attach_loop(self, loop: asyncio.AbstractEventLoop) -> None:
        self._loop = loop

    async def start(self) -> None:
        self._conn_init()
        self._writer = threading.Thread(target=self._writer_loop, name="fpi-writer", daemon=True)
        self._writer.start()

    def _conn_init(self) -> None:
        con = sqlite3.connect(self.db_path, timeout=30, check_same_thread=False)
        con.execute("PRAGMA journal_mode=WAL")
        con.execute("PRAGMA synchronous=NORMAL")
        con.executescript(SCHEMA)
        con.commit()
        self._init_con = con

    def stop(self) -> None:
        self._stop = True
        with contextlib.suppress(Exception):
            self._init_con.close()

    # ---------------------------------------------------------------- 写入
    def _writer_loop(self) -> None:
        con = sqlite3.connect(self.db_path, timeout=30)
        con.execute("PRAGMA journal_mode=WAL")
        con.execute("PRAGMA synchronous=NORMAL")
        n = 0
        while not self._stop:
            try:
                item = self._q.get_nowait()
            except Exception:
                time.sleep(0.01)
                # 队列空: 兜底提交 (没有 sentinel 时也要保证数据落盘)
                with contextlib.suppress(Exception):
                    con.commit()
                continue
            if item is None:
                break
            if item[0] == "__flush__":
                # 屏障: 先提交, 再放行等待者。顺序颠倒会让 drain() 返回时数据还没可见。
                with contextlib.suppress(Exception):
                    con.commit()
                w = item[1]
                with contextlib.suppress(Exception):
                    w.set()
                continue
            try:
                self._apply(con, item)
                n += 1
                if n % 256 == 0:
                    con.commit()
            except Exception:
                self._stats["errors"] += 1
        with contextlib.suppress(Exception):
            con.commit()
            con.close()

    def drain(self, timeout: float = 8.0) -> bool:
        """等到写入队列排空且已 commit —— 否则紧接着的只读查询会看到旧快照。
        这是本工具唯一的"证据可见性"边界: 观测 -> drain -> 出画像。

        实现用**队列内哨兵**而不是共享标志位: 后者会让 drain() 在写入线程
        还没来得及清标志时提前返回 (实测踩过: counters 全表 0 行, 而 submit 明明返回成功)。"""
        ev = threading.Event()
        try:
            self._q.put_nowait(("__flush__", ev))
        except Exception:
            return False
        return ev.wait(timeout)

    def _apply(self, con: sqlite3.Connection, item: tuple) -> None:
        kind = item[0]
        if kind == "call":
            con.execute(
                "INSERT INTO calls (sid,frame,inst,seq,path,surface,cat,n,key,args,ret,ms,top_fn,top_url,top_line,stack,t,src,recv_at)"
                " VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
                item[1],
            )
            self._stats["calls"] += 1
        elif kind == "counts":
            # 绝对值快照 -> MAX 合并。幂等: 重复上报、乱序到达、断线重连都不会算重。
            _, sid, m = item
            con.executemany(
                "INSERT INTO counters (sid,path,surf,n) VALUES (?,?,?,?)"
                " ON CONFLICT(sid,path,surf) DO UPDATE SET n = MAX(n, excluded.n)",
                [(sid, p, s, int(n)) for k, n in m.items() if "|" in k and n
                 for p, s in [k.rsplit("|", 1)]],
            )
        elif kind == "page":
            _, sid, url, href0, ua, tz, lang, top, src, ts = item
            con.execute(
                "INSERT INTO pages (sid,url,href0,ua,tz,lang,first_seen,last_seen,top,src)"
                " VALUES (?,?,?,?,?,?,?,?,?,?)"
                # 空白页 (about:blank / 空串) 不得覆盖真实站点 URL: 注入脚本对**每个**新文档
                # 都发一次 hello, 包括初始空白页 —— 实测它会把已记录的真实 URL 抹掉,
                # 画像标题就变成 about:blank, 看起来像"什么都没采到"。
                " ON CONFLICT(sid) DO UPDATE SET"
                " url=CASE WHEN excluded.url IS NULL OR excluded.url IN ('', 'about:blank')"
                "          THEN pages.url ELSE excluded.url END,"
                " last_seen=excluded.last_seen, top=excluded.top, tz=excluded.tz",
                (sid, url, href0, ua, tz, lang, ts, ts, 1 if top else 0, src),
            )
        elif kind == "inv":
            rows = item[1]
            self._stats["inv"] += len(rows)
            now = time.time()
            con.executemany(
                "INSERT INTO invariants (sid,frame,path,n,uniq,stable,vals,first,last,err,updated)"
                " VALUES (?,?,?,?,?,?,?,?,?,?,?)"
                " ON CONFLICT(sid,path) DO UPDATE SET n=excluded.n, uniq=excluded.uniq,"
                " stable=excluded.stable, vals=excluded.vals, first=excluded.first, last=excluded.last,"
                " err=excluded.err, updated=excluded.updated",
                [(r.get("sid") or item[2], r.get("frame"), r["path"], r.get("n", 0), r.get("distinct", 0),
                  1 if r.get("stable") else 0, json.dumps(r.get("vals") or [], ensure_ascii=False),
                  r.get("first"), r.get("last"), r.get("err", 0), now) for r in rows],
            )
        elif kind == "snap":
            _, sid, frame, t, data = item
            con.execute("INSERT INTO snaps (sid,frame,t,recv_at,data) VALUES (?,?,?,?,?)",
                        (sid, frame, t, time.time(), json.dumps(data, ensure_ascii=False)))
            self._stats["snaps"] += 1
        elif kind == "moment":
            _, sid, label, note, url, data = item
            con.execute("INSERT INTO moments (sid,label,note,url,data,ts) VALUES (?,?,?,?,?,?)",
                        (sid, label, note, url, json.dumps(data, ensure_ascii=False), time.time()))
        elif kind == "__flush__":
            raise RuntimeError("sentinel 应由 writer 直接处理")
        elif kind == "session":
            _, sid, label, target, mode, status, meta, artifact = item
            now = time.time()
            con.execute(
                "INSERT INTO sessions (sid,label,target,mode,started,status,meta,artifact) VALUES (?,?,?,?,?,?,?,?)"
                " ON CONFLICT(sid) DO UPDATE SET label=excluded.label, status=excluded.status,"
                " meta=excluded.meta, artifact=excluded.artifact, ended=CASE WHEN excluded.status IN ('closed','failed') THEN ? ELSE sessions.ended END",
                (sid, label, target, mode, now, status, json.dumps(meta or {}, ensure_ascii=False), artifact, now),
            )

    # ---------------------------------------------------------------- 入队
    def submit(self, item: tuple) -> None:
        try:
            self._q.put_nowait(item)
        except Exception:
            self._stats["dropped"] += 1

    def broadcast(self, msg: dict[str, Any]) -> None:
        if self._loop is None:
            return
        for q in list(self._subs):
            try:
                self._loop.call_soon_threadsafe(q.put_nowait, msg)
            except Exception:
                self._subs.discard(q)

    def on_client_message(self, sid: str, kind: str, payload: Any, frame: str | None = None,
                          inst: str | None = None) -> None:
        """由 WS 端点调用 —— 处于 asyncio 线程, 可直接广播。"""
        if kind == "ev":
            for raw in payload or []:
                self._ingest_call(sid, frame, inst, raw)
            self.broadcast({"ch": "fpi", "kind": "ev", "sid": sid, "frame": frame, "payload": payload})
        elif kind == "hello":
            src = (payload or {}).get("src") or "cdp"
            self.submit(("page", sid, (payload or {}).get("url"), (payload or {}).get("href0"),
                         (payload or {}).get("ua"), (payload or {}).get("tz"), (payload or {}).get("lang"),
                         (payload or {}).get("top", True), src, time.time()))
            self.broadcast({"ch": "fpi", "kind": "hello", "sid": sid, "payload": payload})
        elif kind == "inv":
            self.submit(("inv", payload or [], sid, frame))
            self.broadcast({"ch": "fpi", "kind": "inv", "sid": sid, "payload": payload})
        elif kind == "counts":
            self.submit(("counts", sid, payload or {}))
            self.broadcast({"ch": "fpi", "kind": "counts", "sid": sid, "payload": payload})
        elif kind == "snap":
            self.submit(("snap", sid, frame, (payload or {}).get("t", 0), payload))
            self.broadcast({"ch": "fpi", "kind": "snap", "sid": sid, "payload": payload})
        elif kind == "meta":
            self.broadcast({"ch": "fpi", "kind": "meta", "sid": sid, "payload": payload})

    def _ingest_call(self, sid: str, frame: str | None, inst: str | None, ev: dict) -> None:
        if not isinstance(ev, dict):
            return
        if ev.get("kind") == "meta":
            return
        path = ev.get("path")
        if not path:
            return
        st = ev.get("stack") or []
        top = ev.get("top") or {}
        self.submit(("call", (
            sid, frame, inst or ev.get("inst"), ev.get("seq"), path, ev.get("surface"), ev.get("cat"),
            ev.get("n"), ev.get("key"), json.dumps(ev.get("args"), ensure_ascii=False),
            (ev.get("ret") or "")[:2000], ev.get("ms"), top.get("fn"), top.get("url"), top.get("line"),
            json.dumps(st, ensure_ascii=False), ev.get("t"), "cdp", time.time(),
        )))

    # ---------------------------------------------------------------- 订阅
    def subscribe(self) -> asyncio.Queue:
        q: asyncio.Queue = asyncio.Queue(maxsize=10_000)
        self._subs.add(q)
        return q

    def unsubscribe(self, q: asyncio.Queue) -> None:
        self._subs.discard(q)

    @property
    def stats(self) -> dict[str, int]:
        return dict(self._stats, subs=len(self._subs), queued=self._q.qsize())


class MomentStore:
    """记录"点击某个 UI 按钮的瞬间", 供变量纠察做 (动作, 指纹读取) 因果对照。"""

    def __init__(self, hub: Hub) -> None:
        self.hub = hub

    def mark(self, sid: str, label: str, note: str, url: str, data: dict | None = None) -> None:
        self.hub.submit(("moment", sid, label, note, url, data or {}))
