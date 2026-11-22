"""Checker runs against one running environment, the same tools the fingerprint-score workflow runs in CI.

Agent Note: .agents/notes/implemented/feature/2026-11-22-console-site-checks.md

Two kinds, each a subprocess against the environment's CDP endpoint:
    sites   gate/probe.py captures the public checker pages, gate/score.ts scores them (RESULT line)
    audit   fingerprint-audit/audit.py consistency rules, its report also feeds GET /fingerprint/verdict
This module owns run state and result files only; scoring stays in the gate and audit code. Results go to
<data_dir>/site-checks/<env_id>-<kind>.json, one run per environment and kind at a time. Only Chromium
family environments expose CDP, other kernels get a refusal that says so.
"""

from __future__ import annotations

import asyncio
import importlib.util
import json
import os
import shutil
import sys
import time
from pathlib import Path

from fastapi import APIRouter

from . import kernels
from .api_common import fail, ok
from .config import settings_manager
from .runtime import get_runtime
from .store import get_store

router = APIRouter(prefix="/api/v1")
KINDS = ("sites", "audit")
RUN_TIMEOUT_S = {"sites": 420, "audit": 600}
PACKAGES = Path(__file__).resolve().parents[3]
_TASKS: dict[str, asyncio.Task] = {}


def gate_dir() -> Path:
    return Path(os.environ.get("HX_GATE_DIR", "") or PACKAGES / "browser" / "gate")


def audit_dir() -> Path:
    return Path(os.environ.get("HX_FP_AUDIT_DIR", "") or PACKAGES / "fingerprint-audit")


def results_dir() -> Path:
    s = settings_manager.load()
    return Path(s.data_dir) / "site-checks"


def result_path(env_id: str, kind: str) -> Path:
    return results_dir() / ("%s-%s.json" % (env_id, kind))


def python_for(module: str, project: Path | None = None) -> list[str]:
    """This interpreter when it can import module, else uv run with it, else empty."""
    if importlib.util.find_spec(module) is not None:
        return [sys.executable]
    if shutil.which("uv"):
        if project is not None:
            return ["uv", "run", "--project", str(project), "python"]
        return ["uv", "run", "--with", module, "python"]
    return []


def tools(kind: str) -> tuple[list[str], str]:
    """Command prefix for kind and the reason it is missing, one of the two is empty."""
    if kind == "sites":
        py = python_for("playwright")
        if not py:
            return [], "需要 playwright (uv 或当前 Python 环境)"
        if not shutil.which("node"):
            return [], "评分需要 node 22+ (node --experimental-strip-types)"
        return py, ""
    py = python_for("websocket", audit_dir())
    return (py, "") if py else ([], "需要 websocket-client 或 uv")


def read(env_id: str, kind: str) -> dict:
    """Last stored state of env and kind, idle when never run; running wins while a task is alive."""
    path = result_path(env_id, kind)
    state = {"kind": kind, "state": "idle"}
    if path.exists():
        try:
            state = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            pass
    task = _TASKS.get(env_id + ":" + kind)
    if task is not None and not task.done():
        state["state"] = "running"
    elif state.get("state") == "running":  # the service restarted under a run, nothing will finish it
        state.update(state="failed", error="服务重启, 这次检测中断了")
    return state


def write(env_id: str, kind: str, data: dict) -> None:
    path = result_path(env_id, kind)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")


async def _exec(argv: list[str], cwd: Path, timeout: int) -> tuple[int, str]:
    proc = await asyncio.create_subprocess_exec(*argv, cwd=str(cwd), stdout=asyncio.subprocess.PIPE,
                                                stderr=asyncio.subprocess.STDOUT)
    try:
        out, _ = await asyncio.wait_for(proc.communicate(), timeout)
    except asyncio.TimeoutError:
        proc.kill()
        return -1, "超时 (%ss)" % timeout
    return proc.returncode or 0, out.decode(errors="replace")


def parse_result(text: str) -> dict | None:
    """The RESULT json line score.ts prints last, None when absent."""
    for line in reversed(text.splitlines()):
        if line.startswith("RESULT "):
            try:
                return json.loads(line[len("RESULT "):])
            except ValueError:
                return None
    return None


