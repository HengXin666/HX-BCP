"""Cookie 解析与注入 —— JSON / Netscape / 原始串三种格式 (2026-10-13 从 browser.py 拆出)。

Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md

## 为什么拆出来

browser.py 已到 659 行 (棘轮登记在册, 只许降不许涨), 而本次要加「可见窗口 +
多实例并发」—— 逻辑塞进去必然破 300 行上限。拆的边界按职责取: **cookie 的解析与
落盘**与「浏览器进程怎么起/怎么停」是两件事, 且 cookie 这块是**自包含**的
(输入 raw 字符串, 输出 sqlite 写入), 不碰 RUNTIME / _PROCS。

判据与实现**原样搬移, 未改一个字节** —— 这次只是搬家, 不是重写。任何行为改动
都必须单独论证, 免得把「重构」当成「顺手修 bug」的掩护。

调用方: browser.py (_post_start / _launch_firefox) 与 api.py (cookies 端点)。
"""

from __future__ import annotations

import json
import logging
import sqlite3
import time
from pathlib import Path

from .models import Environment

log = logging.getLogger("hx_browser.cookies")


# ---------- Cookie 解析 ----------
def parse_cookies(raw: str) -> list[dict]:
    """解析 JSON / Netscape / 原始字符串三种格式。"""
    raw = (raw or "").strip()
    if not raw:
        return []
    # JSON
    try:
        parsed = json.loads(raw)
        if isinstance(parsed, list):
            return [
                {
                    "name": c.get("name") or c.get("Name") or "",
                    "value": c.get("value") or c.get("Value") or "",
                    "domain": c.get("domain") or c.get("Domain") or "",
                    "path": c.get("path") or c.get("Path") or "/",
                    "secure": bool(c.get("secure", c.get("Secure", False))),
                    "httpOnly": bool(c.get("httpOnly", c.get("HttpOnly", False))),
                    "expires": c.get("expirationDate")
                    or c.get("expires")
                    or c.get("Expires")
                    or 0,
                    "sameSite": c.get("sameSite", ""),
                }
                for c in parsed
                if (c.get("name") or c.get("Name"))
                and (c.get("domain") or c.get("Domain"))
            ]
    except Exception:
        pass
    # Netscape
    if "\t" in raw and any(w in raw for w in ("TRUE", "FALSE")):
        cookies = []
        for line in raw.splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            parts = line.split("\t")
            if len(parts) < 7 or not parts[5]:
                continue
            cookies.append(
                {
                    "name": parts[5],
                    "value": parts[6],
                    "domain": parts[0],
                    "path": parts[2] or "/",
                    "secure": parts[3] == "TRUE",
                    "httpOnly": False,
                    "expires": int(parts[4]) if parts[4].isdigit() else 0,
                    "sameSite": "",
                }
            )
        return cookies
    # 原始字符串 name=value; name2=value2
    cookies = []
    for pair in raw.split(";"):
        pair = pair.strip()
        if "=" in pair:
            name, value = pair.split("=", 1)
            cookies.append(
                {
                    "name": name.strip(),
                    "value": value.strip(),
                    "domain": "",
                    "path": "/",
                    "secure": False,
                    "httpOnly": False,
                    "sameSite": "",
                }
            )
    return cookies


def _inject_firefox_cookies(env: Environment, profile_dir: Path) -> None:
    """Firefox：cookies.sqlite 直写（CDP 兼容性弱，sqlite 更稳）。需环境未运行。"""
    cookies = parse_cookies(env.account.cookies)
    if not cookies:
        return
    db_path = profile_dir / "cookies.sqlite"
    try:
        conn = sqlite3.connect(str(db_path))
        cur = conn.cursor()
        # 探测 schema
        cur.execute("PRAGMA table_info(moz_cookies)")
        cols = [row[1] for row in cur.fetchall()]
        now_us = int(time.time() * 1_000_000)
        for c in cookies:
            host = c.get("domain", "").lstrip(".")
            if not host:
                continue
            if "originAttributes" in cols:
                cur.execute(
                    "INSERT OR REPLACE INTO moz_cookies "
                    "(originAttributes, name, value, host, path, expiry, lastAccessed, creationTime, isSecure, isHttpOnly, inBrowserElement, sameSite, rawSameSite, schemeMap, isPartitioned) "
                    "VALUES (?,?,?,?,?,?,?,?,?,?,0,0,0,?,0)",
                    (
                        "",
                        c["name"],
                        c["value"],
                        host,
                        c.get("path", "/"),
                        int(c.get("expires") or 0),
                        now_us,
                        now_us,
                        1 if c.get("secure") else 0,
                        1 if c.get("httpOnly") else 0,
                        2,  # schemeMap: https
                    ),
                )
            else:
                cur.execute(
                    "INSERT OR REPLACE INTO moz_cookies (name, value, host, path, expiry, lastAccessed, creationTime, isSecure, isHttpOnly) "
                    "VALUES (?,?,?,?,?,?,?,?,?)",
                    (
                        c["name"],
                        c["value"],
                        host,
                        c.get("path", "/"),
                        int(c.get("expires") or 0),
                        now_us,
                        now_us,
                        1 if c.get("secure") else 0,
                        1 if c.get("httpOnly") else 0,
                    ),
                )
        conn.commit()
        conn.close()
        log.info("env=%s wrote %d cookies to firefox sqlite", env.id, len(cookies))
    except Exception as e:  # noqa: BLE001
        log.warning(
            "firefox-profile sqlite cookie inject failed: %s", e
        )  # browser-gate: allow-legacy 日志文案, 非可执行路径
