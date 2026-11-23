package mihomo

import (
	"encoding/json"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestCompileListenerNormalizesLegacyWebSocketPath(t *testing.T) {
	t.Parallel()

	compiler := &Compiler{cipher: benchmarkCipher{}}
	config, err := compiler.compileListener(store.ListenerRecord{
		ID: "listener-edge", Name: "edge", Kind: "vless", BindAddress: "127.0.0.1", Port: 18088,
		TransportJSON: `{"type":"ws","ws_path":"/edge"}`,
	}, "edge-group", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := config["ws-path"]; got != listener.WebSocketPathPrefix+"edge" {
		t.Fatalf("compiled ws-path = %v, want %sedge", got, listener.WebSocketPathPrefix)
	}
}

func TestEdgeRouteKeyNormalizesPathAndIncludesPublicHost(t *testing.T) {
	t.Parallel()

	transport, err := json.Marshal(listener.Transport{Type: "ws", WSPath: "/edge"})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := json.Marshal(listener.PublicEndpoint{Host: "Proxy.Example.com", Port: 443, TLS: true})
	if err != nil {
		t.Fatal(err)
	}
	key, err := edgeRouteKey(store.ListenerRecord{
		Name: "edge", Kind: "vless", TransportJSON: string(transport), PublicEndpointJSON: string(endpoint),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "proxy.example.com\x00" + listener.WebSocketPathPrefix + "edge"
	if key != want {
		t.Fatalf("edgeRouteKey() = %q, want %q", key, want)
	}
}
