package mihomo

import "testing"

func TestValidateNoListenerLoopRejectsLoopbackEndpoint(t *testing.T) {
	t.Parallel()
	proxies := []map[string]any{{"name": "self", "server": "127.0.0.1", "port": float64(7890)}}
	listeners := []map[string]any{{"name": "entry", "listen": "127.0.0.1", "port": 7890}}
	if err := validateNoListenerLoop(proxies, listeners); err == nil {
		t.Fatal("validateNoListenerLoop() error = nil, want rejection")
	}
}

func TestValidateNoListenerLoopAllowsDifferentPortOrRemoteHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		proxy map[string]any
	}{
		{name: "different port", proxy: map[string]any{"name": "local service", "server": "localhost", "port": 7891}},
		{name: "remote host", proxy: map[string]any{"name": "remote", "server": "proxy.example.com", "port": 7890}},
	}
	listeners := []map[string]any{{"name": "entry", "listen": "127.0.0.1", "port": 7890}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateNoListenerLoop([]map[string]any{test.proxy}, listeners); err != nil {
				t.Fatalf("validateNoListenerLoop() error = %v", err)
			}
		})
	}
}
