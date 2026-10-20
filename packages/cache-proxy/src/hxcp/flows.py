"""请求级 flow 记录 — JSONL 追加写, 按天切分, 与缓存判定完全解耦。

取证档 (2026-09-26 新增, ForensicStore):
  JSONL 里的 post_data 是**摘要** (前 8K), 因为 flow 是给人/agent 扫的观测面 ——
  一条 multipart 上传能把单条记录撑到几十 MB, 扫描成本随之崩塌。但协议化回放要的是
  **完整体**: 8K 截断的请求体回放不了。
  解法不是调大 MAX_BODY (那会把每条记录都撑大, 拖垮 flow_search 的逐行扫描),
  而是「摘要留在 flows, 全量存到旁边的 payload 池, 记录里只留 sha256 引用」。
  实测依据: 生产 flows 文件里 1446 条带体记录中 309 条恰好 8206 字符 (即 8K 截断),
  中位数却有 1194 —— 截断精确地落在最大的那批上, 而它们正是回放最需要的。

为什么不能塞进 store.py 的 entries 表:
  store.py 的纪律是「SCHEMA_VERSION 不符即丢弃重建」—— 那对缓存索引是对的
  (派生数据, 重建成本 = 一次回源)。但 flow 是一次性观测: 请求头、响应头、时序、
  被拒缓存的那类响应, 回源拿不回来。塞进同一张表就会跟着缓存一起被清掉,
  等于拿「可重建」的纪律管「不可重建」的数据。

为什么 JSONL 而不是 SQLite:
  追加写天然崩溃安全 (最多丢最后一行), 不需要事务, 且 grep / agent 直接可读。
  代价是无索引 —— 查询靠扫当天文件, 单文件几万行是毫秒级。真要 SQL 时再建派生索引,
  那是另一个模块的事 (本仓通用纪律: 派生索引必须可全量重建 + 带版本身份)。

非阻塞写入 (硬性):
  write() 只入队, 落盘在独立线程。观测面永远不能反压被观测的链路 ——
  engine 的 passed 分支就在 CDP reader 回调里, 在那里直接写文件会把浏览器请求拖死。
  队列满就丢并计数, 丢了多少进 stats, 不静默。

与 policy 的关系:
  缓存判定 (policy.decide) 与记录无关。被 policy 拒绝缓存的那些路径照样写 flow ——
  拒绝的是「把这份响应喂给别的会话」, 不是「我不能看」。
"""

# Agent Note: .agents/notes/implemented/feature/2026-09-26-hxcp-forensic-payload-store.md

# Agent Note: .agents/notes/implemented/architecture/2026-09-19-flow-record-decoupled-from-cache.md
from __future__ import annotations

import hashlib
import json
import os
import queue
import re
import threading
import time
from pathlib import Path
from typing import Any, Iterator
from urllib.parse import urlparse

# 单字段截断。flow 是给人/agent 看的观测面, 不是取证级存档: 不截断时一个 multipart
# 上传能把单条记录撑到几十 MB, 扫描成本随之崩塌。
MAX_HEADER_VALUE = 2048
MAX_BODY = 8192
MAX_LINE = 65536

# 队列深度。满了丢新记录 —— 丢的是观测, 保的是被观测的链路。
QUEUE_MAX = 20000

# ── 取证档 (payload 池) 上限 ─────────────────────────────
# 默认 1 GiB 池 + 512 MiB 磁盘余量水位。两者都是**硬闸**: 池超预算按 mtime 序轮转删除,
# 磁盘余量低于水位继续删 —— 取证档是"开启后一直在收流量"的, 它绝不能把机器写爆。
FORENSIC_MAX_BYTES = 1024 * 1024 * 1024
FORENSIC_MIN_FREE_BYTES = 512 * 1024 * 1024
# 单条请求体上限: 一条 1GB 上传能一次性把整个池挤空 (用一个请求毁掉全部取证)。
FORENSIC_MAX_PART_BYTES = 16 * 1024 * 1024
# 轮到删时的扫描节流: 超预算期间每次写都扫目录 = 写入路径被自己的回收拖垮。
FORENSIC_GC_INTERVAL_S = 60.0

