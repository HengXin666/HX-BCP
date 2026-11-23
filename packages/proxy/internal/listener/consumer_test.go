package listener

import (
	"encoding/json"
	"strings"
	"testing"
)

// The programmatic node listing is a frozen contract: consumers branch on the
// protocol and transport enums, so an unexpected value must be a loud failure
// rather than a silent guess. See docs/CONSUMER_INTEGRATION_CONTRACT.md.
func TestConsumerPayloadFreezesProtocolAndTransportVocabulary(t *testing.T) {
	t.Parallel()

	export := NewShareExport(
		"香港专线", "mixed", "proxy.example.com", 7890,
		[]ShareNode{{Name: "香港专线-01", Auth: &Auth{Username: "svc-3f9c", Password: "secret"}}},
		Transport{},
		PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	)
	payload := export.ConsumerPayload("/sub/" + strings.Repeat("a", 32))

	// Optional fields (ws_path, server_name) only appear on the transport that
	// uses them, so the vocabulary check spans one payload of each transport.
	wsExport := NewShareExport(
		"香港专线", "vless", "proxy.example.com", 443,
		[]ShareNode{{Name: "香港专线-ws", Auth: &Auth{Password: "uuid-value"}}},
		Transport{WSPath: "/__hx-proxy__/shared"},
		PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal consumer payload: %v", err)
	}
	wsEncoded, err := json.Marshal(wsExport.ConsumerPayload("/sub/" + strings.Repeat("a", 32)))
	if err != nil {
		t.Fatalf("marshal ws consumer payload: %v", err)
	}
	vocabulary := string(encoded) + string(wsEncoded)
	for _, field := range ConsumerFields {
		if !strings.Contains(vocabulary, "\""+field+"\"") {
			t.Errorf("contract field %q never appears in any payload", field)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal consumer payload: %v", err)
	}
	if got := decoded["share_path"]; got != "/sub/"+strings.Repeat("a", 32) {
		t.Fatalf("share_path = %v", got)
	}
	urls, ok := decoded["subscription_urls"].(map[string]any)
	if !ok {
		t.Fatalf("subscription_urls is not an object: %T", decoded["subscription_urls"])
	}
	for _, format := range ConsumerSubscriptionFormats {
		if _, exists := urls[format]; !exists {
			t.Errorf("subscription_urls missing format %q", format)
		}
	}
	nodes, ok := decoded["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("nodes = %v", decoded["nodes"])
	}
	node, _ := nodes[0].(map[string]any)
	if protocol, _ := node["protocol"].(string); !containsString(ConsumerProtocols, protocol) {
		t.Errorf("protocol %v is outside the frozen vocabulary", node["protocol"])
	}
	if transport, _ := node["transport"].(string); !containsString(ConsumerTransports, transport) {
		t.Errorf("transport %v is outside the frozen vocabulary", node["transport"])
	}
}

// A WebSocket entry point is reached through the TLS reverse proxy and can
// never be handed to a browser as a proxy, so the listing must say so.
func TestConsumerPayloadMarksWebSocketNodesNotBrowserCompatible(t *testing.T) {
	t.Parallel()

	export := NewShareExport(
		"香港专线", "vless", "proxy.example.com", 443,
		[]ShareNode{{Name: "香港专线-ws", Auth: &Auth{Password: "uuid-value"}}},
		Transport{WSPath: "/__hx-proxy__/shared"},
		PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	)
	payload := export.ConsumerPayload("/sub/" + strings.Repeat("b", 32))
	if len(payload.Nodes) != 1 {
		t.Fatalf("nodes = %d", len(payload.Nodes))
	}
	node := payload.Nodes[0]
	if node.Transport != "ws" {
		t.Fatalf("transport = %q, want ws", node.Transport)
	}
	if node.BrowserCompatible {
		t.Fatal("a WebSocket node must not be reported as browser compatible")
	}
	if node.WSPath != "/__hx-proxy__/shared" {
		t.Fatalf("ws_path = %q", node.WSPath)
	}
	if !node.TLS || node.ServerName != "proxy.example.com" {
		t.Fatalf("tls = %v server_name = %q", node.TLS, node.ServerName)
	}
}

// A TLS endpoint must stay browser compatible: Chromium accepts an
// "https://host:port" --proxy-server and completes a request through it. This
// pins the verified behaviour so the "browsers cannot use HTTPS proxies"
// intuition (which is wrong for Chromium) is not re-applied as a fix.
func TestConsumerPayloadKeepsTLSTCPNodesBrowserCompatible(t *testing.T) {
	t.Parallel()

	export := NewShareExport(
		"香港专线", "mixed", "proxy.example.com", 7890,
		[]ShareNode{{Name: "香港专线-01", Auth: &Auth{Username: "svc-3f9c", Password: "secret"}}},
		Transport{},
		PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	)
	payload := export.ConsumerPayload("/sub/" + strings.Repeat("d", 32))
	if len(payload.Nodes) != 1 {
		t.Fatalf("nodes = %d", len(payload.Nodes))
	}
	node := payload.Nodes[0]
	if node.Transport != "tcp" || !node.TLS {
		t.Fatalf("transport = %q tls = %v, want tcp/true", node.Transport, node.TLS)
	}
	if !node.BrowserCompatible {
		t.Fatal("a TLS TCP node is browser compatible; Chromium completes requests through an https:// proxy")
	}
}

// Two reads of the same token must be byte-identical; consumers rely on the
// order for sharding and rotation, and a map-backed rendering would break it.
func TestConsumerPayloadOrderIsStable(t *testing.T) {
	t.Parallel()

	nodes := []ShareNode{
		{Name: "node-b", Auth: &Auth{Username: "u", Password: "p"}},
		{Name: "node-a", Auth: &Auth{Username: "u", Password: "p"}},
		{Name: "node-c", Auth: &Auth{Username: "u", Password: "p"}},
	}
	bundle := NewShareBundle("stable", []ShareExport{
		NewShareExport("stable", "mixed", "proxy.example.com", 7890, nodes, Transport{}, PublicEndpoint{}),
	})
	first, err := json.Marshal(bundle.ConsumerPayload("/sub/" + strings.Repeat("c", 32)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := json.Marshal(bundle.ConsumerPayload("/sub/" + strings.Repeat("c", 32)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("consumer payload is not deterministic:\n%s\n%s", first, second)
	}
	for index, want := range []string{"node-b", "node-a", "node-c"} {
		if !strings.Contains(string(first), want) {
			t.Fatalf("node %q missing", want)
		}
		_ = index
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
