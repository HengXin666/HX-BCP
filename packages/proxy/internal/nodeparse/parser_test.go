package nodeparse

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestParseClashYAMLAndPreserveFailures(t *testing.T) {
	result, err := Parse([]byte(`
proxies:
  - name: HK-01
    type: vless
    server: hk.example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    tls: true
  - name: broken
    type: trojan
    server: broken.example.com
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.DetectedFormat != "clash-yaml" {
		t.Fatalf("format = %q", result.DetectedFormat)
	}
	if len(result.Nodes) != 1 || len(result.Failures) != 1 {
		t.Fatalf("nodes = %d, failures = %d", len(result.Nodes), len(result.Failures))
	}
	if result.Nodes[0].Protocol != "vless" || result.Nodes[0].DisplayName != "HK-01" {
		t.Fatalf("unexpected node: %+v", result.Nodes[0])
	}
	if result.Nodes[0].Fingerprint == "" {
		t.Fatal("fingerprint is empty")
	}
}

func TestFingerprintDoesNotDependOnDisplayName(t *testing.T) {
	first, err := Parse([]byte("vless://id@example.com:443?security=tls#first"))
	if err != nil {
		t.Fatalf("first Parse() error = %v", err)
	}
	second, err := Parse([]byte("vless://id@example.com:443?security=tls#renamed"))
	if err != nil {
		t.Fatalf("second Parse() error = %v", err)
	}
	if first.Nodes[0].Fingerprint != second.Nodes[0].Fingerprint {
		t.Fatalf("fingerprints differ: %s != %s", first.Nodes[0].Fingerprint, second.Nodes[0].Fingerprint)
	}
}

func TestParseBase64URIListAndVMess(t *testing.T) {
	vmessPayload, err := json.Marshal(map[string]any{
		"v":    "2",
		"ps":   "Tokyo",
		"add":  "jp.example.com",
		"port": "443",
		"id":   "22222222-2222-2222-2222-222222222222",
		"net":  "ws",
		"tls":  "tls",
	})
	if err != nil {
		t.Fatal(err)
	}
	list := "trojan://secret@sg.example.com:443#SG\nvmess://" + base64.RawStdEncoding.EncodeToString(vmessPayload)
	wrapped := base64.StdEncoding.EncodeToString([]byte(list))
	result, err := Parse([]byte(wrapped))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.DetectedFormat != "base64-uri-list" {
		t.Fatalf("format = %q", result.DetectedFormat)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %d", len(result.Nodes))
	}
}

func TestStandardURIUsesProtocolSpecificCredentialFields(t *testing.T) {
	vless, err := Parse([]byte("vless://11111111-1111-1111-1111-111111111111@example.com:443#vless"))
	if err != nil {
		t.Fatal(err)
	}
	if vless.Nodes[0].Canonical["uuid"] == nil || vless.Nodes[0].Canonical["username"] != nil {
		t.Fatalf("unexpected vless canonical fields: %#v", vless.Nodes[0].Canonical)
	}
	trojan, err := Parse([]byte("trojan://secret@example.com:443#trojan"))
	if err != nil {
		t.Fatal(err)
	}
	if trojan.Nodes[0].Canonical["password"] != "secret" || trojan.Nodes[0].Canonical["username"] != nil {
		t.Fatalf("unexpected trojan canonical fields: %#v", trojan.Nodes[0].Canonical)
	}
}

func TestParseShadowsocksSIP002(t *testing.T) {
	credentials := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:password"))
	result, err := Parse([]byte("ss://" + credentials + "@example.com:8388#SS"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].Protocol != "ss" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestUnsupportedURIIsRetainedAsFailure(t *testing.T) {
	result, err := Parse([]byte("unknown://example.com:1234#bad"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 0 || len(result.Failures) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Failures[0].Protocol != "unknown" {
		t.Fatalf("failure protocol = %q", result.Failures[0].Protocol)
	}
}

func TestParseSingBoxJSON(t *testing.T) {
	result, err := Parse([]byte(`{
  "outbounds": [
    {
      "type": "vless",
      "tag": "CF VLESS",
      "server": "edge.example.com",
      "server_port": 443,
      "uuid": "11111111-1111-1111-1111-111111111111",
      "tls": {"enabled": true, "server_name": "edge.example.com"},
      "transport": {"type": "ws", "path": "/proxy", "headers": {"Host": "edge.example.com"}}
    },
    {"type": "direct", "tag": "direct"}
  ]
}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if result.DetectedFormat != "sing-box-json" || len(result.Nodes) != 1 || len(result.Failures) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	node := result.Nodes[0]
	if node.Protocol != "vless" || node.DisplayName != "CF VLESS" || node.Canonical["network"] != "ws" {
		t.Fatalf("unexpected node: %+v", node)
	}
}

func TestNativeMihomoProtocolCompatibilityMatrix(t *testing.T) {
	content := []byte(`
proxies:
  - {name: AnyTLS, type: anytls, server: anytls.example.com, port: 443, password: secret}
  - {name: Hysteria, type: hysteria, server: hy.example.com, port: 443, auth-str: secret}
  - {name: Hysteria2, type: hysteria2, server: hy2.example.com, port: 443, password: secret}
  - {name: HTTP, type: http, server: http.example.com, port: 8080}
  - {name: Mieru, type: mieru, server: mieru.example.com, port: 443, username: user, password: secret}
  - {name: OpenVPN, type: openvpn, server: ovpn.example.com, port: 443, proto: tcp, cipher: AES-128-CBC, auth: SHA1, ca: "-----BEGIN CERTIFICATE-----"}
  - {name: ShadowTLS, type: shadow-tls, server: shadow.example.com, port: 443, password: secret}
  - {name: Snell, type: snell, server: snell.example.com, port: 443, psk: secret}
  - {name: SOCKS5, type: socks5, server: socks.example.com, port: 1080}
  - {name: SS, type: ss, server: ss.example.com, port: 8388, cipher: aes-128-gcm, password: secret}
  - {name: SSH, type: ssh, server: ssh.example.com, port: 22, username: user, password: secret}
  - {name: SSR, type: ssr, server: ssr.example.com, port: 8388, cipher: aes-128-ctr, password: secret, protocol: auth_sha1_v4, obfs: tls1.2_ticket_auth}
  - {name: Trojan, type: trojan, server: trojan.example.com, port: 443, password: secret}
  - {name: TUIC, type: tuic, server: tuic.example.com, port: 443, uuid: 11111111-1111-1111-1111-111111111111, password: secret}
  - {name: VLESS, type: vless, server: vless.example.com, port: 443, uuid: 11111111-1111-1111-1111-111111111111}
  - {name: VMess, type: vmess, server: vmess.example.com, port: 443, uuid: 11111111-1111-1111-1111-111111111111}
  - {name: WireGuard, type: wireguard, server: wg.example.com, port: 51820, private-key: private, public-key: public, ip: 172.16.0.2}
`)
	result, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 17 || len(result.Failures) != 0 {
		t.Fatalf("nodes = %d, failures = %+v", len(result.Nodes), result.Failures)
	}
	got := make([]string, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		got = append(got, node.Protocol)
	}
	slices.Sort(got)
	want := SupportedProtocols()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("protocols = %v, want %v", got, want)
	}
}

func TestParseMihomoProviderPayloadAndReferences(t *testing.T) {
	payload, err := Parse([]byte(`payload:
  - {name: provider-node, type: hysteria2, server: hy2.example.com, port: 443, password: secret}
`))
	if err != nil {
		t.Fatal(err)
	}
	if payload.DetectedFormat != "mihomo-provider-yaml" || len(payload.Nodes) != 1 {
		t.Fatalf("unexpected payload result: %+v", payload)
	}

	config, err := Parse([]byte(`
proxy-providers:
  inline:
    type: inline
    payload:
      - {name: inline-node, type: tuic, server: tuic.example.com, port: 443, uuid: id, password: secret}
  remote:
    type: http
    url: https://provider.example.com/nodes.yaml
    header:
      User-Agent: [HX-Test/1]
      X-Provider: token
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Nodes) != 1 || len(config.Providers) != 1 || config.Providers[0].Name != "remote" {
		t.Fatalf("unexpected config result: %+v", config)
	}
	if config.Providers[0].UserAgent != "HX-Test/1" || config.Providers[0].Headers["x-provider"] != "token" {
		t.Fatalf("provider headers were not normalized: %+v", config.Providers[0])
	}
}

func TestShareURICompatibilityMatrix(t *testing.T) {
	cases := []struct {
		uri      string
		protocol string
		field    string
		value    string
	}{
		{"hysteria://auth@hy.example.com:443?upmbps=20#HY", "hysteria", "auth-str", "auth"},
		{"hy2://secret@hy2.example.com:443?obfs=salamander#HY2", "hysteria2", "password", "secret"},
		{"tuic://id:secret@tuic.example.com:443?congestion_control=bbr#TUIC", "tuic", "uuid", "id"},
		{"anytls://secret@anytls.example.com:443#AnyTLS", "anytls", "password", "secret"},
		{"ssh://user:secret@ssh.example.com:22#SSH", "ssh", "username", "user"},
	}
	for _, test := range cases {
		t.Run(test.protocol, func(t *testing.T) {
			result, err := Parse([]byte(test.uri))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Nodes) != 1 || result.Nodes[0].Protocol != test.protocol || result.Nodes[0].Canonical[test.field] != test.value {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestProtocolAliasesNormalizeForMihomoCompiler(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		protocol string
		tls      bool
	}{
		{name: "HTTPS URI", content: "https://user:secret@http.example.com:443#HTTPS", protocol: "http", tls: true},
		{name: "SOCKS URI", content: "socks://user:secret@socks.example.com:1080#SOCKS", protocol: "socks5"},
		{name: "HTTPS YAML", content: "proxies:\n  - {name: HTTPS, type: https, server: http.example.com, port: 443}\n", protocol: "http", tls: true},
		{name: "SOCKS YAML", content: "proxies:\n  - {name: SOCKS, type: socks, server: socks.example.com, port: 1080}\n", protocol: "socks5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Parse([]byte(test.content))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Nodes) != 1 || result.Nodes[0].Protocol != test.protocol || result.Nodes[0].Canonical["type"] != test.protocol {
				t.Fatalf("unexpected alias result: %+v", result)
			}
			if tls, _ := result.Nodes[0].Canonical["tls"].(bool); tls != test.tls {
				t.Fatalf("tls = %v, want %v", tls, test.tls)
			}
		})
	}
}

// An OpenVPN outbound without its inline CA is not a usable node, and the data
// plane would reject it at load time. It must surface as a single node failure
// rather than being accepted (or taking the rest of the subscription down).
func TestParseOpenVPNRequiresInlineCA(t *testing.T) {
	result, err := Parse([]byte(`
proxies:
  - {name: NoCA, type: openvpn, server: ovpn.example.com, port: 443, proto: tcp, cipher: AES-128-CBC}
  - {name: Good, type: ss, server: ss.example.com, port: 8388, cipher: aes-128-gcm, password: secret}
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 1 || len(result.Failures) != 1 {
		t.Fatalf("nodes = %d, failures = %+v", len(result.Nodes), result.Failures)
	}
	if result.Failures[0].Name != "NoCA" || !strings.Contains(result.Failures[0].Reason, "inline ca") {
		t.Fatalf("unexpected failure: %+v", result.Failures[0])
	}
	if result.Nodes[0].Protocol != "ss" {
		t.Fatalf("surviving node = %q, want ss", result.Nodes[0].Protocol)
	}
}

// A full VPNGate-style node (inline ca/cert/key) must be accepted and must keep
// its certificate material in the canonical config, since the data plane needs
// it to build the tunnel.
func TestParseOpenVPNKeepsInlineMaterial(t *testing.T) {
	result, err := Parse([]byte(`
proxies:
  - name: HomeJP
    type: openvpn
    server: 219.100.37.96
    port: 443
    proto: tcp
    cipher: AES-128-CBC
    auth: SHA1
    ca: "-----BEGIN CERTIFICATE-----CA-----END CERTIFICATE-----"
    cert: "-----BEGIN CERTIFICATE-----CERT-----END CERTIFICATE-----"
    key: "-----BEGIN PRIVATE KEY-----KEY-----END PRIVATE KEY-----"
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 1 || len(result.Failures) != 0 {
		t.Fatalf("nodes = %d, failures = %+v", len(result.Nodes), result.Failures)
	}
	node := result.Nodes[0]
	if node.Protocol != "openvpn" {
		t.Fatalf("protocol = %q, want openvpn", node.Protocol)
	}
	for _, key := range []string{"ca", "cert", "key"} {
		if strings.TrimSpace(node.Canonical[key].(string)) == "" {
			t.Fatalf("canonical %s was dropped", key)
		}
	}
}