# 引用格式闸: get() 直接拿它当路径片段用, 不校验就是一条路径穿越。
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


def _trim_headers(headers: Any) -> dict[str, str]:
    """统一 headers 形态: CDP 给的是 dict, store 里存的是 list[list]。"""
    if not headers:
        return {}
    items: list[tuple[Any, Any]]
    if isinstance(headers, dict):
        items = list(headers.items())
    elif isinstance(headers, (list, tuple)):
        items = []
        for h in headers:
            if isinstance(h, (list, tuple)) and len(h) >= 2:
                items.append((h[0], h[1]))
            elif isinstance(h, dict):
                items.append((h.get("name", ""), h.get("value", "")))
    else:
        return {}
    out: dict[str, str] = {}
    for k, v in items:
        s = str(v)
        if len(s) > MAX_HEADER_VALUE:
            s = s[:MAX_HEADER_VALUE] + "...[truncated]"
        out[str(k)] = s
    return out


def trim_record(rec: dict, *, max_body: int = MAX_BODY) -> dict:
    """规范化一条 flow 记录。返回新 dict, 不改入参。

    取证档**不改这里**: 记录里的 post_data 永远是 8K 摘要, 完整体在 payload 池里由
    body_ref 指向。把完整体塞进 JSONL 会把每条记录都撑大 —— 那正是要避免的事。
    """
    out = dict(rec)
    if "req_headers" in out:
        out["req_headers"] = _trim_headers(out["req_headers"])
    if "resp_headers" in out:
        out["resp_headers"] = _trim_headers(out["resp_headers"])
    pd = out.get("post_data")
    if isinstance(pd, str) and len(pd) > max_body:
        out["post_data"] = pd[:max_body] + "...[truncated]"
    return out


def _free_bytes(p: Path) -> int:
    """磁盘余量。取不到就返回 -1, 判据侧把 -1 当"未知"而不是"满了" ——
    把未知当满会让取证档在取不到 statvfs 的平台上静默停摆。"""
    try:
        import shutil
        return int(shutil.disk_usage(str(p)).free)
    except Exception:
        return -1


