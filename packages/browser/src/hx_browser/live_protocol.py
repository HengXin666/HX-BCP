"""实时预览的**协议层**: 客户端消息 -> CDP 调用参数 (纯函数, 不碰网络)。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md

## 为什么与 live_view.py 分开

`live_view.py` 要连 CDP、要跑 WebSocket, 测它必须有浏览器或一套假 CDP 的全套异步
脚手架。而**真正容易错的那部分是纯计算**: 坐标从客户端像素换算到页面 CSS 像素、
按键事件的 type/修饰位、滚轮的 delta 方向。把它们放在无依赖的函数里, CI 上不需要
任何浏览器就能逐条断言 —— 这正是本仓「验证纪律」要的那种对照: 断言的是**实际值**,
不是"我传了参数所以它生效了"。

## 坐标换算的判据只有一处

客户端显示的是一张被缩放过的 JPEG: 它的宽高 (`client_w/h`) 与页面 CSS 视口
(`page_w/h`) 不同。换算 = 按**宽度比**缩放 (screencast 等比缩放, 不会变形)。
高度比在首帧与元数据之间可能差一个滚动条宽度, 用它会让纵向点击整体偏移,
所以**只认宽度比**, 这是唯一判据, 前端不再算第二份。

客户端给的坐标若超出图像范围 (浏览器 hover 到图像外仍会派发 move), 一律夹回边界:
把越界坐标原样传给 CDP 会让页面收到负坐标的 mousemove, 表现为"鼠标跳到左上角"。
"""

from __future__ import annotations

import math
from typing import Any

# 客户端 -> 服务端的消息类型 (唯一定义处; 前端 live.ts 的联合类型与此一一对应)
MSG_MOUSE = "mouse"
MSG_KEY = "key"
MSG_TEXT = "text"
MSG_NAVIGATE = "navigate"
MSG_QUALITY = "quality"
MSG_PAUSE = "pause"
MSG_RESUME = "resume"
MSG_TABS = "tabs"
MSG_ACTIVATE = "activate"

# 服务端 -> 客户端
EVT_FRAME = "frame"
EVT_META = "meta"
EVT_TABS = "tabs"
EVT_ERROR = "error"

# CDP 方法名 (出现多次, 按 PROJECT-CONST 提常量)
CDP_MOUSE = "Input.dispatchMouseEvent"
CDP_KEY = "Input.dispatchKeyEvent"
CDP_TEXT = "Input.insertText"

_MOUSE_TYPES = {
    "move": "mouseMoved",
    "down": "mousePressed",
    "up": "mouseReleased",
    "wheel": "mouseWheel",
}
_BUTTONS = ("left", "middle", "right")
_KEY_TYPES = {"down": "keyDown", "up": "keyUp", "char": "char"}

# Input.dispatchKeyEvent 的修饰位 (CDP 定义: alt=1 ctrl=2 meta=4 shift=8)
_MOD_BITS = (("alt", 1), ("ctrl", 2), ("meta", 4), ("shift", 8))

# 这些键必须带 text, 否则 Chromium 不会把它们当可编辑输入处理 (实测: 不带 text 的
# Enter 在 <textarea> 里不换行)。
_KEY_TEXT = {"Enter": "\r", "Tab": "\t"}


class ProtocolError(ValueError):
    """客户端消息不合法 —— 回一条 error 事件, 不是关连接。"""


def num(msg: dict, key: str, default: float = 0.0) -> float:
    """客户端数值字段 -> float; 非数字 / NaN / 无穷一律判为协议错误 (回 error 事件, 不断连)。"""
    raw = msg.get(key, default)
    if isinstance(raw, bool):
        raise ProtocolError(f"字段 {key} 必须是数字")
    try:
        value = float(raw)
    except (TypeError, ValueError, OverflowError) as e:
        raise ProtocolError(f"字段 {key} 必须是数字") from e
    if not math.isfinite(value):
        raise ProtocolError(f"字段 {key} 必须是有限数字")
    return value


def scale(
    client_xy: tuple[float, float], client_w: float, page_w: float
) -> tuple[float, float]:
    """客户端图像坐标 -> 页面 CSS 像素。只认宽度比 (见模块头)。"""
    if client_w <= 0 or page_w <= 0:
        return (float(client_xy[0]), float(client_xy[1]))
    ratio = page_w / client_w
    return (client_xy[0] * ratio, client_xy[1] * ratio)


