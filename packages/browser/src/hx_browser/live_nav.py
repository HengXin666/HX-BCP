"""实时预览的导航与标签页: 地址栏 / 前进后退刷新 / 标签列表与切换。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md

从 live_view.py 拆出 (单文件 300 行上限)。边界按职责取: live_view 管「一条 page
连接上的帧流与输入」, 本模块管「换哪个页面、去哪个地址」—— 后者要读 browser 级
的 /json 目标表, 与当前 page 连接无关。

## 标签表走 HTTP /json 而不是 Target.getTargets

/json 是 browser 级的 HTTP 端点, 沙箱副本经宿主 bridge 同样可达 (#98: 端口与用法不变);
而 Target.getTargets 需要一条 browser 级 WebSocket —— 为列标签再开一条长连接不值得。
"""

from __future__ import annotations

import contextlib

from . import cdp
from . import live_protocol as lp

NAV_ACTIONS = ("back", "forward", "reload")
_LOOPBACK_JSON = "http://127.0.0.1:%d/json"


async def _targets(debug_port: int) -> list[dict]:
    """/json 目标表。HTTP / 超时 / JSON 解码失败都转成 CdpError, 由会话回一条 error 事件。"""
    try:
        targets = await cdp.http_json(_LOOPBACK_JSON % debug_port, timeout=5)
    except Exception as e:  # noqa: BLE001 — urllib/json/超时的异常类型各不相同
        raise cdp.CdpError(f"读取标签页失败: {e}") from e
    if not isinstance(targets, list):
        raise cdp.CdpError("读取标签页失败: /json 返回的不是列表")
    return targets


async def list_tabs(debug_port: int, active_ws: str) -> list[dict]:
    """该副本的页面标签页; active = 当前预览连接所在的那一个。"""
    return [
        {
            "targetId": t.get("id", ""),
            "title": t.get("title", ""),
            "url": t.get("url", ""),
            "active": t.get("webSocketDebuggerUrl", "") == active_ws,
        }
        for t in await _targets(debug_port)
        if t.get("type") == "page"
    ]


async def tab_ws(debug_port: int, target_id: str) -> str:
    """targetId -> 它的 page 级调试地址; 找不到就报协议错误 (不静默换成别的标签)。"""
    if target_id == "":
        raise lp.ProtocolError("切换标签缺少 targetId")
    for t in await _targets(debug_port):
        if t.get("id") == target_id and t.get("webSocketDebuggerUrl"):
            # 让浏览器自己也把该标签切到前台 (可见窗口里用户看到的与预览一致);
            # 失败不影响预览本身, 故不报错。
            with contextlib.suppress(Exception):
                await cdp.http_json(
                    (_LOOPBACK_JSON % debug_port) + "/activate/" + target_id, timeout=5
                )
            return t["webSocketDebuggerUrl"]
    raise lp.ProtocolError(f"标签不存在或没有调试入口: {target_id}")


async def navigate(conn: cdp.CdpConnection, msg: dict) -> None:
    """`navigate` 消息: 有 action 走历史/刷新, 否则按 url 跳转。"""
    action = str(msg.get("action", ""))
    if action in NAV_ACTIONS:
        await history(conn, action)
        return
    url = str(msg.get("url", "")).strip()
    if url == "":
        raise lp.ProtocolError("导航消息既没有 action 也没有 url")
    await conn.send("Page.navigate", {"url": normalize_url(url)}, timeout=15)


def normalize_url(url: str) -> str:
    """地址栏输入 -> 可导航地址。没写协议的按 https 补 (与浏览器地址栏的习惯一致)。"""
    # scheme 不区分大小写 (ABOUT:blank 同样有效); 判断用小写副本, 返回原文
    if "://" in url or url.lower().startswith(("about:", "data:")):
        return url
    return "https://" + url


async def history(conn: cdp.CdpConnection, action: str) -> None:
    """前进/后退/刷新。前两者先读历史表 —— CDP 没有 goBack 这种便捷方法。"""
    if action == "reload":
        await conn.send("Page.reload", {}, timeout=10)
        return
    hist = await conn.send("Page.getNavigationHistory", {}, timeout=8)
    entries = hist.get("entries", [])
    step = -1 if action == "back" else 1
    idx = int(hist.get("currentIndex", 0)) + step
    if idx < 0 or idx >= len(entries):
        word = "后退" if step < 0 else "前进"
        raise lp.ProtocolError(f"没有可{word}的历史记录")
    await conn.send(
        "Page.navigateToHistoryEntry", {"entryId": entries[idx]["id"]}, timeout=10
    )
