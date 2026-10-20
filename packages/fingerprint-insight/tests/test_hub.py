"""事件中枢: 落盘、幂等、可见性屏障。"""
from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from fpinsight.hub import Hub  # noqa: E402


def _hub(tmp_path: Path) -> Hub:
    h = Hub(tmp_path / "t.db", port=1)
    h._conn_init()
    import threading

    h._writer = threading.Thread(target=h._writer_loop, daemon=True)
    h._writer.start()
    return h


def _rows(db: Path, sql: str) -> list[tuple]:
    return sqlite3.connect(db).execute(sql).fetchall()


def test_calls_land_after_drain(tmp_path: Path) -> None:
    h = _hub(tmp_path)
    for i in range(5):
        h.submit(("call", ("sid1", "top", "i1", i, "A.b", "method", "canvas", i, "k",
                           "[]", "1", 0.1, "fn", "https://x/y.js", 3, "[]", i, "cdp", 1.0)))
    assert h.drain(5), "drain 必须在超时内放行"
    assert _rows(tmp_path / "t.db", "select count(*) from calls")[0][0] == 5
    h.stop()


def test_drain_is_a_real_barrier(tmp_path: Path) -> None:
    """回归: 旧实现用共享"空闲"标志位, drain 会在 writer 尚未清标志时提前返回,
    导致 counters 全表 0 行而 submit 明明返回成功。"""
    h = _hub(tmp_path)
    h.submit(("counts", "s1", {"A.b|method": 7}))
    assert h.drain(5)
    got = _rows(tmp_path / "t.db", "select n from counters where path='A.b'")
    assert got == [(7,)], "drain 返回后计数必须已可见"
    h.stop()


def test_counts_merge_is_idempotent_max(tmp_path: Path) -> None:
    h = _hub(tmp_path)
    h.submit(("counts", "s1", {"A.b|method": 5}))
    h.submit(("counts", "s1", {"A.b|method": 5}))   # 同一快照重复到达
    h.submit(("counts", "s1", {"A.b|method": 9}))   # 更新
    h.submit(("counts", "s1", {"A.b|method": 7}))   # 乱序的旧快照
    assert h.drain(5)
    assert _rows(tmp_path / "t.db", "select n from counters where path='A.b'") == [(9,)]
    h.stop()


def test_blank_url_does_not_overwrite_real_page(tmp_path: Path) -> None:
    """注入脚本对每个新文档都发 hello, 包含初始 about:blank。
    回归: 它曾把已记录的真实 URL 抹成 about:blank。"""
    h = _hub(tmp_path)
    h.submit(("page", "s1", "https://real.site/p", "", "UA", "Asia/Shanghai", "zh", 1, "cdp", 1.0))
    h.submit(("page", "s1", "about:blank", "", "UA", "Asia/Shanghai", "zh", 1, "cdp", 2.0))
    assert h.drain(5)
    assert _rows(tmp_path / "t.db", "select url from pages") == [("https://real.site/p",)]
    h.stop()


def test_invariants_upsert_keeps_latest(tmp_path: Path) -> None:
    h = _hub(tmp_path)
    h.submit(("inv", [{"path": "Navigator.platform", "n": 2, "distinct": 1, "stable": True,
                       "vals": ['"Linux"'], "first": '"Linux"', "last": '"Linux"', "err": 0}], "s1", "top"))
    h.submit(("inv", [{"path": "Navigator.platform", "n": 4, "distinct": 1, "stable": True,
                       "vals": ['"Linux"'], "first": '"Linux"', "last": '"Linux"', "err": 0}], "s1", "top"))
    assert h.drain(5)
    rows = _rows(tmp_path / "t.db", "select n, vals from invariants where path='Navigator.platform'")
    assert rows == [(4, '["\\"Linux\\""]')]
    h.stop()


def test_ingest_call_parses_stack_and_top(tmp_path: Path) -> None:
    h = _hub(tmp_path)
    h._ingest_call("s1", "top", "i1", {
        "seq": 1, "path": "CanvasRenderingContext2D.fillText", "surface": "method", "cat": "canvas",
        "n": 3, "args": ['"x"', 1], "ret": "undefined", "ms": 0.5, "t": 42,
        "stack": [{"fn": "readCanvas", "url": "https://t/f.js", "line": 12}],
        "top": {"fn": "readCanvas", "url": "https://t/f.js", "line": 12}})
    assert h.drain(5)
    row = _rows(tmp_path / "t.db",
                "select path, top_fn, top_url, top_line, args from calls")[0]
    assert row[:4] == ("CanvasRenderingContext2D.fillText", "readCanvas", "https://t/f.js", 12)
    assert json.loads(row[4]) == ['"x"', 1]
    h.stop()
