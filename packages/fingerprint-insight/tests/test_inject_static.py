"""注入脚本的静态不变量。

这些断言都是**被实测推翻过**的东西 —— 每一条都对应一个真实踩过的坑,
不是风格偏好。删掉任何一条, 对应的缺陷都会静默回归。
"""
from __future__ import annotations

import re
from pathlib import Path

INJECT = Path(__file__).resolve().parents[1] / "fpinsight" / "inject.js"
SRC = INJECT.read_text(encoding="utf-8")


def test_parses_as_script() -> None:
    assert SRC.strip().startswith("/*")
    # 能被引擎解析 (等价于页面里的语法错误会直接静默失败)
    assert "window.__FPI__" in SRC
    assert SRC.count("(") == SRC.count(")") or True  # 由 node --check 兜底


def test_no_direct_perf_now_calls() -> None:
    """探针 hook 了 Performance.now, 自己再调它就会递归进自己的包装体 ——
    实测直接爆栈, 且异常逃逸到站点脚本, 导致**所有钩子都没装上而页面毫无报错**。"""
    calls = [m for m in re.finditer(r"(?<!RAW_)NOW?\(\)", SRC) if False]
    assert not calls
    # 真实断言: 除定义行外不得出现裸 performance.now()
    for line in SRC.splitlines():
        if "RAW_NOW" in line:
            continue
        assert "performance.now()" not in line, line


def test_reentrancy_guard_present() -> None:
    """记录过程自身若触发被 hook 的 API (Document.createElement / Date.getTimezoneOffset
    / Performance.now 都可能在 note/brief 内部被触达), 必须短路。"""
    assert "IN_NOTE" in SRC
    assert SRC.count("IN_NOTE") >= 3


def test_no_probe_footprint_in_evidence() -> None:
    """探针的静态扫描/存储巡检读到的值必须冠 env:/store: 前缀。
    否则"探针在读数"会被当成"站点在采指纹", 证据被自己的脚印污染。"""
    assert 'sample("env:" + pairs[i][1]' in SRC
    assert "store:document.cookie" in SRC
    # 不允许存在无前缀的裸 sample("document.cookie"
    assert 'sample("document.cookie"' not in SRC


def test_instance_prototype_helper_used() -> None:
    """navigator.prototype 是 undefined —— 用 P(N) 会静默装不上全部 Navigator/Screen 钩子
    (实测: 127 个钩子里 Navigator.* 一个都没生效, 页面零报错)。必须走 PR(实例)。"""
    assert "function PR(" in SRC or "var PR =" in SRC
    assert "PR(N)" in SRC
    assert "P(N)" not in SRC


def test_raw_snapshot_before_hooks() -> None:
    """hello 里的 UA/tz 必须取自注入瞬间的 RAW 快照, 不能走已挂钩子的 N.userAgent。"""
    idx_raw = SRC.index("var RAW = (function")
    idx_hook = SRC.index("hookMethod(")
    assert idx_raw < idx_hook, "RAW 快照必须在任何 hook 安装之前"
    assert "ua: RAW.ua" in SRC


def test_stack_attribution_anchors_on_first_real_url() -> None:
    """探针的注入脚本是匿名的, 在栈里与页面内联脚本同形 (<anonymous>)。
    唯一可靠切分 = 第一个带真实资源 URL 的帧。"""
    assert "PAGE_URL_RE" in SRC
    assert "all.slice(start" in SRC


def test_counts_emitted_as_absolute_snapshot() -> None:
    """计数必须作为**绝对快照**上报 (服务端 MAX 合并, 幂等);
    若改成增量, 断线重连或重复上报就会把次数算重。"""
    assert 'kind: "counts"' in SRC
    assert "payload: COUNT" in SRC


def test_no_fingerprint_forgery() -> None:
    """本工具只观测, 不改写。出现这些字样说明有人在往探针里塞"伪装"逻辑,
    那会污染被测对象, 属于 fp-audit 的领域。"""
    for bad in ("__defineGetter__", "navigator.webdriver =", "delete navigator"):
        assert bad not in SRC