def clamp(value: float, lo: float, hi: float) -> float:
    if hi < lo:
        return lo
    return max(lo, min(value, hi))


def modifiers(msg: dict) -> int:
    bits = 0
    for name, bit in _MOD_BITS:
        if msg.get(name):
            bits |= bit
    return bits


def mouse_params(
    msg: dict, client_w: float, page_w: float, page_h: float
) -> dict[str, Any]:
    """`mouse` 消息 -> Input.dispatchMouseEvent 参数。"""
    action = str(msg.get("action", ""))
    cdp_type = _MOUSE_TYPES.get(action)
    if cdp_type is None:
        raise ProtocolError(f"未知鼠标动作: {action or '(空)'}")
    x, y = scale((num(msg, "x"), num(msg, "y")), client_w, page_w)
    params: dict[str, Any] = {
        "type": cdp_type,
        "x": clamp(x, 0, page_w),
        "y": clamp(y, 0, page_h),
        "modifiers": modifiers(msg),
        "clickCount": int(num(msg, "clickCount", 1)) if action in ("down", "up") else 0,
    }
    button = str(msg.get("button", "left"))
    params["button"] = button if button in _BUTTONS else "left"
    if action == "move" and not msg.get("pressed"):
        params["button"] = "none"
    if action == "wheel":
        params["button"] = "none"
        # 滚轮的 delta 不经坐标缩放: 它是**滚动量**而不是位置, 页面按 CSS 像素滚动,
        # 客户端的像素量与之同量纲 (缩放只影响"点在哪", 不影响"滚多少")。
        params["deltaX"] = num(msg, "deltaX")
        params["deltaY"] = num(msg, "deltaY")
    return params


def key_params(msg: dict) -> dict[str, Any]:
    """`key` 消息 -> Input.dispatchKeyEvent 参数。"""
    action = str(msg.get("action", ""))
    cdp_type = _KEY_TYPES.get(action)
    if cdp_type is None:
        raise ProtocolError(f"未知按键动作: {action or '(空)'}")
    key = str(msg.get("key", ""))
    if key == "":
        raise ProtocolError("按键消息缺少 key")
    key_code = int(num(msg, "keyCode"))
    params: dict[str, Any] = {
        "type": cdp_type,
        "key": key,
        "code": str(msg.get("code", "")),
        "modifiers": modifiers(msg),
        "windowsVirtualKeyCode": key_code,
        "nativeVirtualKeyCode": key_code,
    }
    text = _KEY_TEXT.get(key, "")
    # 单字符键 (a / 1 / 空格) 的 text 就是它自己; 功能键 (ArrowLeft) 没有 text。
    if text == "" and len(key) == 1:
        text = key
    if text != "" and cdp_type != "keyUp":
        params["text"] = text
    return params


def text_params(msg: dict) -> dict[str, Any]:
    """`text` 消息 (输入法/粘贴) -> Input.insertText 参数。"""
    text = msg.get("text", "")
    if not isinstance(text, str) or text == "":
        raise ProtocolError("文本消息缺少 text")
    return {"text": text}


def frame_event(data: str, meta: dict, session_id: int) -> dict[str, Any]:
    """一帧 screencast -> 发给客户端的事件。

    `session_id` 原样带回是为了让客户端能把 ack 对上; ack 丢了 Chromium 会**停止推帧**
    (它按未确认帧数限流), 表现为"画面卡住但页面其实在动"。
    """
    return {
        "type": EVT_FRAME,
        "data": data,
        "sessionId": session_id,
        "pageWidth": float(meta.get("deviceWidth", 0)),
        "pageHeight": float(meta.get("deviceHeight", 0)),
        "offsetTop": float(meta.get("offsetTop", 0)),
        "scrollOffsetX": float(meta.get("scrollOffsetX", 0)),
        "scrollOffsetY": float(meta.get("scrollOffsetY", 0)),
    }


def error_event(message: str, fatal: bool = False) -> dict[str, Any]:
    """错误一律**显式回报**, 绝不静默吞掉 (仓库诚实口径)。"""
    return {"type": EVT_ERROR, "message": message, "fatal": bool(fatal)}
