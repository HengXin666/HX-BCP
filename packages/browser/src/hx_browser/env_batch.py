"""Batch operations on existing browsers, served by POST /environments/batch when the body has "action".

Body: {"action": "start"|"stop"|"move"|"tags"|"delete", "ids": [...], "folder": str (move), "tags": [...] (tags, merged
into existing tags)}. Without "action" the same route keeps its old meaning, batch create (count, base).
Each id runs through the single-browser handler, so lane ownership, proxy pre-check, trash semantics and logs stay
identical to one-by-one calls; ids the caller may not write are refused per item, never silently skipped.
Reply: {"action", "done", "failed": [{"id", "error"}], "results": [{"id", "ok", "error"}]}, at most 4 ids in flight.
"""

from __future__ import annotations

import asyncio
import json

from fastapi import HTTPException

from . import lane_guard
from .api_common import fail, ok, store

ACTIONS = {"start", "stop", "move", "tags", "delete"}
MAX_IDS = 200
PARALLEL = 4


def _outcome(resp) -> tuple[bool, str]:
    """(ok, error) from a handler's JSONResponse envelope."""
    try:
        body = json.loads(resp.body)
    except Exception:  # noqa: BLE001
        return True, ""
    return body.get("code") == 0, "" if body.get("code") == 0 else str(body.get("msg") or "失败")


async def run(payload: dict):
    from . import api_env_ops as ops  # late import: api_env_ops imports this module

    action = str(payload.get("action") or "")
    ids = [str(i) for i in (payload.get("ids") or []) if i]
    if action not in ACTIONS:
        return fail("action 只能是 " + ", ".join(sorted(ACTIONS)))
    if not ids or len(ids) > MAX_IDS:
        return fail(f"ids 需要 1 到 {MAX_IDS} 个")
    folder = str(payload.get("folder") or "")
    tags = [str(t) for t in (payload.get("tags") or []) if str(t).strip()]
    if action == "tags" and not tags:
        return fail("tags 不能为空")
    s = store()
    gate = asyncio.Semaphore(PARALLEL)

    async def one(env_id: str) -> dict:
        env = s.get_env(env_id)
        if not env:
            return {"id": env_id, "ok": False, "error": "环境不存在"}
        if not lane_guard.writable(env):
            return {"id": env_id, "ok": False, "error": "由 api 或 mcp 管理, 不能在这里改"}
        async with gate:
            try:
                if action == "start":
                    resp = await ops.start_env(env_id)
                elif action == "stop":
                    resp = await ops.stop_env(env_id)
                elif action == "delete":
                    resp = await ops.delete_env(env_id, False)
                elif action == "move":
                    resp = await ops.update_env(env_id, {"folder": folder})
                else:
                    resp = await ops.update_env(env_id, {"tags": list(dict.fromkeys([*(env.tags or []), *tags]))})
            except HTTPException as e:
                return {"id": env_id, "ok": False, "error": str(e.detail)}
            except Exception as e:  # noqa: BLE001
                return {"id": env_id, "ok": False, "error": str(e)}
        good, err = _outcome(resp)
        return {"id": env_id, "ok": good, "error": err}

    results = await asyncio.gather(*(one(i) for i in ids))
    failed = [{"id": r["id"], "error": r["error"]} for r in results if not r["ok"]]
    s.add_log("env.batch-" + action, {"count": len(ids), "failed": len(failed)})
    return ok({"action": action, "done": len(ids) - len(failed), "failed": failed, "results": results})
