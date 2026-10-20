"""把一个浏览器里的每个 page target 都挂上引擎。

`hxcp run` 以前只连 /json/list 的第一个 page。新开的标签是另一条 CDP 会话,
不会继承那条上的 Fetch.enable。这里订浏览器的 target 事件, 现有页和新建页各一条 Session。
"""

from __future__ import annotations

import json
import threading
import time
import urllib.request

import websocket  # type: ignore

from .cdp import Session
from .engine import Engine


def page_id_from_event(msg: dict) -> str:
    if msg.get("method") not in ("Target.targetCreated", "Target.attachedToTarget"):
        return ""
    info = (msg.get("params") or {}).get("targetInfo") or {}
    if info.get("type") != "page":
        return ""
    return str(info.get("targetId") or "")


def destroyed_id(msg: dict) -> str:
    if msg.get("method") not in ("Target.targetDestroyed", "Target.detachedFromTarget"):
        return ""
    params = msg.get("params") or {}
    return str(params.get("targetId") or "")


class Fleet:
    def __init__(self, endpoint: str, make_engine) -> None:
        self.endpoint = endpoint.rstrip("/")
        self._make = make_engine
        self._slots: dict[str, tuple | None] = {}
        self._dead: set[str] = set()
        self._inflight = 0
        self._lock = threading.Lock()
        self._idle = threading.Condition(self._lock)
        self._stop = False
        self._id = 0
        version = json.loads(
            urllib.request.urlopen(self.endpoint + "/json/version", timeout=8).read()
        )
        self._ws = websocket.create_connection(
            version["webSocketDebuggerUrl"], timeout=120, max_size=8 * 1024 * 1024
        )
        self._reader = threading.Thread(
            target=self._loop, name="hxcp-targets", daemon=True
        )

    def _send(self, method: str, params: dict | None = None) -> None:
        with self._lock:
            self._id += 1
            mid = self._id
        self._ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))

    def start(self) -> None:
        # 先发再起 reader, 避免和 recv 并发写同一条 websocket。
        self._send("Target.setDiscoverTargets", {"discover": True})
        self._reader.start()
        self._sweep()

    def _sweep(self) -> None:
        tabs = json.loads(
            urllib.request.urlopen(self.endpoint + "/json/list", timeout=8).read()
        )
        for tab in tabs:
            if tab.get("type") == "page" and tab.get("id"):
                self._attach(str(tab["id"]))

    def _loop(self) -> None:
        while not self._stop:
            try:
                msg = json.loads(self._ws.recv())
            except websocket.WebSocketTimeoutException:
                continue
            except Exception:
                return
            if msg.get("id") is not None:
                continue
            fresh = page_id_from_event(msg)
            if fresh:
                threading.Thread(
                    target=self._attach, args=(fresh,), daemon=True
                ).start()
                continue
            gone = destroyed_id(msg)
            if gone:
                self._detach(gone)

    def _cancelled(self, target_id: str) -> bool:
        with self._lock:
            return self._stop or target_id in self._dead

    def _attach(self, target_id: str) -> None:
        with self._lock:
            if self._stop or target_id in self._dead or target_id in self._slots:
                return
            self._slots[target_id] = None
            self._inflight += 1
        session: Session | None = None
        engine: Engine | None = None
        try:
            err: Exception | None = None
            for _attempt in range(3):
                if self._cancelled(target_id):
                    break
                try:
                    session = Session(self.endpoint, timeout=120, target_id=target_id)
                    engine = self._make(session)
                    engine.start()
                    err = None
                    break
                except Exception as exc:
                    err = exc
                    self._close_quiet(session)
                    session = None
                    engine = None
                    time.sleep(0.05)
            if engine is None:
                with self._lock:
                    if self._slots.get(target_id) is None:
                        self._slots.pop(target_id, None)
                if err is not None and not self._cancelled(target_id):
                    print("[hxcp] 挂载页面失败 %s: %s" % (target_id, err), flush=True)
                return
            with self._lock:
                if self._stop or target_id in self._dead:
                    self._slots.pop(target_id, None)
                else:
                    self._slots[target_id] = (session, engine)
                    session = None
                    engine = None
            if engine is not None:
                try:
                    engine.stop()
                finally:
                    self._close_quiet(session)
        finally:
            with self._lock:
                self._inflight -= 1
                if self._inflight == 0:
                    self._idle.notify_all()

    def _detach(self, target_id: str) -> None:
        with self._lock:
            self._dead.add(target_id)
            item = self._slots.pop(target_id, None)
        if not item:
            return
        session, engine = item
        try:
            engine.stop()
        finally:
            session.close()

    @staticmethod
    def _close_quiet(session: Session | None) -> None:
        if session is None:
            return
        try:
            session.close()
        except Exception:
            pass

    def stop(self) -> None:
        with self._lock:
            self._stop = True
            ids = [tid for tid, item in self._slots.items() if item]
        threads = []
        for target_id in ids:
            thread = threading.Thread(target=self._detach, args=(target_id,))
            thread.start()
            threads.append(thread)
        with self._lock:
            deadline = time.monotonic() + 5
            while self._inflight and time.monotonic() < deadline:
                self._idle.wait(timeout=0.2)
        for thread in threads:
            thread.join(timeout=5)
        try:
            self._ws.close()
        except Exception:
            pass

    def stats(self) -> dict:
        with self._lock:
            items = [item for item in self._slots.values() if item]
        total: dict = {}
        reasons: dict[str, int] = {}
        for _session, engine in items:
            data = engine.stats.to_dict()
            for key, value in data.items():
                if key == "reasons" and isinstance(value, dict):
                    for reason, count in value.items():
                        reasons[reason] = reasons.get(reason, 0) + int(count)
                elif isinstance(value, (int, float)) and key != "saved_ratio":
                    total[key] = total.get(key, 0) + value
        served = float(total.get("bytes_served") or 0)
        network = float(total.get("bytes_network") or 0)
        total["saved_ratio"] = (
            (served / (served + network)) if (served + network) else 0.0
        )
        total["reasons"] = reasons
        total["pages"] = len(items)
        return total


def open_fleet(endpoint: str, store, rules, **engine_kwargs) -> Fleet:
    def make(session: Session) -> Engine:
        return Engine(session, store, rules, **engine_kwargs)

    fleet = Fleet(endpoint, make)
    fleet.start()
    return fleet
