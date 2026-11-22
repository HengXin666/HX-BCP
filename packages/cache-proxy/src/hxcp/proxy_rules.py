"""TLS decisions for the proxy entry: which hosts are tunnelled and which are decrypted.

Default is tunnel for every host, so the browser keeps its own TLS handshake.
Decryption is opt in per host suffix and lives in <root>/proxy-rules.json so the
console, the CLI and agents edit the same file. The proxy re-reads it on change.
"""

# Agent Note: .agents/notes/implemented/feature/2026-11-22-hxcp-universal-proxy.md
from __future__ import annotations

import json
import os
import threading
import time
from pathlib import Path
from typing import Any

RULES_FILE = "proxy-rules.json"
TUNNEL = "tunnel"
DECRYPT = "decrypt"


def _norm_host(h: str) -> str:
    h = str(h or "").strip().lower()
    if "//" in h:
        h = h.split("//", 1)[1]
    h = h.split("/", 1)[0]
    if h.startswith("[") and "]" in h:
        return h[1:h.index("]")]
    if h.count(":") == 1:
        h = h.split(":", 1)[0]
    return h.lstrip("*.").rstrip(".")


def _suffix_match(host: str, suffixes: list[str]) -> str:
    for s in suffixes:
        if host == s or host.endswith("." + s):
            return s
    return ""


def default_rules() -> dict[str, Any]:
    return {"decrypt": [], "never_decrypt": [], "decrypt_all": False, "updated_at": 0.0}


def clean(doc: dict) -> dict[str, Any]:
    """Normalize a rules document coming from a file or an API body."""
    out = default_rules()
    for k in ("decrypt", "never_decrypt"):
        seen: list[str] = []
        for h in (doc or {}).get(k) or []:
            n = _norm_host(h)
            if n and n not in seen:
                seen.append(n)
        out[k] = sorted(seen)
    out["decrypt_all"] = bool((doc or {}).get("decrypt_all"))
    out["updated_at"] = float((doc or {}).get("updated_at") or 0.0)
    return out


def load(root: str | Path) -> dict[str, Any]:
    try:
        with open(Path(root) / RULES_FILE, encoding="utf-8") as f:
            return clean(json.load(f))
    except Exception:
        return default_rules()


def save(root: str | Path, doc: dict) -> dict[str, Any]:
    out = clean(doc)
    out["updated_at"] = time.time()
    p = Path(root) / RULES_FILE
    p.parent.mkdir(parents=True, exist_ok=True)
    tmp = p.with_name(p.name + "." + str(os.getpid()) + ".tmp")
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, indent=2)
    os.replace(tmp, p)
    return out


def set_host(root: str | Path, host: str, mode: str) -> dict[str, Any]:
    """Move one host suffix to decrypt, tunnel (remove) or never_decrypt."""
    doc = load(root)
    h = _norm_host(host)
    if not h:
        raise ValueError("empty host")
    doc["decrypt"] = [x for x in doc["decrypt"] if x != h]
    doc["never_decrypt"] = [x for x in doc["never_decrypt"] if x != h]
    if mode == DECRYPT:
        doc["decrypt"].append(h)
    elif mode == "never":
        doc["never_decrypt"].append(h)
    elif mode != TUNNEL:
        raise ValueError("mode must be decrypt, tunnel or never")
    return save(root, doc)


def decide(doc: dict, host: str) -> tuple[str, str]:
    """Return (mode, reason) for one host. never_decrypt beats everything else."""
    h = _norm_host(host)
    if not h:
        return TUNNEL, "no_sni"
    m = _suffix_match(h, doc.get("never_decrypt") or [])
    if m:
        return TUNNEL, "never:" + m
    m = _suffix_match(h, doc.get("decrypt") or [])
    if m:
        return DECRYPT, "rule:" + m
    if doc.get("decrypt_all"):
        return DECRYPT, "decrypt_all"
    return TUNNEL, "default"


class LiveRules:
    """Rules bound to a file, re-read at most once per second when its mtime moves."""

    def __init__(self, root: str | Path, overrides: dict | None = None) -> None:
        self.root = Path(root)
        self._overrides = clean(overrides or {}) if overrides else None
        self._doc = load(self.root)
        self._mtime = self._stat()
        self._checked = time.time()
        self._lock = threading.Lock()

    def _stat(self) -> float:
        try:
            return (self.root / RULES_FILE).stat().st_mtime
        except OSError:
            return 0.0

    def current(self) -> dict[str, Any]:
        now = time.time()
        with self._lock:
            if now - self._checked >= 1.0:
                self._checked = now
                mt = self._stat()
                if mt != self._mtime:
                    self._mtime = mt
                    self._doc = load(self.root)
            doc = self._doc
        if self._overrides is None:
            return doc
        merged = dict(doc)
        for k in ("decrypt", "never_decrypt"):
            merged[k] = sorted(set(doc.get(k) or []) | set(self._overrides.get(k) or []))
        merged["decrypt_all"] = bool(doc.get("decrypt_all") or self._overrides.get("decrypt_all"))
        return merged

    def decide(self, host: str) -> tuple[str, str]:
        return decide(self.current(), host)
