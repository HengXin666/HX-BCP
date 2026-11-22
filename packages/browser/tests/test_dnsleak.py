"""DNS 泄漏探针判定 (纯函数, 不出网) 与适用范围.

Agent Note: .agents/notes/implemented/feature/2026-11-15-hxbrowser-dns-leak-probe.md
"""

from __future__ import annotations

import hxbr_testenv  # noqa: F401
from hx_browser import dnsleak
from hx_browser.models import Environment, ProxyConfig


def row(ip, cc, asn, kind):
    return {"ip": ip, "country": cc, "country_name": "", "asn": asn, "org": "", "type": kind}


EXIT = row("114.33.139.168", "tw", "AS3462 Data Communication Business Group", "ip")
CONCLUSION = row("DNS may be leaking.", "", "", "conclusion")


def test_same_asn_and_same_country_pass():
    hinet, google = row("168.95.1.1", "tw", "AS3462 HiNet", "dns"), row("8.8.8.8", "tw", "AS15169 Google LLC", "dns")
    rows = [EXIT, hinet, google, CONCLUSION]
    out = dnsleak.assess(rows)
    assert out["status"] == dnsleak.HEALTHY
    assert [h["match"] for h in out["resolvers"]] == ["asn", "country"]
    assert out["exit"]["asn"] == "AS3462"


def test_foreign_resolver_is_red():
    rows = [EXIT, row("168.95.1.1", "tw", "AS3462 HiNet", "dns"), row("14.154.58.59", "cn", "AS4134 Chinanet", "dns")]
    out = dnsleak.assess(rows)
    assert out["status"] == dnsleak.LEAK == "error"
    assert "1/2" in out["value"] and "14.154.58.59" in out["detail"]


def test_same_asn_other_country_passes():
    rows = [EXIT, row("203.0.113.9", "jp", "AS3462 HiNet", "dns")]
    assert dnsleak.assess(rows)["status"] == dnsleak.HEALTHY


def test_missing_evidence_is_unknown_not_pass():
    assert dnsleak.assess([])["status"] == dnsleak.UNKNOWN
    assert dnsleak.assess([EXIT, CONCLUSION])["status"] == dnsleak.UNKNOWN
    assert dnsleak.assess([row("8.8.8.8", "us", "AS15169", "dns")])["status"] == dnsleak.UNKNOWN


def test_asn_of():
    assert dnsleak.asn_of("AS16509 Amazon.com Inc.") == "AS16509"
    assert dnsleak.asn_of("as13335") == "AS13335"
    assert dnsleak.asn_of("") == ""


def test_applies_only_when_a_proxy_is_attached():
    for kind in ("no_proxy", "direct", "system"):
        assert not dnsleak.applies(Environment(proxy=ProxyConfig(type=kind)), environ={})
    for kind in ("http", "https", "socks5"):
        assert dnsleak.applies(Environment(proxy=ProxyConfig(type=kind, host="1.2.3.4", port=8080)), environ={})


def test_applies_to_system_mode_with_proxy_env():
    env_vars = {"HTTPS_PROXY": "http://u:p@127.0.0.1:7890"}
    assert dnsleak.applies(Environment(proxy=ProxyConfig(type="system")), environ=env_vars)
    assert not dnsleak.applies(Environment(proxy=ProxyConfig(type="direct")), environ=env_vars)
    assert not dnsleak.applies(Environment(proxy=ProxyConfig(type="no_proxy")), environ=env_vars)


def test_doctor_lists_probe_only_for_custom_upstream():
    assert dnsleak.doctor_checks(Environment(id="x", proxy=ProxyConfig(type="direct"))) == []
    got = dnsleak.doctor_checks(Environment(id="not-running", proxy=ProxyConfig(type="http", host="1.2.3.4", port=1)))
    assert got[0]["id"] == "dns_leak" and got[0]["status"] == dnsleak.UNKNOWN


def test_rows_of_page_result():
    rows = [EXIT, row("168.95.1.1", "tw", "AS3462 HiNet", "dns")]
    assert dnsleak.rows_of({"id": "abc123", "rows": rows}) == (rows, "")
    assert dnsleak.rows_of({"error": "probe id unreadable: 407"})[1].startswith("probe id unreadable")
    assert dnsleak.rows_of(None) == ([], "probe returned NoneType")
    assert dnsleak.rows_of({"id": "abc123"}) == ([], "probe rows missing")
