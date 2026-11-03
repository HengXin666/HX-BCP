"""人机双通道的准入判定: 人类通道要本机回环连接或本地令牌, Agent 通道要项目名.

# Agent Note: .agents/notes/implemented/architecture/2026-10-21-lane-policy.md

decide(lane, token, project) -> Decision; 判据只在这里, 别处引用它.
"""

from __future__ import annotations

import hashlib
import hmac
import ipaddress
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
HEADER_LANE = "x-bcp-lane"
HEADER_PROJECT = "x-bcp-project"
HEADER_HUMAN_TOKEN = "x-bcp-human-token"
AGENT_GROUP_PREFIX = "agent:"
SESSION_COOKIE = "bcp_human"
SESSION_LABEL = b"bcp-console-session:"


@dataclass
class Decision:
    """一次准入判定的结果, ok=False 时 code 说明原因. """

    ok: bool
    lane: str
    project: str = ""
    code: str = ""
    reason: str = ""
    audit: dict = field(default_factory=dict)


def normalize_project(raw: str) -> str:
    """把项目名归一化成分组键, 不合法返回空串. """
    name = (raw or "").strip().lower().replace("_", "-").replace(" ", "-")
    name = re.sub(r"-+", "-", name).strip("-")
    return name if PROJECT_PATTERN.match(name) else ""


def read_human_token(path: Path | None = None) -> str:
    """读本机人类令牌, 没有就返回空串. """
    file = path or TOKEN_FILE
    try:
        return file.read_text(encoding="utf-8").strip()
    except OSError:
        return ""


def issue_human_token(path: Path | None = None) -> str:
    """生成人类令牌并只写到本机文件 (权限 0600), 返回令牌. """
    file = path or TOKEN_FILE
    file.parent.mkdir(parents=True, exist_ok=True)
    token = secrets.token_urlsafe(TOKEN_BYTES)
    file.write_text(token + "\n", encoding="utf-8")
    file.chmod(0o600)
    return token


def human_token_valid(token: str, path: Path | None = None) -> bool:
    """判断给定令牌是否等于本机的人类令牌, 比较用常量时间. """
    expected = read_human_token(path)
    if not expected or not token:
        return False
    return secrets.compare_digest(token, expected)


def _session_mac(key: str, stamp: str) -> str:
    return hmac.new(key.encode(), SESSION_LABEL + stamp.encode(), hashlib.sha256).hexdigest()


def issue_session(path: Path | None = None, now: float | None = None) -> str:
    """Console session value "<unix>.<hmac>" keyed by the local token, empty when there is no token.

    The value never contains the token; issuing a new token invalidates every session.
    """
    key = read_human_token(path)
    if not key:
        return ""
    stamp = str(int(time.time() if now is None else now))
    return stamp + "." + _session_mac(key, stamp)


def session_valid(value: str, path: Path | None = None, now: float | None = None) -> bool:
    """True when value came from issue_session with the current token and is younger than TOKEN_TTL_S."""
    key = read_human_token(path)
    stamp, _, mac = (value or "").partition(".")
    if not key or not stamp.isdigit() or not mac:
        return False
    age = (time.time() if now is None else now) - int(stamp)
    if age < 0 or age > TOKEN_TTL_S:
        return False
    return secrets.compare_digest(mac, _session_mac(key, stamp))


def same_origin(host: str, origin: str = "", fetch_site: str = "") -> bool:
    """True only for a request a browser sent from a page of this same origin.

    Sec-Fetch-Site wins when present (pages can not forge it). Without it, Origin must equal
    http(s)://host. No browser signal at all means not same origin. Other ports on localhost are
    same-site but not same-origin, so they are refused too.
    """
    if fetch_site:
        return fetch_site == "same-origin"
    return bool(host) and origin in ("http://" + host, "https://" + host)


def is_loopback(peer: str) -> bool:
    """True when peer is a loopback address: 127.0.0.0/8, ::1, or ::ffff:127.x.x.x.

    peer must be the socket peer of the connection (ASGI scope client), never a value taken from
    X-Forwarded-For, X-Real-IP, Forwarded or any other header, those are written by the client.
    """
    try:
        ip = ipaddress.ip_address((peer or "").strip())
    except ValueError:
        return False
    mapped = getattr(ip, "ipv4_mapped", None)
    return ip.is_loopback or bool(mapped and mapped.is_loopback)


