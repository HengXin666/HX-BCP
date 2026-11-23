package mihomo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// bpbCanonical builds the canonical node shape that internal/nodeparse
// produces for a Cloudflare Worker panel (BPB-Worker-Panel) /sub/raw share
// URI: protocol identity at the top level plus a "query" map carrying the
// ws/tls/path/host/sni/fp/alpn parameters.
func bpbCanonical(protocol, server string, port int) map[string]any {
	canonical := map[string]any{
		"type":   protocol,
		"server": server,
		"port":   port,
		"query": map[string]any{
			"encryption": "none",
			"host":       "example.com",
			"type":       "ws",
			"security":   "tls",
			"path":       "/vl/abc?ed=2560",
			"sni":        "example.com",
			"fp":         "chrome",
			"alpn":       "http/1.1",
		},
	}
	if protocol == "vless" {
		canonical["uuid"] = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	} else {
		canonical["password"] = "secret-pass"
	}
	return canonical
}

func TestConvertNodeConfigBPBWorkerShape(t *testing.T) {
	t.Parallel()
	config, err := convertNodeConfig(bpbCanonical("vless", "104.16.0.1", 443))
	if err != nil {
		t.Fatalf("convertNodeConfig() error = %v", err)
	}
	if config["type"] != "vless" || config["server"] != "104.16.0.1" || config["port"] != 443 {
		t.Fatalf("base node fields = %v", config)
	}
	if config["uuid"] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("uuid = %v", config["uuid"])
	}
	if config["tls"] != true {
		t.Fatalf("tls = %v, want true from query security=tls", config["tls"])
	}
	if config["network"] != "ws" {
		t.Fatalf("network = %v, want ws", config["network"])
	}
	if config["servername"] != "example.com" {
		t.Fatalf("servername = %v, want example.com", config["servername"])
	}
	if config["client-fingerprint"] != "chrome" {
		t.Fatalf("client-fingerprint = %v, want chrome", config["client-fingerprint"])
	}
	alpn, ok := config["alpn"].([]string)
	if !ok || len(alpn) != 1 || alpn[0] != "http/1.1" {
		t.Fatalf("alpn = %#v (%T), want []string{\"http/1.1\"}", config["alpn"], config["alpn"])
	}
	options, ok := config["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("ws-opts missing: %#v", config["ws-opts"])
	}
	// 20260821 BPB/cfnew 早数据兼容：ed=2560 拆分为 max-early-data，
	// path 只保留 /vl/abc（否则 mihomo 1.19.30 连接 worker 会 reset）。
	if options["path"] != "/vl/abc" {
		t.Fatalf("ws-opts.path = %v, want /vl/abc", options["path"])
	}
	if options["max-early-data"] != 2560 {
		t.Fatalf("ws-opts.max-early-data = %v, want 2560", options["max-early-data"])
	}
	if options["early-data-header-name"] != "Sec-WebSocket-Protocol" {
		t.Fatalf("ws-opts.early-data-header-name = %v", options["early-data-header-name"])
	}
	headers, ok := options["headers"].(map[string]string)
	if !ok || headers["Host"] != "example.com" {
		t.Fatalf("ws-opts.headers = %#v, want Host header", options["headers"])
	}
	if _, exists := config["query"]; exists {
		t.Fatal("query map leaked into Mihomo node config")
	}
}

func TestConvertNodeConfigBPBTrojanShape(t *testing.T) {
	t.Parallel()
	config, err := convertNodeConfig(bpbCanonical("trojan", "104.16.0.2", 8443))
	if err != nil {
		t.Fatalf("convertNodeConfig() error = %v", err)
	}
	if config["type"] != "trojan" || config["password"] != "secret-pass" {
		t.Fatalf("trojan node = %v", config)
	}
	if config["tls"] != true || config["network"] != "ws" {
		t.Fatalf("trojan transport = %v", config)
	}
}

func TestConvertNodeConfigBPBWorkerNonTLSPort(t *testing.T) {
	t.Parallel()
	canonical := bpbCanonical("vless", "104.16.0.3", 80)
	// BPB emits security=none and omits sni/fp/alpn for non-HTTPS ports.
	canonical["query"] = map[string]any{
		"host":     "example.com",
		"type":     "ws",
		"security": "none",
		"path":     "/vl/def?ed=2560",
	}
	config, err := convertNodeConfig(canonical)
	if err != nil {
		t.Fatalf("convertNodeConfig() error = %v", err)
	}
	if _, exists := config["tls"]; exists {
		t.Fatalf("tls must be absent for security=none, got %v", config["tls"])
	}
	if _, exists := config["alpn"]; exists {
		t.Fatal("alpn must be absent when the URI carries no alpn parameter")
	}
	if config["network"] != "ws" {
		t.Fatalf("network = %v, want ws", config["network"])
	}
}

func TestNormalizeALPN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value any
		want  []string
		ok    bool
	}{
		{name: "scalar", value: "http/1.1", want: []string{"http/1.1"}, ok: true},
		{name: "comma list", value: "h2,http/1.1", want: []string{"h2", "http/1.1"}, ok: true},
		{name: "whitespace", value: " h2 , http/1.1 ", want: []string{"h2", "http/1.1"}, ok: true},
		{name: "string slice", value: []string{"http/1.1"}, want: []string{"http/1.1"}, ok: true},
		{name: "any slice", value: []any{"h2", "http/1.1"}, want: []string{"h2", "http/1.1"}, ok: true},
		{name: "empty scalar", value: "", ok: false},
		{name: "empty slice", value: []string{}, ok: false},
		{name: "non-string value", value: 42, ok: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := normalizeALPN(test.value)
			if ok != test.ok {
				t.Fatalf("normalizeALPN(%#v) ok = %v, want %v", test.value, ok, test.ok)
			}
			if !ok {
				return
			}
			if len(got) != len(test.want) {
				t.Fatalf("normalizeALPN(%#v) = %v, want %v", test.value, got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("normalizeALPN(%#v) = %v, want %v", test.value, got, test.want)
				}
			}
		})
	}
}

// TestBPBWorkerNodePassesMihomoValidation compiles the exact canonical shape a
// Cloudflare Worker panel (BPB-Worker-Panel) subscription produces and lets
// the installed Mihomo validate it with `mihomo -t`. This is the regression
// test for the scalar-alpn defect: BPB payloads carry alpn=http/1.1 as a single
// value, and Mihomo rejects anything but a slice.
func TestBPBWorkerNodePassesMihomoValidation(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	node, err := convertNodeConfig(bpbCanonical("vless", "104.16.0.1", 443))
	if err != nil {
		t.Fatalf("convertNodeConfig() error = %v", err)
	}
	node["name"] = "bpb-vless"
	document := map[string]any{
		"mixed-port": 0,
		"allow-lan":  false,
		"mode":       "rule",
		"log-level":  "info",
		"proxies":    []any{node},
		"proxy-groups": []any{map[string]any{
			"name":    "PROXY",
			"type":    "select",
			"proxies": []string{"bpb-vless"},
		}},
		"rules": []string{"MATCH,PROXY"},
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "active.yaml")
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, "-t", "-f", configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("mihomo -t rejected a BPB worker node:\n%s\nconfig:\n%s", output, encoded)
	}
	if !strings.Contains(string(output), "test is successful") {
		t.Fatalf("mihomo -t output did not confirm success:\n%s", output)
	}
}