class ForensicStore:
    """请求体取证池 — 内容寻址, 可与 flow 记录分处不同磁盘。

    为什么内容寻址 (sha256 前 2 位做目录): 同一份请求体重复发送 (轮询/重试) 只落一份,
    回放时按引用取回的字节与捕获时**逐字节相同** —— 这是"取证"这个词的全部含义。
    幂等写入也让"捕获时写、查询时读"两处不需要任何协调。

    三个硬闸 (缺一个就是"用取证档把机器写爆"):
      1. 单条上限 FORENSIC_MAX_PART_BYTES —— 一个巨型上传不该挤掉池里其余全部;
      2. 池预算 max_bytes —— 超了按 mtime 从旧到新删, 直到回到预算内;
      3. 磁盘余量水位 min_free_bytes —— 连预算都还没到但盘快满了, 照样删。
    全部在**每次写之后**检查, 而不是定时任务: 取证档在持续收流量, 定时器有窗口期。

    写失败绝不抛给调用方: 调用点在 CDP 回调链路上, 取证是观测行为,
    不能因为磁盘满就把被观测的请求拖死。失败原因进 stats 的 last_error。
    """

    def __init__(self, root: str | Path, *, enabled: bool = False,
                 max_bytes: int = FORENSIC_MAX_BYTES,
                 min_free_bytes: int = FORENSIC_MIN_FREE_BYTES,
                 max_part_bytes: int = FORENSIC_MAX_PART_BYTES) -> None:
        self.root = Path(root).expanduser()
        self.enabled = bool(enabled)
        self.max_bytes = int(max_bytes)
        self.min_free_bytes = int(min_free_bytes)
        self.max_part_bytes = int(max_part_bytes)
        self._lock = threading.Lock()
        self._written = 0
        self._deduped = 0
        self._bytes = 0
        self._evicted = 0
        self._too_large = 0
        self._last_error = ""
        self._last_gc = 0.0

    # ── 定位与读写 ────────────────────────────────────────
    def _bucket(self, h: str) -> Path:
        return self.root / h[:2]

    def path_for(self, h: str) -> Path:
        return self._bucket(h) / (h + ".bin")

    def put(self, data: bytes) -> dict:
        """写入并返回引用标记 (不含裸 hash —— 引用必须自带尺度信息)。"""
        h = hashlib.sha256(data).hexdigest()
        out = {"sha256": h, "size": len(data)}
        if not data:
            # 空体不落文件也不给引用: 池里几百个 0 字节文件会让统计与轮转都失真,
            # 而"引用"的价值恰在于它指向一份**存在且可读**的字节 (调用方按 sha256=="" 判空)。
            return {"sha256": "", "size": 0}
        if not self.enabled:
            out["dropped"] = "forensic_disabled"
            return out
        if len(data) > self.max_part_bytes:
            with self._lock:
                self._too_large += 1
            out["dropped"] = "too_large"
            return out
        dst = self.path_for(h)
        try:
            if dst.exists():
                with self._lock:
                    self._deduped += 1
                return out                      # 同一份体只落一份
            dst.parent.mkdir(parents=True, exist_ok=True)
            tmp = dst.with_suffix(".bin.tmp")
            with open(tmp, "wb") as f:
                f.write(data)
            os.replace(tmp, dst)                # 原子落地: 半截文件不会被当成取证证据
            with self._lock:
                self._written += 1
                self._bytes += len(data)
            self._enforce_budget()
        except Exception as ex:  # noqa: BLE001
            with self._lock:
                self._last_error = type(ex).__name__ + " " + str(ex)[:140]
            out["dropped"] = "write_failed"
        return out

    def get(self, h: str) -> bytes | None:
        """按 sha256 取回完整请求体。形态非法直接返回 None (别把路径拼出池子)。"""
        if not isinstance(h, str) or not _SHA256_RE.match(h):
            return None
        try:
            return self.path_for(h).read_bytes()
        except OSError:
            return None

    def has(self, h: str) -> bool:
        return self.get(h) is not None

    def stats(self) -> dict[str, Any]:
        with self._lock:
            return {"enabled": self.enabled, "root": str(self.root),
                    "written": self._written, "deduped": self._deduped,
                    "bytes": self._bytes, "evicted": self._evicted,
                    "too_large": self._too_large, "max_bytes": self.max_bytes,
                    "min_free_bytes": self.min_free_bytes,
                    "max_part_bytes": self.max_part_bytes,
                    "error": self._last_error}

    # ── 预算与轮转 ────────────────────────────────────────
    def _files(self) -> list[tuple[float, int, Path]]:
        out: list[tuple[float, int, Path]] = []
        try:
            for p in self.root.glob("*/*.bin"):
                try:
                    st = p.stat()
                except OSError:
                    continue
                out.append((st.st_mtime, st.st_size, p))
        except OSError:
            pass
        out.sort()            # 最旧在前
        return out

    def _enforce_budget(self) -> None:
        """两个闸: 池预算 + 磁盘余量。只删, 不拦写入 (拦写入 = 反压被观测链路)。"""
        now = time.time()
        with self._lock:
            if now - self._last_gc < FORENSIC_GC_INTERVAL_S:
                return
            self._last_gc = now
        items = self._files()
        total = sum(sz for _, sz, _ in items)
        free = _free_bytes(self.root)
        over_pool = total > self.max_bytes
        over_free = free >= 0 and free < self.min_free_bytes
        if not (over_pool or over_free):
            return
        i = 0
        while i < len(items) and (total > self.max_bytes or (free >= 0 and free < self.min_free_bytes)):
            _, sz, p = items[i]
            try:
                p.unlink()
                total -= sz
                with self._lock:
                    self._bytes -= sz
                    self._evicted += 1
                if free >= 0:
                    free += sz              # 估算即可: 下一轮会重新实测
            except OSError:
                pass
            i += 1

    def purge(self, keep_days: float) -> int:
        """按 mtime 删除早于 keep_days 的 payload。返回删除数。

        只有**空目录**才一起删 —— 目录名取自 sha256 前缀, 无日期语义,
        按目录粒度删会把桶里新文件陪葬。
        """
        cutoff = time.time() - float(keep_days) * 86400
        n = 0
        for mt, _, p in self._files():
            if mt < cutoff:
                try:
                    p.unlink()
                    n += 1
                except OSError:
                    pass
        try:
            for d in self.root.iterdir():
                if d.is_dir() and not any(d.iterdir()):
                    d.rmdir()
        except OSError:
            pass
        return n


