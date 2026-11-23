package nodeparse

import (
	"testing"
)

// A proxy list has no container header, so every assertion here is about the
// detection boundary: what a flat list must be read as, and — just as important
// — which documents it must NOT claim.

func TestParseBareProxyListImportsEveryEndpoint(t *testing.T) {
	result, err := Parse([]byte("1.2.3.4:8080\n5.6.7.8:3128\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.DetectedFormat != ProxyListFormat {
		t.Fatalf("format = %q, want %q", result.DetectedFormat, ProxyListFormat)
	}
	if len(result.Nodes) != 2 || len(result.Failures) != 0 {
		t.Fatalf("nodes = %d, failures = %+v", len(result.Nodes), result.Failures)
	}
	// A bare endpoint carries no protocol, so it takes the documented default.
	if result.Nodes[0].Protocol != "http" || result.Nodes[0].Canonical["server"] != "1.2.3.4" || result.Nodes[0].Canonical["port"] != 8080 {
		t.Fatalf("unexpected node: %+v", result.Nodes[0])
	}
}

func TestParseBareProxyListWithInlineCredentials(t *testing.T) {
	result, err := Parse([]byte("1.2.3.4:8080:user:pass\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 1 {
		t.Fatalf("nodes = %d", len(result.Nodes))
	}
	node := result.Nodes[0]
	if node.Canonical["username"] != "user" || node.Canonical["password"] != "pass" {
		t.Fatalf("credentials were dropped: %#v", node.Canonical)
	}
}

// A file that mixes bare and scheme-carrying lines must keep the scheme: reading
// "socks5://h:p" as HTTP would silently produce endpoints that do not work.
func TestParseMixedProxyListKeepsEachLineProtocol(t *testing.T) {
	result, err := Parse([]byte("1.2.3.4:8080\nsocks5://5.6.7.8:1080\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %d, failures = %+v", len(result.Nodes), result.Failures)
	}
	if result.Nodes[0].Protocol != "http" || result.Nodes[1].Protocol != "socks5" {
		t.Fatalf("protocols = %q, %q", result.Nodes[0].Protocol, result.Nodes[1].Protocol)
	}
}

// The shape that made a real 3,400-endpoint export import zero nodes: the
// annotation is appended inside the "#" fragment, and "81% |" is not a valid URL
// escape, so url.Parse rejects the entire line.
func TestParseDecoratedShareURIListRecoversAnnotatedLines(t *testing.T) {
	result, err := Parse([]byte(
		"socks5://184.178.172.13:4145#住宅-风险81% | AS22773 - Cox Communications Inc.\n" +
			"socks5://184.178.172.17:4145#住宅-风险81% | AS22773 - Cox Communications Inc.\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.DetectedFormat != "uri-list" {
		t.Fatalf("format = %q, want uri-list (a decorated list is still a share URI list)", result.DetectedFormat)
	}
	if len(result.Nodes) != 2 || len(result.Failures) != 0 {
		t.Fatalf("nodes = %d, failures = %+v", len(result.Nodes), result.Failures)
	}
	if result.Nodes[0].Protocol != "socks5" || result.Nodes[0].Canonical["server"] != "184.178.172.13" {
		t.Fatalf("unexpected node: %+v", result.Nodes[0])
	}
}

// A checker column in front of the endpoint must be skipped, not parsed.
func TestParseCheckerColumnPrefixedShareURIList(t *testing.T) {
	result, err := Parse([]byte("OK|http|http://user:pass@65.111.9.15:3129|65.111.9.15\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 1 {
		t.Fatalf("nodes = %d, failures = %+v", len(result.Nodes), result.Failures)
	}
	if result.Nodes[0].Canonical["username"] != "user" || result.Nodes[0].Canonical["port"] != 3129 {
		t.Fatalf("unexpected node: %#v", result.Nodes[0].Canonical)
	}
}

// The repair must not fire on a URI that parses: an intentional node name is
// data, and discarding it in favour of a trimmed endpoint would be a silent
// quality loss on every ordinary subscription line.
func TestParseProxyListRepairPreservesParsableNodeName(t *testing.T) {
	result, err := Parse([]byte("socks5://1.2.3.4:1080#HK-01\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].DisplayName != "HK-01" {
		t.Fatalf("node name was not preserved: %+v", result.Nodes)
	}
}

// Detection must not steal documents from other formats.
func TestParseProxyListDoesNotClaimOtherFormats(t *testing.T) {
	cases := map[string]string{
		"clash-yaml":  "proxies:\n  - {name: a, type: http, server: 1.2.3.4, port: 8080}\n",
		"uri-list":    "vless://id@example.com:443?security=tls#HK\n",
		"sing-box":    "{\"outbounds\":[{\"type\":\"socks\",\"tag\":\"a\",\"server\":\"1.2.3.4\",\"server_port\":1080}]}",
		"free-text":   "this is not a proxy list at all, just prose written by a human\n",
		"empty-lines": "\n\n\n",
	}
	for name, content := range cases {
		result, err := Parse([]byte(content))
		if name == "clash-yaml" || name == "uri-list" || name == "sing-box" {
			if err != nil {
				t.Fatalf("%s: Parse() error = %v", name, err)
			}
			if result.DetectedFormat == ProxyListFormat {
				t.Fatalf("%s was claimed as a proxy list", name)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: expected a rejection, got format %q with %d node(s)", name, result.DetectedFormat, len(result.Nodes))
		}
	}
}

// A list is accepted only when most of its lines really resolve; a handful of
// endpoints buried in prose is not a proxy list.
func TestParseProxyListRequiresMajorityOfEndpoints(t *testing.T) {
	prose := "1.2.3.4:8080\n"
	for index := 0; index < 9; index++ {
		prose += "here is some sentence that is certainly not an endpoint\n"
	}
	if _, err := Parse([]byte(prose)); err == nil {
		t.Fatal("a document that is 10% endpoints was accepted as a proxy list")
	}
}

func TestParseProxyListReportsUnusableLines(t *testing.T) {
	result, err := Parse([]byte("1.2.3.4:8080\n1.2.3.4:99999\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 1 || len(result.Failures) != 1 {
		t.Fatalf("nodes = %d, failures = %+v", len(result.Nodes), result.Failures)
	}
	// A bad port must be reported, not silently turned into a node.
	if result.Failures[0].Index != 1 {
		t.Fatalf("failure index = %d, want 1", result.Failures[0].Index)
	}
}

// Bare list endpoints are deduplicated by the same fingerprint as every other
// format, because they go through the same finalize() path.
func TestParseProxyListDeduplicatesByIdentity(t *testing.T) {
	result, err := Parse([]byte("1.2.3.4:8080\n1.2.3.4:8080\n5.6.7.8:8080\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	unique := map[string]struct{}{}
	for _, node := range result.Nodes {
		unique[node.Fingerprint] = struct{}{}
	}
	if len(unique) != 2 {
		t.Fatalf("distinct fingerprints = %d, want 2", len(unique))
	}
}
