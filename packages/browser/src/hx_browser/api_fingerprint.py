"""指纹自检结论端点

GET /api/v1/fingerprint/verdict
"""

from __future__ import annotations

import json
import logging
import os
from pathlib import Path

from fastapi import APIRouter
from fastapi.responses import JSONResponse

log = logging.getLogger("hx_browser.api_fingerprint")

router = APIRouter(prefix="/api/v1")

LEVELS = ("green", "yellow", "red")


def ok(data=None, msg: str = "success") -> JSONResponse:
    return JSONResponse({"code": 0, "msg": msg, "data": data})


def reports_dir() -> Path:
    """fingerprint-audit 报告目录, 可用 HX_FP_REPORTS_DIR 覆盖."""
    env = os.environ.get("HX_FP_REPORTS_DIR", "")
    if env:
        return Path(env)
    return Path(__file__).resolve().parents[3] / "fingerprint-audit" / "reports"


def unknown(at: str = "") -> dict:
    """没有可用报告时的结论."""
    return {"verdict": "unknown", "checked": 0, "at": at, "evidence": []}


def latest_report(root: Path) -> Path | None:
    """按文件名排序取最新一份 json 报告, 没有时返回 None."""
    if not root.is_dir():
        return None
    files = sorted(p for p in root.iterdir() if p.is_file() and p.suffix == ".json")
    return files[-1] if files else None


def summarize(rep: dict) -> dict:
    """报告 -> {verdict, checked, at, evidence}, verdict 取 verdict.level."""
    v = rep.get("verdict") or {}
    level = str(v.get("level") or "unknown")
    results = rep.get("results") or {}
    return {
        "verdict": level if level in LEVELS else "unknown",
        "checked": len(results) if isinstance(results, dict) else 0,
        "at": str(rep.get("at") or ""),
        "evidence": list(v.get("findings") or []),
    }


def read_verdict(root: Path) -> dict:
    """读最新报告并汇总, 任何读取或解析失败都返回 unknown."""
    try:
        path = latest_report(root)
        if path is None:
            return unknown()
        rep = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(rep, dict):
            return unknown()
        return summarize(rep)
    except Exception as e:  # noqa: BLE001
        log.warning("读指纹自检报告失败: %s", e)
        return unknown()


@router.get("/fingerprint/verdict")
async def fingerprint_verdict():
    return ok(read_verdict(reports_dir()))
