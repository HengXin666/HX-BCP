"""引擎 — 把「缓存决策」接到「CDP 拦截」上, 并顺带成为流量计量表。

架构 (2026-09-18 实测确定的分工):

  Fetch 域 (Request 阶段)  → 命中就本地 fulfill, 未命中就 continue。
                             命中时**不产生网络流量, 也不产生 TLS 握手**。
  Network 域 (被动监听)     → 拿响应体入缓存 + 计流量。
                             刻意**不在 Response 阶段拦截**: 那样会让页面等我们,
                             一旦慢或错页面就挂。被动监听无阻塞风险, 代价只是首次请求
                             照常走网络 —— 而首次请求本来就要走网络。

即: 用 Fetch 省流量, 用 Network 填缓存, 职责不重叠。

flow 记录 (2026-09-19 新增, flows.py):
  **记录与判定解耦**。被 policy 拒绝缓存的请求 (sensitive_path / set_cookie / no_store)
  照样写 flow —— 拒绝的是"把这份响应喂给别的会话", 不是"不能看"。
  请求头只在 Fetch 阶段拿得到 (Network.requestWillBeSent 没订阅), 响应头只在 Network 阶段
  拿得到, 所以一条完整 flow 由两个阶段各补一半, 用 URL 串起来 (requestId 两套命名空间不通用, 见下)。

⚠️ **两套 requestId 命名空间不同** (2026-09-18 实测踩到的真 bug):
  Fetch  侧: `"12345.67"` (无前缀)
  Network 侧: `"535604.237"` (带 loader 前缀)
  用 Fetch 的 id 去索引 Network 的数据永远取不到 → `_capture` 静默 return,
  表现是 `filled=0 且 skipped=0` (两个计数器都不动 —— 这是"根本没进函数"的信号,
  与"进去了但判据拒绝"不同, 后者会动 skipped)。**配对必须按 URL, 不能按 id。**
"""
from __future__ import annotations

import json
import os
import threading
import time
from dataclasses import dataclass, field
from typing import Any, Callable

from . import policy
from .cdp import Session
from .flows import FlowLog, ForensicStore, forensic_path
from .store import Store, Entry


@dataclass
class Stats:
    paused: int = 0
    hits: int = 0
    misses: int = 0
    passed: int = 0
    filled: int = 0
    skipped: int = 0
    bytes_served: int = 0
    bytes_network: int = 0
    errors: int = 0
    unmatched: int = 0          # Network 侧收到但无对应 Fetch 记录的请求
    reasons: dict[str, int] = field(default_factory=dict)

    def note(self, reason: str) -> None:
        self.reasons[reason] = self.reasons.get(reason, 0) + 1

    def to_dict(self) -> dict[str, Any]:
        d = dict(self.__dict__)
        total = self.bytes_served + self.bytes_network
        d["saved_ratio"] = (self.bytes_served / total) if total else 0.0
        return d