def host_is_loopback(host: str) -> bool:
    """True when a Host header names this machine by a loopback name: localhost, 127.x, [::1]."""
    name = (host or "").strip().lower()
    if name.startswith("["):
        name = name[1:].partition("]")[0]
    elif name.count(":") == 1:
        name = name.partition(":")[0]
    return name == "localhost" or is_loopback(name)


def loopback_admitted(peer: str, host: str) -> bool:
    """Local caller rule of the human lane: the socket peer is loopback and Host is a loopback name.

    The peer is the proof: whoever can open a socket to 127.0.0.1 runs on this machine with the
    same reach as HX-BCP itself (it could read ~/.bcp/human.token anyway), so asking it for the
    token proves nothing. Host is only an extra narrowing, never a proof on its own: it refuses
    DNS rebinding, where a web page on evil.example resolves to 127.0.0.1, its fetches reach 8325
    from a loopback peer but carry Host evil.example. A front reverse proxy would make every caller
    look like 127.0.0.1; the server is started with proxy headers off and must stay so, and any new
    front proxy has to change this rule (see the decision note).
    """
    return is_loopback(peer) and host_is_loopback(host)


def decide(lane: str, token: str = "", project: str = "",
           path: Path | None = None, session: str = "", from_self: bool = False,
           loopback: bool = False) -> Decision:
    """判定这次请求属于哪条通道, 是否放行.

    The lane is chosen by the x-bcp-lane header alone, loopback never switches a request into the
    human lane: no header still means agent and still needs a project.
    human: any one of these admits, checked in this order and the first hit is recorded in
    audit["by"]:
      1. loopback: loopback_admitted(peer, host) computed by the caller from the socket peer,
      2. session: a valid console session cookie sent from a same-origin page (from_self),
      3. token: the token header equals the local token.
    They are alternatives, not layers, so the order only decides what audit says.
    """
    want = (lane or AGENT).strip().lower()
    if want == HUMAN:
        by = ("loopback" if loopback else "session" if from_self and session_valid(session, path)
              else "token" if human_token_valid(token, path) else "")
        if not by:
            return Decision(False, HUMAN, code="human_token_required",
                            reason="人类通道需要本机回环连接或本机人类令牌 (packages/lane 的 issue 命令生成)",
                            audit={"lane": HUMAN, "at": time.time()})
        return Decision(True, HUMAN, audit={"lane": HUMAN, "by": by, "at": time.time()})
    name = normalize_project(project)
    if not name:
        return Decision(False, AGENT, code="project_required",
                        reason="Agent 通道必须带项目名 (小写字母/数字/连字符, 2..40)",
                        audit={"lane": AGENT, "project": project, "at": time.time()})
    return Decision(True, AGENT, project=name,
                    audit={"lane": AGENT, "project": name, "at": time.time()})


def group_key(lane: str, project: str = "") -> str:
    """Return the grouping key of a session, human for the human lane, agent:<project> otherwise."""
    if (lane or HUMAN).strip().lower() == AGENT:
        return AGENT_GROUP_PREFIX + normalize_project(project)
    return HUMAN


def may_operate(decision: Decision, env_lane: str, env_project: str = "", write: bool = False) -> bool:
    """Tell whether an admitted request may read or write a session owned by env_lane and env_project.

    agent: only sessions of its own project, read and write.
    human: own lane read and write, agent sessions read only.
    A refused decision may do nothing.
    """
    if not decision.ok:
        return False
    owner = (env_lane or HUMAN).strip().lower()
    if decision.lane == AGENT:
        return owner == AGENT and normalize_project(env_project) == decision.project
    if owner == HUMAN:
        return True
    return not write


def main(argv: list[str] | None = None) -> int:
    '''命令行入口: issue 生成本机人类令牌, check 查看令牌是否存在, 都不回显令牌本身'''
    import argparse

    parser = argparse.ArgumentParser(prog='bcp-lane', description='human token stays in a local file only')
    parser.add_argument('--path', type=Path, default=None, help='token file, default ~/.bcp/human.token')
    sub = parser.add_subparsers(dest='cmd', required=True)
    sub.add_parser('issue', help='generate a new local human token, the old one stops working')
    sub.add_parser('check', help='report whether a local human token exists')
    args = parser.parse_args(argv)
    file = args.path or TOKEN_FILE
    if args.cmd == 'issue':
        issue_human_token(file)
        print(f'issued, file={file}, mode={oct(file.stat().st_mode & 0o777)}')
        return 0
    if read_human_token(file):
        print(f'present, file={file}')
        return 0
    print(f'missing, file={file}, run: bcp-lane issue')
    return 1


if __name__ == '__main__':
    raise SystemExit(main())
