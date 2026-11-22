"""Per-instance runtime state shared through the cache directory.

Several producers can share one cache root: CDP engines (one per browser env)
and proxy listeners. Each one writes its own runtime/<instance>.json, readers
aggregate them. The legacy single runtime.json is still written so older
readers keep working.
"""

# Agent Note: .agents/notes/implemented/feature/2026-11-22-hxcp-universal-proxy.md
from __future__ import annotations

import json
import os
import re
import time
from pathlib import Path
from typing import Any

STALE_AFTER_S = 120.0
PRUNE_AFTER_S = 86400.0
_SAFE = re.compile(r"[^A-Za-z0-9_.-]+")

# Counters that are summed when several instances run at once.
SUM_KEYS = ("paused", "hits", "misses", "passed", "filled", "skipped", "bytes_served",
            "bytes_network", "errors", "unmatched", "tunnels", "decrypted")


def safe_name(name: str) -> str:
    """Instance names become file names, keep them boring."""
    out = _SAFE.sub("-", str(name or "")).strip("-.")
    return out[:80] or "default"


def _atomic_write(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + "." + str(os.getpid()) + ".tmp")
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(payload, f, ensure_ascii=False)
    os.replace(tmp, path)


def publish(root: str | Path, instance: str, payload: dict) -> None:
    """Write one instance state, never raises (observation must not hurt traffic)."""
    try:
        root = Path(root)
        body = dict(payload)
        body.setdefault("updated_at", time.time())
        body.setdefault("pid", os.getpid())
        body["instance"] = instance
        _atomic_write(root / "runtime" / (safe_name(instance) + ".json"), body)
        _atomic_write(root / "runtime.json", body)
    except Exception:
        pass


def _load(p: Path) -> dict | None:
    try:
        with open(p, encoding="utf-8") as f:
            d = json.load(f)
    except Exception:
        return None
    if not isinstance(d, dict):
        return None
    if d.get("state") == "running" and time.time() - float(d.get("updated_at", 0)) > STALE_AFTER_S:
        d["state"] = "stale"
    return d


def instances(root: str | Path) -> list[dict]:
    """All instance states, newest first."""
    rdir = Path(root) / "runtime"
    rows: list[dict] = []
    if rdir.is_dir():
        now = time.time()
        for p in rdir.glob("*.json"):
            d = _load(p)
            if d is not None and d.get("state") != "running" \
                    and now - float(d.get("updated_at") or 0) > PRUNE_AFTER_S:
                try:
                    p.unlink()
                except OSError:
                    pass
                continue
            if d is not None:
                d.setdefault("instance", p.stem)
                rows.append(d)
    if not rows:
        legacy = _load(Path(root) / "runtime.json")
        if legacy is not None:
            legacy.setdefault("instance", "default")
            rows.append(legacy)
    rows.sort(key=lambda d: -float(d.get("updated_at") or 0))
    return rows


def aggregate(root: str | Path) -> dict[str, Any] | None:
    """One flat view: running if any instance runs, counters summed over running ones."""
    rows = instances(root)
    if not rows:
        return None
    live = [d for d in rows if d.get("state") == "running"]
    pick = live or rows[:1]
    stats: dict[str, Any] = {}
    reasons: dict[str, int] = {}
    for d in pick:
        s = d.get("stats") or {}
        for k in SUM_KEYS:
            if k in s:
                stats[k] = int(stats.get(k, 0)) + int(s.get(k) or 0)
        for k, v in (s.get("reasons") or {}).items():
            reasons[k] = reasons.get(k, 0) + int(v or 0)
    stats["reasons"] = reasons
    total = int(stats.get("bytes_served", 0)) + int(stats.get("bytes_network", 0))
    stats["saved_ratio"] = (int(stats.get("bytes_served", 0)) / total) if total else 0.0
    state = "running" if live else str(pick[0].get("state") or "absent")
    return {"state": state, "updated_at": max(float(d.get("updated_at") or 0) for d in rows),
            "stats": stats, "flows": (pick[0].get("flows") or {}),
            "instances": len(rows), "running": len(live)}


def forget(root: str | Path, instance: str) -> None:
    try:
        (Path(root) / "runtime" / (safe_name(instance) + ".json")).unlink()
    except OSError:
        pass
