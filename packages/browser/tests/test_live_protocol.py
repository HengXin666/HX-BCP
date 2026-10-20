"""实时预览协议层: 坐标换算 / 鼠标 / 按键 / 文本 -> CDP 参数 (纯函数, 无浏览器)。

Agent Note: .agents/notes/implemented/feature/2026-10-17-hxbrowser-live-view.md
"""

from __future__ import annotations

import pytest

import hxbr_testenv  # noqa: F401 — 把 src 放进 sys.path
from hx_browser import live_protocol as lp

PAGE_W = 1280.0
PAGE_H = 720.0
CLIENT_W = 640.0
DOWN = "down"
WHEEL = "wheel"
LEFT = "left"
NONE = "none"
KEY_A = "a"
VK_A = 65
XY = 10


def _mouse(**kw) -> dict:
    return lp.mouse_params(kw, CLIENT_W, PAGE_W, PAGE_H)


def test_scale_uses_width_ratio_only():
    # 客户端图像缩成一半宽: 坐标放大 2 倍; 高度比不参与 (见模块头的判据)
    assert lp.scale((100, 50), CLIENT_W, PAGE_W) == (200.0, 100.0)
    # 尺寸未知 (首帧前) 时原样返回, 不除零
    assert lp.scale((7, 9), 0, PAGE_W) == (7.0, 9.0)
    assert lp.scale((7, 9), CLIENT_W, 0) == (7.0, 9.0)


def test_clamp_bounds():
    assert lp.clamp(-5, 0, XY) == 0
    assert lp.clamp(15, 0, XY) == XY
    assert lp.clamp(5, 0, XY) == 5
    assert lp.clamp(5, 0, -1) == 0  # 页面尺寸未知时不产生负坐标


def test_mouse_down_scaled_and_clamped():
    p = _mouse(action=DOWN, x=320, y=180, button=LEFT, clickCount=2)
    assert p["type"] == "mousePressed"
    assert (p["x"], p["y"]) == (640.0, 360.0)
    assert p["button"] == LEFT and p["clickCount"] == 2
    # 越界坐标夹回页面内 (浏览器 hover 到图像外仍会派发事件)
    far = _mouse(action="up", x=9999, y=-3)
    assert (far["x"], far["y"]) == (PAGE_W, 0)
    assert far["type"] == "mouseReleased"


def test_mouse_move_button_none_unless_dragging():
    assert _mouse(action="move", x=1, y=1)["button"] == NONE
    drag = _mouse(action="move", x=1, y=1, pressed=True, button=LEFT)
    assert drag["button"] == LEFT and drag["clickCount"] == 0


def test_mouse_wheel_delta_not_scaled():
    p = _mouse(action=WHEEL, x=XY, y=XY, deltaX=3, deltaY=-120)
    assert p["type"] == "mouseWheel" and p["button"] == NONE
    # 滚动量不经坐标缩放 (缩放只影响"点在哪")
    assert (p["deltaX"], p["deltaY"]) == (3.0, -120.0)
    assert (p["x"], p["y"]) == (20.0, 20.0)


def test_mouse_unknown_button_falls_back_and_bad_action_raises():
    assert _mouse(action=DOWN, x=0, y=0, button="xbutton1")["button"] == LEFT
    with pytest.raises(lp.ProtocolError):
        _mouse(action="hover", x=0, y=0)


def test_modifier_bits_match_cdp():
    assert lp.modifiers({"alt": True}) == 1
    assert lp.modifiers({"ctrl": True, "shift": True}) == 10
    assert lp.modifiers({"meta": True, "alt": True, "ctrl": True, "shift": True}) == 15
    assert lp.modifiers({}) == 0


def test_key_printable_carries_text_on_down_not_up():
    down = lp.key_params(
        {"action": DOWN, "key": KEY_A, "code": "KeyA", "keyCode": VK_A}
    )
    assert down["type"] == "keyDown" and down["text"] == KEY_A
    assert down["windowsVirtualKeyCode"] == VK_A
    up = lp.key_params({"action": "up", "key": KEY_A, "code": "KeyA", "keyCode": VK_A})
    assert up["type"] == "keyUp" and "text" not in up


def test_key_enter_and_function_keys():
    enter = lp.key_params({"action": DOWN, "key": "Enter", "keyCode": 13})
    assert enter["text"] == "\r"  # 不带 text 的 Enter 在 textarea 里不换行
    arrow = lp.key_params(
        {"action": DOWN, "key": "ArrowLeft", "keyCode": 37, "shift": True}
    )
    assert "text" not in arrow and arrow["modifiers"] == 8


def test_key_errors():
    with pytest.raises(lp.ProtocolError):
        lp.key_params({"action": "press", "key": KEY_A})
    with pytest.raises(lp.ProtocolError):
        lp.key_params({"action": DOWN, "key": ""})


def test_text_params():
    assert lp.text_params({"text": "你好"}) == {"text": "你好"}
    with pytest.raises(lp.ProtocolError):
        lp.text_params({"text": ""})
    with pytest.raises(lp.ProtocolError):
        lp.text_params({"text": 3})


def test_frame_and_error_events():
    meta = {
        "deviceWidth": 1280,
        "deviceHeight": 720,
        "offsetTop": 0,
        "scrollOffsetY": 40,
    }
    ev = lp.frame_event("QUJD", meta, 7)
    assert ev["type"] == lp.EVT_FRAME and ev["sessionId"] == 7
    assert (ev["pageWidth"], ev["pageHeight"], ev["scrollOffsetY"]) == (
        1280.0,
        720.0,
        40.0,
    )
    err = lp.error_event("x", fatal=True)
    assert err == {"type": lp.EVT_ERROR, "message": "x", "fatal": True}


def test_numeric_fields_must_be_finite_numbers():
    for bad in ("abc", None, True, float("inf"), [1]):
        with pytest.raises(lp.ProtocolError):
            _mouse(action=DOWN, x=bad, y=XY)
    assert lp.num({"x": "12.5"}, "x") == 12.5