def forensic_path(root: str | Path) -> Path:
    """payload 池目录。**单一来源**: 引擎写入与 API/MCP 读取都从这里取,
    不各自拼路径 —— 两处拼路径必然有一天不同步 (本仓栽过判据散落两处)。"""
    return Path(root).expanduser() / "flows" / "payloads"


def open_forensic(root: str | Path, **kw: Any) -> ForensicStore:
    """读侧用的池句柄 (enabled=True: 读不写的时候也需要它可用)。"""
    return ForensicStore(forensic_path(root), enabled=True, **kw)


class FlowLog:
    """按天切分的 JSONL flow 记录器。线程安全, write() 非阻塞。"""

    def __init__(self, root: str | Path, *, enabled: bool = True, max_body: int = MAX_BODY,
                 queue_max: int = QUEUE_MAX, forensic: "ForensicStore | None" = None) -> None:
        self.root = Path(root).expanduser()
        self.enabled = bool(enabled)
        self.max_body = int(max_body)
        # 取证档: 非空即"请求体整份落池 + 记录里留引用" (见模块头与 ForensicStore)。
        self.forensic = forensic
        self._q: queue.Queue = queue.Queue(maxsize=int(queue_max))
        self._stop = threading.Event()
        self._lock = threading.Lock()
        self._fh = None
        self._fh_day = ""
        self._written = 0
        self._dropped = 0
        self._last_error = ""
        self._t: threading.Thread | None = None
        if self.enabled:
            self.root.mkdir(parents=True, exist_ok=True)
            self._t = threading.Thread(target=self._loop, name="hxcp-flowlog", daemon=True)
            self._t.start()

    # ── 写 ─────────────────────────────────────────────
    def write(self, rec: dict) -> None:
        """非阻塞入队。队列满 → 丢弃并计数 (绝不阻塞 CDP 回调)。

        取证档下**先落体, 摘要照旧截**: 请求体整份进 payload 池, JSONL 里依旧只留 8K 摘要,
        外加一个 sha256 引用 (几十字节)。
        这是关键取舍: 完整体**不进** JSONL —— 逐行扫描 (flow_search / flow-stats) 的成本
        因此与取证开关完全无关, 而回放要的全量字节由引用取回。
        """
        if not self.enabled:
            return
        if self.forensic is not None:
            if not self.forensic.enabled:
                # 开关开了但池没就绪 (禁用/目录不可写): 记录里明说, 别让人以为拿到了完整体。
                # 引用形态始终只有 body_ref 一种 —— 同一事实两种形状就是两处判据。
                rec = dict(rec)
                rec["body_ref"] = {"sha256": "", "size": 0, "dropped": "store_disabled"}
            else:
                pd = rec.get("post_data")
                # 只在**有请求体**时入池: GET 没有体, 给它一个空引用是噪音 (body_ref 缺席即无体)。
                info = self.forensic.put(pd.encode("utf-8") if isinstance(pd, str) else b"")
                if info.get("size"):
                    rec = dict(rec)
                    rec["body_ref"] = dict(info)
        try:
            self._q.put_nowait(trim_record(rec, max_body=self.max_body))
        except queue.Full:
            with self._lock:
                self._dropped += 1

    def _path_for(self, ts: float) -> Path:
        return self.root / ("flows-" + time.strftime("%Y-%m-%d", time.localtime(ts)) + ".jsonl")

    def _loop(self) -> None:
        while not self._stop.is_set() or not self._q.empty():
            try:
                rec = self._q.get(timeout=0.5)
            except queue.Empty:
                continue
            try:
                self._append(rec)
            except Exception as ex:
                # 写失败不致命 (磁盘满/权限), 但必须留痕, 否则记录会静默消失
                with self._lock:
                    self._last_error = type(ex).__name__ + " " + str(ex)[:140]
            finally:
                self._q.task_done()

    def _append(self, rec: dict) -> None:
        ts = float(rec.get("at") or time.time())
        p = self._path_for(ts)
        line = json.dumps(rec, ensure_ascii=False, separators=(",", ":"))
        if len(line) > MAX_LINE:
            # 超长行会让逐行解析变慢并掩盖真正内容 -> 降级成骨架 + 显式标记, 不静默截字节
            slim = {k: v for k, v in rec.items()
                    if k not in ("req_headers", "resp_headers", "post_data")}
            slim["truncated"] = True
            slim["dropped_fields"] = [k for k in ("req_headers", "resp_headers", "post_data")
                                      if k in rec]
            line = json.dumps(slim, ensure_ascii=False, separators=(",", ":"))
        with self._lock:
            if self._fh is None or self._fh_day != p.name:
                if self._fh is not None:
                    try:
                        self._fh.close()
                    except OSError:
                        pass
                self._fh = open(p, "a", encoding="utf-8")
                self._fh_day = p.name
            self._fh.write(line + chr(10))
            # flush 但不 fsync: 每次请求 fsync 会拖垮整条链; 而进程被强杀时,
            # 未 flush 的内容才是真正会丢的那部分 (这正是"记录看不到"的坑)。
            self._fh.flush()
            self._written += 1

    def flush(self) -> None:
        with self._lock:
            if self._fh is not None:
                try:
                    self._fh.flush()
                except OSError:
                    pass

    def close(self) -> None:
        """drain 队列后收摊。调用方负责调用 (engine.stop 只 flush)。"""
        if not self.enabled:
            return
        self._stop.set()
        if self._t is not None:
            self._t.join(timeout=5.0)
        with self._lock:
            if self._fh is not None:
                try:
                    self._fh.close()
                except OSError:
                    pass
                self._fh = None

    @property
    def written(self) -> int:
        with self._lock:
            return self._written

    @property
    def dropped(self) -> int:
        with self._lock:
            return self._dropped

    def stats(self) -> dict[str, Any]:
        out = {"enabled": self.enabled, "root": str(self.root),
               "written": self.written, "dropped": self.dropped,
               "queued": self._q.qsize(), "error": self._last_error}
        # forensic 段: "完整体到底存了没有"必须能从 stats 一眼看到
        # (本仓教训: 机制"生效了"必须有实测证据, 不能靠调用方自称)。
        if self.forensic is not None:
            out["forensic"] = self.forensic.stats()
        return out

# ── 读 / 查 / 清 ────────────────────────────────────────
def day_files(root: Path) -> list[Path]:
    if not root.is_dir():
        return []
    return sorted(root.glob("flows-*.jsonl"), reverse=True)   # 新的在前


def _day_of(p: Path) -> str:
    return p.name[len("flows-"):-len(".jsonl")]


def iter_flows(root: str | Path, *, days: float | None = None, host: str = "",
               status: int | None = None, q: str = "", kind: str = "",
               limit: int = 0, newest_first: bool = True, offset: int = 0) -> Iterator[dict]:
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
