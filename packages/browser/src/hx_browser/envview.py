"""环境对外视图与出口对齐 —— 前端/其它服务看到的那一份形状 (2026-10-13 从 api.py 拆出)。

Agent Note: .agents/notes/implemented/feature/2026-10-13-hxbrowser-visible-window-and-multi-instance.md

## 为什么拆出来

api.py 是既有存量 (棘轮登记 766 行, **只许降不许涨**), 而本次要给它加「可见窗口」
字段 —— 塞进去就是 789 行, 门禁会红。拆的边界按职责取: **「环境序列化成什么形状」**
与「HTTP 路由」是两件事, 而且前者是**跨服务契约** (T2/T4 直接消费), 值得单独一处。

## 可见窗口契约 (T2/T4 依赖, 字段名/语义在此定死)

    visible: bool          —— 用户**真的看得见**这个窗口吗
                              (**断言真值**, 不是意图; 见 browser._assert_window)
    display: str           —— ":1"=用户桌面 / ":7x"=Xvfb 虚拟屏(看不见) / ""=无 X
    window_title: str      —— 可见顶层窗口标题; 未断言到为空
    visible_reason: str    —— visible=false 时**必非空**, 说清为什么看不见

为什么顶层再放一份 (runtime 里已有): 面板列环境时不该为了三个字段去解 runtime,
且环境未运行时 runtime 是 null —— 顶层字段缺失比嵌套 null 更好判。
**值仍来自 RuntimeInfo 这一处**: 这里只换位置暴露, 不复制判据。

## 出口对齐

match_ip 模式下按出口 IP 的国家对齐时区/语言。映射表在 locale.py (唯一定义处)。
"""

from __future__ import annotations

from . import kernels, launch_hooks, locale
from .browser import get_runtime
from .models import Environment


def env_dict(env: Environment) -> dict:
    """环境 -> 对外 dict (含运行时与可见窗口字段)。

    顶层字段 = T2/T4 的消费面。为什么在顶层再放一份 (runtime 里已有):
    面板列环境时不该为了几个字段去解 runtime, 且环境未运行时 runtime 是 null。
    **值仍来自 RuntimeInfo 这一处** —— 这里只换位置暴露, 不复制判据。
    """
    rt = get_runtime(env.id)
    d = env.model_dump(exclude={"runtime"})
    d["runtime"] = rt.model_dump() if rt else None
    d["visible"] = bool(rt.visible) if rt else False
    d["display"] = rt.display if rt else ""
    d["window_title"] = rt.window_title if rt else ""
    d["visible_reason"] = rt.reason if rt else "环境未运行 (没有窗口)"
    # display_source 是「窗口开在真实桌面还是 Xvfb」的**唯一可判读信号** ——
    # 上层靠它决定要不要提示用户"你看不见" (2026-10-13 Lead 指出缺失)。
    d["display_source"] = rt.display_source if rt else ""
    d["display_source_detail"] = rt.display_source_detail if rt else ""
    d["window_id"] = rt.window_id if rt else ""
    d["window_pos"] = list(rt.window_pos) if rt else []
    # 2026-10-16: 运行中改了代理/指纹/偏好 -> 待重启 (只改名称/标签/备注不算, 见 config_hash)
    d["pending_restart"] = bool(
        rt and rt.config_hash and rt.config_hash != launch_hooks.config_hash(env)
    )
    d["kernel_family"] = kernels.family_of(env)
    d["deleted"] = bool(env.deleted_at)
    d["proxy"]["has_password"] = bool(env.proxy.password)
    return d


def apply_timezone_from_ip(env: Environment, geo: dict) -> Environment:
    """match_ip 模式：按出口 IP 国家对齐时区/语言 (粗粒度)。

    映射表在 locale.py —— 那是**唯一**定义处。此处原来另写了一份 country->tz 表
    **外加**一份 country->lang 表, 与 fingerprint.gen_random_fingerprint 那份不同步
    (原来这里认 "SG" 而那边不认)。同一事实两处判据, 必然分叉。
    """
    if env.location_mode != "match_ip":
        return env
    country = (geo.get("country") or "").upper()
    tz = locale.tz_for_country(country)
    if tz:
        env.fingerprint.timezone = tz
        if not env.fingerprint.language:
            lang = locale.lang_for_country(country)
            if lang:
                env.fingerprint.language = lang
    return env
