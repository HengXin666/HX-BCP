"""缓存存储 — 内容寻址 blob + SQLite 索引, 带版本身份。

为什么不用单一 sqlite 存 body:
  静态资源在同一站点内高度重复 (同一份 js 被多个页面引用), 内容寻址让去重天然发生;
  且种子导入/导出只是复制 blob 文件, 不需要重放 INSERT。

为什么索引必须带版本身份 (SCHEMA_VERSION):
  派生数据必须能**全量重建**且可判"这份索引是不是当前代码产的"。版本不符时直接丢弃重建,
  而不是尝试迁移 —— 迁移逻辑会成为长期负担, 而重建成本 = 一次回源。
  这是本项目对"派生索引"的通用纪律。

与旧 cacheproxy 的关系: 沿用它的缓存键规范化语义 (剥离纯缓存破坏参数),
但**不沿用**它必须 MITM 的假设 —— 本模块只被浏览器层拦截调用。
"""
from __future__ import annotations

import hashlib
import json
import sqlite3
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from urllib.parse import parse_qsl, urlencode, urlparse, urlunparse

# 索引格式版本。改动表结构 / 语义时必须 +1 (不符即重建, 不做迁移)。
SCHEMA_VERSION = 1

# 已知的纯缓存破坏参数: 内容不随其变化, 归一化后同一资源命中同一键。
VOLATILE_PARAMS = frozenset({
    "u", "v", "_", "t", "ts", "timestamp", "cachebuster", "cb",
    "session_id", "sid", "_ga", "gclid", "fbclid",
})


def normalize_url(url: str, drop_volatile: bool = True) -> str:
    """归一化 URL: 排序查询参数 + 剥离已知缓存破坏参数。

    为什么要排序: 同一资源的不同参数顺序是同一资源, 但字典序不同 → 不同缓存键 → 重复回源。
    """
    p = urlparse(url)
    if not p.query:
        return urlunparse((p.scheme, p.netloc, p.path, p.params, "", ""))
    pairs = parse_qsl(p.query, keep_blank_values=True)
    if drop_volatile:
        pairs = [(k, v) for k, v in pairs if k.lower() not in VOLATILE_PARAMS]
    pairs.sort()
    return urlunparse((p.scheme, p.netloc, p.path, p.params, urlencode(pairs), ""))


def cache_key(method: str, url: str) -> str:
    return hashlib.sha256((method.upper() + "|" + normalize_url(url)).encode()).hexdigest()


@dataclass
class Entry:
    key: str
    url: str
    method: str
    status: int
    headers: list[list[str]]
    body_hash: str
    size: int
    stored_at: float
    fresh_until: float
    etag: str = ""
    hit_count: int = 0
    source: str = "net"          # net | seed | replay
    tags: list[str] = field(default_factory=list)

    def is_fresh(self, now: float | None = None) -> bool:
        now = time.time() if now is None else now
        return now < self.fresh_until