async def run_sites(env_id: str, cdp: str, py: list[str]) -> dict:
    out_dir = results_dir()
    capture = out_dir / ("%s-sites-capture.json" % env_id)
    code, log = await _exec(py + [str(gate_dir() / "probe.py"), "--cdp", cdp, "--out", str(capture)],
                            gate_dir(), RUN_TIMEOUT_S["sites"])
    if code != 0 or not capture.exists():
        return {"state": "failed", "error": "检测站采集失败: " + log.strip()[-600:]}
    svg = out_dir / ("%s-sites.svg" % env_id)
    code, log = await _exec(["node", "--experimental-strip-types", str(gate_dir() / "score.ts"), "--no-fail",
                             str(capture), str(svg)], gate_dir(), 60)
    res = parse_result(log)
    if res is None:
        return {"state": "failed", "error": "评分失败: " + log.strip()[-600:]}
    return {"state": "done", **res}


def _latest_report(label: str) -> dict | None:
    found = sorted((audit_dir() / "reports").glob("*-" + label + ".json"))
    if not found:
        return None
    return json.loads(found[-1].read_text(encoding="utf-8"))


async def run_audit(env_id: str, cdp: str, py: list[str]) -> dict:
    label = "env-%s-%d" % (env_id[:8], int(time.time()))
    code, log = await _exec(py + ["audit.py", "--endpoint", cdp, "--expect-tz", "auto", "--label", label],
                            audit_dir(), RUN_TIMEOUT_S["audit"])
    rep = _latest_report(label)
    if rep is None:
        return {"state": "failed", "error": ("环境没有评分 (exit 3)" if code == 3 else "自检没有产出报告: ")
                + log.strip()[-600:]}
    v = rep.get("verdict") or {}
    rows = [{"name": f.get("rule", ""), "verdict": f.get("level", ""), "headline": f.get("detail", ""),
             "fix": f.get("fix", "")} for f in v.get("findings") or []]
    return {"state": "done", "verdict": v.get("level", "unknown"), "rows": rows, "signals": v.get("signals") or {}}


async def _run(env_id: str, kind: str, cdp: str, py: list[str]) -> None:
    started = int(time.time() * 1000)
    write(env_id, kind, {"kind": kind, "state": "running", "started_at": started})
    try:
        res = await (run_sites if kind == "sites" else run_audit)(env_id, cdp, py)
    except Exception as e:  # noqa: BLE001 the stored state must leave running whatever happened
        res = {"state": "failed", "error": "%s: %s" % (type(e).__name__, e)}
    write(env_id, kind, {"kind": kind, "started_at": started, "finished_at": int(time.time() * 1000), **res})


def refusal(env_id: str, kind: str) -> tuple[str, str]:
    """(cdp endpoint, "") when kind can run on env now, else ("", reason)."""
    if kind not in KINDS:
        return "", "未知检测: " + kind
    env = get_store().get_env(env_id)
    if env is None:
        return "", "环境不存在"
    if kernels.family_of(env) != kernels.BLINK:
        return "", "检测需要 CDP, 只支持 Chromium 系内核"
    rt = get_runtime(env_id)
    if rt is None or not rt.debug_port:
        return "", "环境未运行, 先启动再检测"
    return rt.cdp_http or "http://127.0.0.1:%d" % rt.debug_port, ""


@router.get("/environments/{env_id}/site-checks")
async def list_checks(env_id: str):
    return ok({k: read(env_id, k) for k in KINDS})


@router.post("/environments/{env_id}/site-checks/{kind}")
async def start_check(env_id: str, kind: str):
    """Start one run in the background; a run already going for this env and kind is returned as is."""
    cdp, why = refusal(env_id, kind)
    if why:
        return fail(why)
    key = env_id + ":" + kind
    if key in _TASKS and not _TASKS[key].done():
        return ok(read(env_id, kind))
    py, why = tools(kind)
    if why:
        return fail(why)
    _TASKS[key] = asyncio.create_task(_run(env_id, kind, cdp, py))
    await asyncio.sleep(0)
    return ok(read(env_id, kind))
