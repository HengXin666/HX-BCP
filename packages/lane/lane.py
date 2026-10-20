"""人机双通道的准入判定: 人类通道要本地令牌, Agent 通道要项目名.

# Agent Note: .agents/notes/implemented/architecture/2026-10-21-lane-policy.md

decide(lane, token, project) -> Decision; 判据只在这里, 别处引用它。
"""

from __future__ import annotations

import re
import secrets
import time
from dataclasses import dataclass, field
from pathlib import Path

HUMAN = "human"
AGENT = "agent"
PROJECT_PATTERN = re.compile(r"^[a-z0-9][a-z0-9-]{1,39}$")
TOKEN_FILE = Path.home() / ".bcp" / "human.token"
TOKEN_TTL_S = 12 * 3600
TOKEN_BYTES = 24


@dataclass
class Decision:
    """一次准入判定的结果, ok=False 时 code 说明原因。"""

    ok: bool
    lane: str
    project: str = ""
    code: str = ""
    reason: str = ""
    audit: dict = field(default_factory=dict)


def normalize_project(raw: str) -> str:
    """把项目名归一化成分组键, 不合法返回空串。"""
    name = (raw or "").strip().lower().replace("_", "-").replace(" ", "-")
    name = re.sub(r"-+", "-", name).strip("-")
    return name if PROJECT_PATTERN.match(name) else ""


def read_human_token(path: Path | None = None) -> str:
    """读本机人类令牌, 没有就返回空串。"""
    file = path or TOKEN_FILE
    try:
        return file.read_text(encoding="utf-8").strip()
    except OSError:
        return ""


def issue_human_token(path: Path | None = None) -> str:
    """生成人类令牌并只写到本机文件 (权限 0600), 返回令牌。"""
    file = path or TOKEN_FILE
    file.parent.mkdir(parents=True, exist_ok=True)
    token = secrets.token_urlsafe(TOKEN_BYTES)
    file.write_text(token + "\n", encoding="utf-8")
    file.chmod(0o600)
    return token


def human_token_valid(token: str, path: Path | None = None) -> bool:
    """判断给定令牌是否等于本机的人类令牌, 比较用常量时间。"""
    expected = read_human_token(path)
    if not expected or not token:
        return False
    return secrets.compare_digest(token, expected)


def decide(lane: str, token: str = "", project: str = "",
           path: Path | None = None) -> Decision:
    """判定这次请求属于哪条通道, 是否放行。"""
    want = (lane or AGENT).strip().lower()
    if want == HUMAN:
        if not human_token_valid(token, path):
            return Decision(False, HUMAN, code="human_token_required",
                            reason="人类通道需要本机人类令牌 (packages/lane 的 issue 命令生成)",
                            audit={"lane": HUMAN, "at": time.time()})
        return Decision(True, HUMAN, audit={"lane": HUMAN, "at": time.time()})
    name = normalize_project(project)
    if not name:
        return Decision(False, AGENT, code="project_required",
                        reason="Agent 通道必须带项目名 (小写字母/数字/连字符, 2..40)",
                        audit={"lane": AGENT, "project": project, "at": time.time()})
    return Decision(True, AGENT, project=name,
                    audit={"lane": AGENT, "project": name, "at": time.time()})
