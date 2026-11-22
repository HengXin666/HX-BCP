"""proxy entry pure parts: TLS rules, shared cache policy, runtime aggregation, access facts."""
import json
import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))
from hxcp import access, policy, proxy_rules, runtime  # noqa: E402


def test_default_is_tunnel():
    assert proxy_rules.decide(proxy_rules.default_rules(), "cdn.test")[0] == "tunnel"


def test_decrypt_suffix_and_never_wins(tmp_path):
    proxy_rules.set_host(tmp_path, "cdn.test", "decrypt")
    doc = proxy_rules.load(tmp_path)
    assert proxy_rules.decide(doc, "a.cdn.test") == ("decrypt", "rule:cdn.test")
    assert proxy_rules.decide(doc, "xcdn.test")[0] == "tunnel"
    proxy_rules.set_host(tmp_path, "login.cdn.test", "never")
    doc = proxy_rules.load(tmp_path)
    assert proxy_rules.decide(doc, "login.cdn.test")[0] == "tunnel"
    doc["decrypt_all"] = True
    assert proxy_rules.decide(doc, "other.test") == ("decrypt", "decrypt_all")
    assert proxy_rules.decide(doc, "login.cdn.test")[0] == "tunnel"


def test_set_host_normalizes_and_rejects(tmp_path):
    doc = proxy_rules.set_host(tmp_path, "https://*.CDN.test:443/x", "decrypt")
    assert doc["decrypt"] == ["cdn.test"]
    try:
        proxy_rules.set_host(tmp_path, "x.test", "bogus")
        raise AssertionError("bad mode accepted")
    except ValueError:
        pass


def test_live_rules_reload(tmp_path):
    live = proxy_rules.LiveRules(tmp_path)
    assert live.decide("cdn.test")[0] == "tunnel"
    proxy_rules.set_host(tmp_path, "cdn.test", "decrypt")
    os.utime(tmp_path / proxy_rules.RULES_FILE, (time.time() + 5, time.time() + 5))
    live._checked = 0
    assert live.decide("cdn.test")[0] == "decrypt"


def test_vary_user_agent_not_shared():
    d = policy.decide("https://fonts.test/css", 200,
                      {"content-type": "text/css", "vary": "Accept-Encoding, User-Agent"},
                      size=10, max_bytes=1000)
    assert not d and d.reason == "vary:user-agent"
    ok = policy.decide("https://cdn.test/a.css", 200,
                       {"content-type": "text/css", "vary": "Accept-Encoding"}, size=10, max_bytes=1000)
    assert ok


def test_request_cacheable():
    assert policy.request_cacheable("GET", {"accept": "*/*"}) == ""
    assert policy.request_cacheable("POST", {}) == "method"
    assert policy.request_cacheable("GET", {"Range": "bytes=0-1"}) == "range"
    assert policy.request_cacheable("GET", {"Authorization": "Bearer x"}) == "authorization"


def test_runtime_aggregate_sums_running(tmp_path):
    runtime.publish(tmp_path, "cdp-a", {"state": "running", "stats": {"hits": 2, "bytes_served": 10}})
    runtime.publish(tmp_path, "proxy-b", {"state": "running", "stats": {"hits": 3, "bytes_network": 30}})
    runtime.publish(tmp_path, "cdp-old", {"state": "stopped", "stats": {"hits": 100}})
    agg = runtime.aggregate(tmp_path)
    assert agg["state"] == "running" and agg["running"] == 2 and agg["instances"] == 3
    assert agg["stats"]["hits"] == 5
    assert abs(agg["stats"]["saved_ratio"] - 0.25) < 1e-9


def test_runtime_stale(tmp_path):
    runtime.publish(tmp_path, "x", {"state": "running", "updated_at": time.time() - 999})
    assert runtime.instances(tmp_path)[0]["state"] == "stale"


def test_access_info_and_har(tmp_path):
    runtime.publish(tmp_path, "proxy-p", {"state": "running", "kind": "proxy", "listen": "0.0.0.0",
                                          "listeners": [{"name": "p", "mode": "regular", "port": 8899}]})
    info = access.access_info(tmp_path, "http://api.test")
    assert info["running"] and info["proxy_url"] == "http://127.0.0.1:8899"
    assert info["snippets"]["chromium"] == "--proxy-server=http://127.0.0.1:8899"
    assert info["snippets"]["pac"] == "http://api.test/proxy.pac"
    har = access.to_har([{"kind": "flow", "url": "https://x.test/a?b=1", "method": "POST", "status": 200,
                          "req_headers": {"a": "1"}, "post_data": "z", "at": 1.0, "ms": 5},
                         {"kind": "tunnel", "url": "https://y.test:443"}])
    e = har["log"]["entries"]
    assert len(e) == 1 and e[0]["request"]["queryString"] == [{"name": "b", "value": "1"}]
    assert e[0]["request"]["postData"]["text"] == "z"
    json.dumps(har)
