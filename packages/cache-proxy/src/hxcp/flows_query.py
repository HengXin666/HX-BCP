"""Read side of the flow log: scan, filter, count and purge the day files written by flows.FlowLog."""

# Agent Note: .agents/notes/implemented/architecture/2026-09-19-flow-record-decoupled-from-cache.md
from __future__ import annotations

import json
import os
import time
from pathlib import Path
from typing import Any, Iterator
from urllib.parse import urlparse


# ── 读 / 查 / 清 ────────────────────────────────────────
def day_files(root: Path) -> list[Path]:
    if not root.is_dir():
        return []
    return sorted(root.glob("flows-*.jsonl"), reverse=True)   # 新的在前


def _day_of(p: Path) -> str:
    return p.name[len("flows-"):-len(".jsonl")]


def iter_flows(root: str | Path, *, days: float | None = None, host: str = "",
               status: int | None = None, q: str = "", kind: str = "",
               limit: int = 0, newest_first: bool = True, offset: int = 0,
               client: str = "", via: str = "", since: float = 0) -> Iterator[dict]:
    """扫 flow 文件产出记录。过滤在读取侧做 —— 无索引是 JSONL 的已知代价。

    days: 只看最近 N 天 (按文件名日期筛, 不逐行解析时间戳)。
    host: 后缀匹配 (github.com 命中 api.github.com)。
    """
    root = Path(root).expanduser()
    files = day_files(root)
    if days is not None:
        cutoff = time.strftime("%Y-%m-%d", time.localtime(time.time() - float(days) * 86400))
        files = [f for f in files if _day_of(f) >= cutoff]
    n = 0
    skip = 0
    for f in files:
        try:
            with open(f, encoding="utf-8") as fh:
                rows = [ln for ln in fh if ln.strip()]
        except OSError:
            continue
        if newest_first:
            rows.reverse()
        for ln in rows:
            try:
                rec = json.loads(ln)
            except ValueError:
                continue      # 崩溃时可能留半行, 跳过而不是让整个查询失败
            if kind and rec.get("kind") != kind:
                continue
            if client and str(rec.get("client") or "") != client:
                continue
            if via and str(rec.get("via") or "cdp") != via:
                continue
            if since and float(rec.get("at") or 0) <= since:
                if newest_first:
                    break
                continue
            url = str(rec.get("url") or "")
            if host:
                h = (urlparse(url).hostname or "").lower()
                if not (h == host.lower() or h.endswith("." + host.lower())):
                    continue
            if status is not None and int(rec.get("status") or 0) != int(status):
                continue
            if q and q.lower() not in ln.lower():
                continue
            if skip < int(offset or 0):
                skip += 1          # 已经匹配上的才计数: offset 是"第几条命中结果", 不是第几行
                continue
            yield rec
            n += 1
            if limit and n >= limit:
                return


def flow_stats(root: str | Path) -> dict[str, Any]:
    root = Path(root).expanduser()
    files = day_files(root)
    total = 0
    bytes_ = 0
    first = ""
    last = ""
    kinds: dict[str, int] = {}
    verdicts: dict[str, int] = {}
    unparsed = 0
    for f in files:
        try:
            bytes_ += f.stat().st_size
        except OSError:
            continue
        try:
            with open(f, encoding="utf-8") as fh:
                for ln in fh:
                    if not ln.strip():
                        continue
                    total += 1
                    try:
                        rec = json.loads(ln)
                    except ValueError:
                        unparsed += 1
                        continue
                    d = time.strftime("%Y-%m-%d %H:%M:%S",
                                      time.localtime(float(rec.get("at") or 0)))
                    if not first or d < first:
                        first = d
                    if not last or d > last:
                        last = d
                    k = str(rec.get("kind") or "?")
                    kinds[k] = kinds.get(k, 0) + 1
                    v = rec.get("verdict")
                    if v:
                        v = str(v)
                        verdicts[v] = verdicts.get(v, 0) + 1
        except OSError:
            continue
    top = sorted(verdicts.items(), key=lambda kv: -kv[1])[:12]
    return {"root": str(root), "files": len(files), "records": total, "bytes": bytes_,
            "first": first, "last": last, "kinds": kinds, "unparsed": unparsed,
            "top_verdicts": [{"verdict": k, "count": v} for k, v in top]}


def purge(root: str | Path, keep_days: float) -> int:
    """删除早于 keep_days 的 flow 文件。返回删除数。缓存 (entries) 不受影响。"""
    root = Path(root).expanduser()
    cutoff = time.strftime("%Y-%m-%d", time.localtime(time.time() - float(keep_days) * 86400))
    n = 0
    for f in day_files(root):
        if _day_of(f) < cutoff:
            try:
                os.remove(f)
                n += 1
            except OSError:
                pass
    return n