class Store:
    """内容寻址缓存库。线程安全 (单锁; 拦截回调与 CLI 可能同进程)。"""

    def __init__(self, root: str | Path, *, max_entries_bytes: int = 8 * 1024 * 1024) -> None:
        self.root = Path(root).expanduser()
        self.root.mkdir(parents=True, exist_ok=True)
        self.blobs = self.root / "blobs"
        self.blobs.mkdir(exist_ok=True)
        self.max_entries_bytes = max_entries_bytes
        self._lock = threading.RLock()
        self._db_path = self.root / "index.sqlite3"
        self._conn = sqlite3.connect(str(self._db_path), check_same_thread=False)
        self._conn.row_factory = sqlite3.Row
        self._rebuilt = False
        self._ensure_schema()

    # ── schema ────────────────────────────────────────────
    def _ensure_schema(self) -> None:
        with self._lock:
            self._conn.executescript(
                """
                CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
                CREATE TABLE IF NOT EXISTS entries (
                    key         TEXT PRIMARY KEY,
                    url         TEXT NOT NULL,
                    method      TEXT NOT NULL,
                    status      INTEGER NOT NULL,
                    headers     TEXT NOT NULL,
                    body_hash   TEXT NOT NULL,
                    size        INTEGER NOT NULL,
                    stored_at   REAL NOT NULL,
                    fresh_until REAL NOT NULL,
                    etag        TEXT NOT NULL DEFAULT '',
                    hit_count   INTEGER NOT NULL DEFAULT 0,
                    source      TEXT NOT NULL DEFAULT 'net',
                    tags        TEXT NOT NULL DEFAULT '[]'
                );
                CREATE TABLE IF NOT EXISTS hits (id INTEGER PRIMARY KEY AUTOINCREMENT,
                    key TEXT NOT NULL, at REAL NOT NULL);
                CREATE INDEX IF NOT EXISTS idx_entries_url ON entries(url);
                CREATE INDEX IF NOT EXISTS idx_hits_at ON hits(at);
                """
            )
            row = self._conn.execute("SELECT v FROM meta WHERE k='schema_version'").fetchone()
            cur = int(row["v"]) if row else None
            if cur != SCHEMA_VERSION:
                # 版本不符 → 丢弃重建 (派生数据, 可全量重建; 不做迁移)
                self._conn.executescript("DELETE FROM entries; DELETE FROM hits;")
                self._conn.execute(
                    "INSERT INTO meta(k,v) VALUES('schema_version',?) "
                    "ON CONFLICT(k) DO UPDATE SET v=excluded.v", (str(SCHEMA_VERSION),))
                self._rebuilt = True
                # 旧 blob 一并清理, 否则磁盘只增不减
                for f in self.blobs.rglob("*.bin"):
                    try:
                        f.unlink()
                    except OSError:
                        pass
            self._conn.execute(
                "INSERT INTO meta(k,v) VALUES('created_at',?) ON CONFLICT(k) DO NOTHING",
                (str(time.time()),))
            self._conn.commit()

    @property
    def rebuilt(self) -> bool:
        """本次打开是否是"因版本不符而重建"。调用方据此决定是否要重新导入种子。"""
        return self._rebuilt

    # ── blob ──────────────────────────────────────────────
    def _blob_path(self, body_hash: str) -> Path:
        return self.blobs / body_hash[:2] / (body_hash + ".bin")

    def put_blob(self, body: bytes) -> str:
        h = hashlib.sha256(body).hexdigest()
        p = self._blob_path(h)
        if not p.exists():
            p.parent.mkdir(parents=True, exist_ok=True)
            tmp = p.with_suffix(".tmp")
            tmp.write_bytes(body)
            tmp.replace(p)
        return h

    def get_blob(self, body_hash: str) -> bytes | None:
        p = self._blob_path(body_hash)
        try:
            return p.read_bytes()
        except OSError:
            return None

    # ── 读写 ──────────────────────────────────────────────
    def close(self) -> None:
        with self._lock:
            try:
                self._conn.close()
            except Exception:
                pass

    def put(self, url: str, method: str, status: int, headers: list[list[str]],
            body: bytes, *, ttl: float, etag: str = "", source: str = "net",
            tags: list[str] | None = None) -> Entry | None:
        if len(body) > self.max_entries_bytes:
            return None
        key = cache_key(method, url)
        now = time.time()
        with self._lock:
            bh = self.put_blob(body)
            self._conn.execute(
                "INSERT INTO entries(key,url,method,status,headers,body_hash,size,stored_at,"
                "fresh_until,etag,hit_count,source,tags) VALUES(?,?,?,?,?,?,?,?,?,?,0,?,?) "
                "ON CONFLICT(key) DO UPDATE SET status=excluded.status, headers=excluded.headers,"
                "body_hash=excluded.body_hash, size=excluded.size, stored_at=excluded.stored_at,"
                "fresh_until=excluded.fresh_until, etag=excluded.etag, source=excluded.source,"
                "tags=excluded.tags",
                (key, url, method.upper(), status, json.dumps(headers), bh, len(body),
                 now, now + ttl, etag, source, json.dumps(tags or [])))
            self._conn.commit()
        return self.get(key)

    def get(self, key: str) -> Entry | None:
        with self._lock:
            row = self._conn.execute("SELECT * FROM entries WHERE key=?", (key,)).fetchone()
        if row is None:
            return None
        return Entry(
            key=row["key"], url=row["url"], method=row["method"], status=row["status"],
            headers=json.loads(row["headers"]), body_hash=row["body_hash"], size=row["size"],
            stored_at=row["stored_at"], fresh_until=row["fresh_until"], etag=row["etag"],
            hit_count=row["hit_count"], source=row["source"], tags=json.loads(row["tags"]))

    def lookup(self, url: str, method: str = "GET") -> Entry | None:
        return self.get(cache_key(method, url))

    def record_hit(self, key: str) -> None:
        now = time.time()
        with self._lock:
            self._conn.execute("UPDATE entries SET hit_count=hit_count+1 WHERE key=?", (key,))
            self._conn.execute("INSERT INTO hits(key,at) VALUES(?,?)", (key, now))
            self._conn.commit()

    def purge_expired(self, now: float | None = None) -> int:
        now = time.time() if now is None else now
        with self._lock:
            rows = self._conn.execute("SELECT key,body_hash FROM entries WHERE fresh_until<?",
                                      (now,)).fetchall()
            for r in rows:
                self._conn.execute("DELETE FROM entries WHERE key=?", (r["key"],))
                # 仅当没有别的条目引用同一 blob 时才删文件 (内容寻址下多个键可共享)
                still = self._conn.execute("SELECT 1 FROM entries WHERE body_hash=? LIMIT 1",
                                           (r["body_hash"],)).fetchone()
                if not still:
                    try:
                        self._blob_path(r["body_hash"]).unlink()
                    except OSError:
                        pass
            self._conn.commit()
        return len(rows)

    def stats(self) -> dict[str, Any]:
        try:
            with self._lock:
                row = self._conn.execute(
                    "SELECT COUNT(*) AS n, COALESCE(SUM(size),0) AS bytes, "
                    "COALESCE(SUM(hit_count),0) AS hits FROM entries").fetchone()
                served = self._conn.execute(
                    "SELECT COALESCE(SUM(e.size),0) AS b FROM hits h JOIN entries e "
                    "ON e.key=h.key").fetchone()
            return {"entries": row["n"], "bytes": row["bytes"], "hits": row["hits"],
                    "bytes_served": served["b"], "schema_version": SCHEMA_VERSION,
                    "root": str(self.root)}
        except sqlite3.ProgrammingError:
            # 已关闭的连接降级返回, 不抛异常 (收尾路径不该把进程带崩)
            return {"entries": -1, "bytes": -1, "hits": -1, "bytes_served": -1, "closed": True}
