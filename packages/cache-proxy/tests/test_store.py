"""store 单元测试 — 缓存键归一化 / 版本身份 / 内容寻址去重。"""
import json, sys, os, tempfile, time
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

from hxcp.store import Store, cache_key, normalize_url, SCHEMA_VERSION


def test_normalize_orders_params():
    a = normalize_url("https://x.test/a.js?b=2&a=1")
    b = normalize_url("https://x.test/a.js?a=1&b=2")
    assert a == b, "参数顺序不同必须归一为同一键"


def test_normalize_drops_volatile():
    a = normalize_url("https://x.test/a.js?v=123")
    b = normalize_url("https://x.test/a.js?v=999")
    assert a == b
    assert "v=" not in a


def test_normalize_keeps_meaningful():
    a = normalize_url("https://x.test/a.js?ver=1")
    b = normalize_url("https://x.test/a.js?ver=2")
    assert a != b, "非易变参数必须保留, 否则不同内容会互相命中"


def test_key_stable():
    assert cache_key("GET", "https://x.test/a.js") == cache_key("get", "https://x.test/a.js")


def test_put_get_roundtrip(tmp=tempfile.mkdtemp()):
    s = Store(tmp)
    e = s.put("https://x.test/a.js", "GET", 200, [["content-type", "text/javascript"]],
              b"console.log(1)", ttl=100, etag='"abc"')
    assert e is not None
    got = s.lookup("https://x.test/a.js")
    assert got is not None and got.etag == '"abc"'
    assert s.get_blob(got.body_hash) == b"console.log(1)"
    s.close()


def test_content_addressed_dedup(tmp=tempfile.mkdtemp()):
    s = Store(tmp)
    body = b"same-bytes"
    s.put("https://x.test/a.js", "GET", 200, [], body, ttl=100)
    s.put("https://x.test/b.js", "GET", 200, [], body, ttl=100)
    files = list(s.blobs.rglob("*.bin"))
    assert len(files) == 1, "相同内容只应存一份 blob"
    assert s.stats()["entries"] == 2
    s.close()


def test_oversize_rejected(tmp=tempfile.mkdtemp()):
    s = Store(tmp, max_entries_bytes=10)
    assert s.put("https://x.test/big.js", "GET", 200, [], b"x" * 11, ttl=100) is None
    s.close()


def test_schema_mismatch_rebuilds(tmp=tempfile.mkdtemp()):
    s = Store(tmp)
    s.put("https://x.test/a.js", "GET", 200, [], b"v1", ttl=100)
    assert s.stats()["entries"] == 1
    s.close()
    # 手改版本号, 模拟"索引由旧代码产出"
    import sqlite3
    c = sqlite3.connect(os.path.join(tmp, "index.sqlite3"))
    c.execute("UPDATE meta SET v='0' WHERE k='schema_version'")
    c.commit(); c.close()
    s2 = Store(tmp)
    assert s2.rebuilt, "版本不符必须重建"
    assert s2.stats()["entries"] == 0
    s2.close()


def test_stats_after_close_degrades(tmp=tempfile.mkdtemp()):
    s = Store(tmp)
    s.close()
    st = s.stats()
    assert st.get("closed") is True, "关闭后取统计必须降级而不是抛异常"


def test_purge_expired(tmp=tempfile.mkdtemp()):
    s = Store(tmp)
    s.put("https://x.test/a.js", "GET", 200, [], b"old", ttl=0.01)
    time.sleep(0.05)
    assert s.purge_expired() == 1
    assert s.lookup("https://x.test/a.js") is None
    s.close()
