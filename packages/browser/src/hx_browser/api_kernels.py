"""Kernel management REST: list with source and usage, set a path, set the default, probe one kernel.

Agent Note: .agents/notes/implemented/feature/2026-11-22-safarier-webkit-kernel.md

Judgement (which binary, which family, which features) stays in kernels.py; this file only shapes HTTP.
GET /kernels is open to the agent lane (lane_guard.AGENT_PATHS), every write is human only because its
path is not in AGENT_PATHS.
"""

from __future__ import annotations

import asyncio

from fastapi import APIRouter
from pydantic import BaseModel

from . import display_iso, external, kernels, sandbox
from .api_common import fail, ok
from .config import settings_manager
from .lane_guard import visible
from .store import get_store

router = APIRouter(prefix="/api/v1")
INHERIT = "inherit"
VERSION_TIMEOUT_S = 8


class PathReq(BaseModel):
    path: str = ""


def usage() -> dict[str, int]:
    """Visible, not deleted environments per kernel id; legacy ones without a kernel count as inherit."""
    out: dict[str, int] = {}
    for e in get_store().load_envs():
        if e.deleted_at or not visible(e):
            continue
        key = (e.fingerprint.kernel or "").lower() or INHERIT
        out[key] = out.get(key, 0) + 1
    return out


@router.get("/kernels")
async def list_kernels():
    s = settings_manager.load()
    rows = kernels.list_kernels(s)
    used = usage()
    for r in rows:
        r["envs"] = used.get(r["id"], 0)
    return ok({
        "kernels": rows,
        "inherit": {"envs": used.get(INHERIT, 0), "blink": s.chromium_path, "gecko": s.firefox_path},
        "default": next((r["id"] for r in rows if r.get("default")), ""),
        "sandbox": sandbox.status(),
        "display": display_iso.status(),  # 窗口系统: X11 / Wayland / Xephyr
    })


@router.put("/kernels/{kernel_id}/path")
async def set_kernel_path(kernel_id: str, req: PathReq):
    """Override the executable (or for safarier the CLI command line); an empty path goes back to detection."""
    s = settings_manager.load()
    try:
        kernels.set_path(s, kernel_id, req.path)
    except ValueError as e:
        return fail(str(e), status=404)
    settings_manager.save(s)
    row = next(r for r in kernels.list_kernels(s) if r["id"] == kernels.get(kernel_id).id)
    return ok(row)


@router.put("/kernels/{kernel_id}/default")
async def set_default_kernel(kernel_id: str):
    """Default kernel for new environments; it must be available now."""
    s = settings_manager.load()
    row = next((r for r in kernels.list_kernels(s) if r["id"] == (kernel_id or "").lower()), None)
    if row is None:
        return fail("unknown kernel: " + kernel_id, status=404)
    if not row["available"]:
        return fail("内核不可用, 先设好路径: " + (row["note"] or row["label"]))
    s.default_kernel = row["id"]
    settings_manager.save(s)
    return ok({"default": row["id"]})


async def _binary_version(path: str) -> dict:
    try:
        proc = await asyncio.create_subprocess_exec(path, "--version", stdout=asyncio.subprocess.PIPE,
                                                    stderr=asyncio.subprocess.STDOUT)
        out, _ = await asyncio.wait_for(proc.communicate(), VERSION_TIMEOUT_S)
    except (OSError, asyncio.TimeoutError) as e:
        return {"ok": False, "error": str(e) or "timeout"}
    text = out.decode(errors="replace").strip().splitlines()
    return {"ok": proc.returncode == 0, "version": text[0] if text else "", "error": "" if proc.returncode == 0
            else "exit %s" % proc.returncode}


@router.post("/kernels/{kernel_id}/probe")
async def probe_kernel(kernel_id: str):
    """Run the kernel once: --version for a binary, agent spaces for safarier, build lookup for webkit. Nothing is launched."""
    s = settings_manager.load()
    k = kernels.get(kernel_id)
    if k is None:
        return fail("unknown kernel: " + kernel_id, status=404)
    if external.handles(k.id):
        return ok({"id": k.id, **await external.probe(s, k.id)})
    row = next(r for r in kernels.list_kernels(s) if r["id"] == k.id)
    if not row["available"]:
        return ok({"id": k.id, "ok": False, "error": row["note"]})
    return ok({"id": k.id, **await _binary_version(row["path"])})
