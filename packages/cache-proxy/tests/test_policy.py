"""policy 单元测试 — 该缓存什么, 尤其"绝不能缓存什么"。"""
import os, sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))
from hxcp import policy


def test_static_cached_without_headers():
    d = policy.decide("https://cdn.test/a.js", 200, {"content-type": "text/javascript"}, size=100, max_bytes=1000)
    assert d and d.ttl > 0


def test_set_cookie_never_cached():
    d = policy.decide("https://x.test/a.js", 200,
                      {"content-type": "text/javascript", "set-cookie": "sid=1"}, size=10, max_bytes=1000)
    assert not d and d.reason == "set_cookie"


def test_sensitive_path_never_cached():
    for u in ("https://x.test/login/x.js", "https://x.test/api/data.js",
              "https://x.test/oauth/tok.css"):
        d = policy.decide(u, 200, {"content-type": "text/javascript"}, size=10, max_bytes=1000)
        assert not d, u


def test_json_requires_positive_cache_control():
    assert not policy.decide("https://x.test/d.json", 200,
                             {"content-type": "application/json"}, size=10, max_bytes=1000)
    d = policy.decide("https://x.test/d.json", 200,
                      {"content-type": "application/json", "cache-control": "max-age=3600"},
                      size=10, max_bytes=1000)
    assert d


def test_html_no_store_rejected():
    d = policy.decide("https://x.test/p.html", 200,
                      {"content-type": "text/html", "cache-control": "no-store"}, size=10, max_bytes=1000)
    assert not d


def test_oversize_rejected():
    assert not policy.decide("https://x.test/a.js", 200, {"content-type": "text/javascript"},
                             size=99999, max_bytes=1000)


def test_non_200_rejected():
    assert not policy.decide("https://x.test/a.js", 404, {"content-type": "text/javascript"},
                             size=10, max_bytes=1000)


def test_rules_allow_deny():
    r = policy.Rules(allow_hosts=frozenset({"cdn.test"}), deny_hosts=frozenset({"bad.test"}))
    assert r.may_handle("https://cdn.test/a.js")
    assert not r.may_handle("https://other.test/a.js")
    assert not r.may_handle("https://bad.test/a.js")
    assert policy.Rules().may_handle("https://anything.test/a.js")