class Engine:
    """一个浏览器会话对应一个引擎。"""

    def __init__(self, session: Session, store: Store, rules: policy.Rules | None = None,
                 *, log: Callable[[str], None] | None = None,
                 on_flow: Callable[[dict], None] | None = None,
                 record_flows: bool = False,
                 flow_dir: str | None = None,
                 flow_max_body: int = 0,
                 forensic: bool = False,
                 forensic_dir: str | None = None,
                 forensic_max_bytes: int = 0,
                 forensic_free_bytes: int = 0) -> None:
        self.s = session
        self.store = store
        self.rules = rules or policy.Rules()
        self.stats = Stats()
        self._log = log or (lambda m: None)
        self.on_flow = on_flow
        # flow 落盘。默认关闭 —— 记录是观测行为, 不该在没人要日志时默默长文件。
        # 目录默认放 store 根下的 flows/ (与缓存同源, 清理时一起看得见)。
        _root = getattr(self.store, "root", None)
        _fdir = flow_dir or (os.path.join(str(_root), "flows") if _root else "")
        # 取证档: 只有**同时**开了 record_flows 才有意义 —— 请求体引用要写在 JSONL 记录里,
        # 没有记录就没有引用, 池里的字节等于孤儿。
        self.forensic = ForensicStore(
            forensic_dir or (str(forensic_path(_root)) if _root else ""),
            enabled=bool(forensic and record_flows and _fdir),
            **({"max_bytes": int(forensic_max_bytes)} if forensic_max_bytes else {}),
            **({"min_free_bytes": int(forensic_free_bytes)} if forensic_free_bytes else {}))
        self.flows = FlowLog(_fdir, enabled=bool(record_flows and _fdir),
                             forensic=self.forensic if forensic else None,
                             **({"max_body": int(flow_max_body)} if flow_max_body else {}))
        # 按 **URL** 索引 (两套 requestId 不通用)。同一 URL 并发命中时保留最新的那本。
        self._by_url: dict[str, dict] = {}
        self._served_keys: set[str] = set()   # 已被本地满足的 URL, 避免重复填充
        # Fetch 阶段抓到的请求头, 等 Network 阶段配对 (两条流各有一半信息)。
        # 上限兜底: 被 continue 但从未收到响应的请求 (取消/失败) 会留在这里, 不设上限就是泄漏。
        self._pending_req: dict[str, dict] = {}
        self._workers: list[threading.Thread] = []
        self.s.on_event = self._on_event
        self._lock = threading.Lock()

    # ── 装配 ──────────────────────────────────────────────
    def start(self) -> None:
        self.s.send("Network.enable", {"maxTotalBufferSize": 64 * 1024 * 1024,
                                       "maxResourceBufferSize": 16 * 1024 * 1024})
        self.s.enable_fetch([{"urlPattern": "*", "requestStage": "Request"}])
        self._log("[hxcp] engine started (fetch=request-stage, network=passive)")
        self._publish()

    def stop(self) -> None:
        try:
            self.s.disable_fetch()
        except Exception:
            pass
        # flow 收尾必须显式 drain: 引擎退出与 FlowLog 的写线程是两条命,
        # 不 close 就会丢掉队列里最后一批记录 —— 而"最后那批"往往正是要抓的那次。
        try:
            self.flows.close()
        except Exception:
            pass
        self._publish(state="stopped")

    # ── 运行态发布 ────────────────────────────────────────
    def _publish(self, state: str = "running") -> None:
        """把运行态写进 store 目录下的 runtime.json。

        为什么不用进程内全局: API / MCP 与引擎**不在同一个进程**
        (引擎挂在浏览器旁的脚本里, API 是个长驻 HTTP 服务)。
        实测症状: `/stats` 的 `engine` 恒为 null, 面板上看到的永远是空的观测面。
        落盘是这个约束下最简单的正解 —— 且任何进程都能读, 包括 `hxcp cache` 与 MCP。
        失败不致命 (磁盘只读/权限), 所以吞异常。
        """
        try:
            root = getattr(self.store, "root", None)
            if root is None:
                return
            payload = {"state": state, "updated_at": time.time(),
                       "pid": __import__("os").getpid(), "stats": self.stats.to_dict(),
                       "flows": self._flow_stats()}
            p = os.path.join(str(root), "runtime.json")
            tmp = p + ".tmp"
            with open(tmp, "w", encoding="utf-8") as f:
                json.dump(payload, f, ensure_ascii=False)
            os.replace(tmp, p)
        except Exception:
            pass

    def tick(self) -> None:
        """由宿主周期调用 (或每次命中后), 刷新运行态。"""
        self._publish()

    # ── 运行态节流发布 ────────────────────────────────────
    # 2026-09-18 实测缺陷: _publish() 只在 start()/stop() 调用, 运行期间从不更新,
    # 于是 /stats 的 engine 计数器**永远是启动瞬间的快照 (全 0)** —— 实际缓存在正常
    # 命中 (store.hits 在涨), 但观测面上看不到任何引擎计数。这就是 README 记的
    # "面板上看到的永远是空观测面" 的根因 (此前只归因于跨进程, 其实还差这一环)。
    # 修法: 关键计数事件后节流发布 (>=1s 一次), 避免每个请求都写盘。
    _PUBLISH_MIN_INTERVAL_S = 1.0

    def _publish_throttled(self) -> None:
        now = time.time()
        last = getattr(self, "_last_publish_at", 0.0)
        if now - last < self._PUBLISH_MIN_INTERVAL_S:
            return
        self._last_publish_at = now
        self._publish()

    # ── 事件分发 ──────────────────────────────────────────
    def _on_event(self, msg: dict) -> None:
        m = msg.get("method")
        if m == "Fetch.requestPaused":
            self._on_paused(msg["params"])
        elif m == "Network.responseReceived":
            p = msg["params"]
            r = p.get("response", {}) or {}
            url = r.get("url", "")
            with self._lock:
                self._by_url[url] = {
                    "rid": p["requestId"], "status": int(r.get("status", 0) or 0),
                    "headers": {str(k): str(v) for k, v in (r.get("headers") or {}).items()},
                    "mime": r.get("mimeType", ""), "t": time.time(),
                    # 请求头按 URL 留给 _emit 配对; 此处 peek 不 pop,
                    # 否则命中/跳过分支的 _emit 就取不到了。
                    "req": None,
                }
        elif m == "Network.loadingFinished":
            p = msg["params"]
            enc = int(p.get("encodedDataLength", 0) or 0)
            self.stats.bytes_network += enc
            self._schedule_capture(p["requestId"], enc)

    # ── flow 记录 (观测面, 与缓存判定解耦) ─────────────────
    # 为什么请求头必须在这里取: Network.requestWillBeSent 需要额外订阅一个域,
    # 而 Fetch.requestPaused 的参数里**本来就带 request.headers** —— 同一个请求已经
    # 从我们手里过了一遍, 再订阅一遍等于同一事实两处判据 (本仓栽过)。
    _PENDING_MAX = 512

    def _note_request(self, url: str, req: dict, method: str, p: dict) -> None:
        if not self.flows.enabled:
            return
        with self._lock:
            if len(self._pending_req) >= self._PENDING_MAX:
                # 丢最旧的一条: 新请求比旧请求更可能拿到响应对上
                self._pending_req.pop(next(iter(self._pending_req)), None)
            self._pending_req[url] = {
                "method": method,
                "req_headers": dict(req.get("headers") or {}),
                "post_data": req.get("postData") or "",
                "resource_type": p.get("resourceType") or "",
                "fetch_rid": p.get("requestId") or "",
            }

    def _flow_stats(self) -> dict:
        """flow 计数并入引擎运行态 —— 否则"有没有在记"只能在文件系统里找。"""
        try:
            return self.flows.stats()
        except Exception:
            return {"enabled": False}

    # ── Request 阶段: 命中即本地满足 ───────────────────────
    def _on_paused(self, p: dict) -> None:
        rid = p["requestId"]
        req = p.get("request", {}) or {}
        url = req.get("url", "")
        method = (req.get("method") or "GET").upper()
        self.stats.paused += 1
        self._note_request(url, req, method, p)

        if method != "GET" or not self.rules.may_handle(url):
            self.stats.passed += 1
            self.s.continue_request(rid)
            self._publish_throttled()
            return

        entry = self.store.lookup(url, "GET")
        if entry is not None and entry.is_fresh():
            body = self.store.get_blob(entry.body_hash)
            if body is not None:
                self.store.record_hit(entry.key)
                self.stats.hits += 1
                self.stats.bytes_served += len(body)
                self.stats.note("hit")
                self._publish_throttled()
                with self._lock:
                    self._served_keys.add(url)
                headers = [(h[0], h[1]) for h in entry.headers
                           if h[0].lower() not in ("content-length", "content-encoding")]
                headers.append(("x-hx-cache", "HIT-SEED" if entry.source == "seed" else "HIT"))
                self.s.fulfill(rid, body, status=entry.status or 200,
                               headers=[{"name": a, "value": b} for a, b in headers])
                self._emit(url, entry.status or 200,
                           {str(h[0]): str(h[1]) for h in entry.headers}, "hit",
                           size=len(body), req=p, verdict_kind="hit", key=entry.key)
                return

        self.stats.misses += 1
        self.stats.note("miss")
        # 陈旧但有 etag: 带条件头回源, 省掉 body
        if entry is not None and entry.etag:
            self.s.continue_request(rid, headers=[{"name": "if-none-match", "value": entry.etag}])
        else:
            self.s.continue_request(rid)

    # ── 捕获: 被动取响应体入缓存 ───────────────────────────
    def _schedule_capture(self, net_rid: str, encoded: int) -> None:
        with self._lock:
            candidates = [u for u, m in self._by_url.items() if m.get("rid") == net_rid]
            if not candidates:
                self.stats.unmatched += 1
                return
            url = candidates[0]
            meta = self._by_url.pop(url)
            served = url in self._served_keys
            if served:
                self._served_keys.discard(url)
        if served:
            return
        meta["encoded"] = encoded
        t = threading.Thread(target=self._capture, args=(url, meta), daemon=True)
        self._workers.append(t)
        t.start()

    def _capture(self, url: str, meta: dict) -> None:
        """在**工作线程**里跑: getResponseBody 要等响应, 在 reader 回调里等就是自己等自己。"""
        try:
            status = int(meta.get("status", 0) or 0)
            headers = dict(meta.get("headers") or {})
            encoded = int(meta.get("encoded", 0) or 0)
            if not self.rules.may_handle(url):
                self.stats.skipped += 1
                self.stats.note("host_filtered")
                # 原实现此处直接 return, 连 _emit 都没调 —— 被 host 规则排除的请求
                # 在观测面上完全不存在。记录与判定解耦后必须补上。
                self._emit(url, status, headers, "skip:host_filtered", meta=meta)
                return
            dec = policy.decide(url, status, headers, size=encoded, max_bytes=self.rules.max_bytes)
            if not dec:
                self.stats.skipped += 1
                self.stats.note(dec.reason or "skip")
                self._emit(url, status, headers, "skip:" + (dec.reason or "?"), meta=meta)
                return
            r = self.s.send("Network.getResponseBody", {"requestId": meta["rid"]}, timeout=30)
            res = r.get("result", {}) or {}
            body_txt = res.get("body", "")
            body = (body_txt.encode("latin-1", "ignore") if res.get("base64Encoded")
                    else body_txt.encode("utf-8", "ignore"))
            if not body:
                self.stats.skipped += 1
                self.stats.note("empty_body")
                self._emit(url, status, headers, "skip:empty_body", meta=meta)
                return
            etag = ""
            for k, v in headers.items():
                if k.lower() in ("etag", "last-modified"):
                    etag = v
                    break
            e = self.store.put(url, "GET", status, [[k, v] for k, v in headers.items()],
                               body, ttl=dec.ttl, etag=etag, source="net", tags=["auto"])
            if e is None:
                self.stats.skipped += 1
                self.stats.note("too_large")
                self._emit(url, status, headers, "skip:too_large", meta=meta)
                return
            self.stats.filled += 1
            self.stats.note("fill:" + dec.reason)
            self._emit(url, status, headers, "fill:" + dec.reason, size=len(body), meta=meta)
        except Exception as ex:  # noqa: BLE001
            self.stats.errors += 1
            self._log("[hxcp] capture error: " + type(ex).__name__ + " " + str(ex)[:140])

    def _emit(self, url: str, status: int, headers: dict, verdict: str, size: int = 0,
              *, meta: dict | None = None, req: dict | None = None,
              verdict_kind: str = "flow", key: str = "") -> None:
        """发一条 flow (回调 + 落盘)。

        req: 命中分支直接传 Fetch 的 paused 参数; 其余走 _pending_req (按 URL 配对)。
        """
        if not self.flows.enabled and not self.on_flow:
            return
        with self._lock:
            rq = self._pending_req.pop(url, None)
        if req is not None:
            rq = {"method": (req.get("request", {}) or {}).get("method") or "GET",
                  "req_headers": dict((req.get("request", {}) or {}).get("headers") or {}),
                  "post_data": (req.get("request", {}) or {}).get("postData") or "",
                  "resource_type": req.get("resourceType") or "",
                  "fetch_rid": req.get("requestId") or ""}
        rq = rq or {}
        rec = {"kind": verdict_kind if verdict_kind == "hit" else "flow",
               "verdict": verdict, "url": url, "status": int(status or 0),
               "size": int(size or 0), "type": policy.content_type(headers or {}),
               "method": rq.get("method") or "GET",
               "resource_type": rq.get("resource_type") or "",
               "req_headers": rq.get("req_headers") or {},
               "resp_headers": dict(headers or {}),
               "at": time.time()}
        if rq.get("post_data"):
            rec["post_data"] = rq["post_data"]
        if key:
            rec["key"] = key
        m = meta or {}
        if m.get("encoded") is not None:
            rec["encoded"] = int(m.get("encoded") or 0)
        if m.get("mime"):
            rec["mime"] = m["mime"]
        if m.get("t"):
            rec["resp_at"] = float(m["t"])
        self.flows.write(rec)
        if self.on_flow:
            self.on_flow(rec)
