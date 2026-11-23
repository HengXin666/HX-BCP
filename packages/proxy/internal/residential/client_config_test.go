package residential

import (
	"strings"
	"testing"
)

func TestClashConfigRendersVLESSWebSocketSession(t *testing.T) {
	config, err := ClashConfig(ClientSession{
		SessionID:     "flow-01",
		ProxyUsername: "hx-session-flow-01",
		ProxyPassword: "550e8400-e29b-41d4-a716-446655440000",
		ProxyEndpoint: &ClientProxyEndpoint{
			Type:   "vless-ws",
			Server: "proxy.example.com",
			Port:   443,
			TLS:    true,
			SNI:    "proxy.example.com",
			Path:   "/__hx-proxy__/residential",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, expected := range []string{
		"type: vless",
		"server: proxy.example.com",
		"uuid: 550e8400-e29b-41d4-a716-446655440000",
		"path: /__hx-proxy__/residential",
		"name: HX-Residential-flow-01",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("config missing %q:\n%s", expected, text)
		}
	}
}

func TestClashConfigRequiresPublicEndpointAndCredential(t *testing.T) {
	if _, err := ClashConfig(ClientSession{ProxyPassword: "secret"}); err == nil {
		t.Fatal("missing public endpoint should fail")
	}
	if _, err := ClashConfig(ClientSession{
		ProxyEndpoint: &ClientProxyEndpoint{Type: "vless-ws", Server: "proxy.example.com", Port: 443},
	}); err == nil {
		t.Fatal("missing session credential should fail")
	}
}
