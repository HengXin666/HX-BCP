package mihomo

import (
	"encoding/json"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// An OpenVPN outbound is a level-3 device: it can only work if destination
// hostnames are resolved inside the tunnel. Mihomo's default fake-ip resolver
// would otherwise hand the 198.18.0.0/16 address to the tun device, which
// cannot route it. The compiler therefore injects a remote resolver, and this
// test pins that behaviour so a "useless but accepted" node cannot come back.
func TestConvertNodeConfigInjectsRemoteDNSForOpenVPN(t *testing.T) {
	t.Parallel()

	config, err := convertNodeConfig(map[string]any{
		"type":   "openvpn",
		"server": "219.100.37.96",
		"port":   443,
		"proto":  "tcp",
		"cipher": "AES-128-CBC",
		"auth":   "SHA1",
		"ca":     "-----BEGIN CERTIFICATE-----CA-----END CERTIFICATE-----",
	})
	if err != nil {
		t.Fatalf("convertNodeConfig() error = %v", err)
	}
	if config["type"] != "openvpn" {
		t.Fatalf("type = %v, want openvpn", config["type"])
	}
	if config["remote-dns-resolve"] != true {
		t.Fatalf("remote-dns-resolve = %v, want true", config["remote-dns-resolve"])
	}
	dns, ok := config["dns"].([]any)
	if !ok || len(dns) == 0 {
		t.Fatalf("dns = %#v, want a non-empty resolver list", config["dns"])
	}
}

// An operator-supplied resolver must win over the injected default.
func TestConvertNodeConfigKeepsExplicitOpenVPNDNS(t *testing.T) {
	t.Parallel()

	config, err := convertNodeConfig(map[string]any{
		"type":               "openvpn",
		"server":             "219.100.37.96",
		"port":               443,
		"ca":                 "-----BEGIN CERTIFICATE-----CA-----END CERTIFICATE-----",
		"remote-dns-resolve": false,
		"dns":                []any{"9.9.9.9"},
	})
	if err != nil {
		t.Fatalf("convertNodeConfig() error = %v", err)
	}
	if config["remote-dns-resolve"] != false {
		t.Fatalf("remote-dns-resolve = %v, want the explicit false", config["remote-dns-resolve"])
	}
	dns, _ := config["dns"].([]any)
	if len(dns) != 1 || dns[0] != "9.9.9.9" {
		t.Fatalf("dns = %#v, want the explicit [9.9.9.9]", config["dns"])
	}
}

// decryptNode must carry the openvpn config through the store round-trip
// (encrypted canonical -> emitted Mihomo node) with the resolver injected.
func TestDecryptNodeEmitsUsableOpenVPNNode(t *testing.T) {
	t.Parallel()

	canonical, err := json.Marshal(map[string]any{
		"type":   "openvpn",
		"server": "219.100.37.96",
		"port":   443,
		"proto":  "tcp",
		"cipher": "AES-128-CBC",
		"auth":   "SHA1",
		"ca":     "-----BEGIN CERTIFICATE-----CA-----END CERTIFICATE-----",
	})
	if err != nil {
		t.Fatal(err)
	}
	compiler := &Compiler{cipher: benchmarkCipher{}}
	config, err := compiler.decryptNode(store.NodeConfigRecord{
		DisplayName:              "VPNGate JP",
		Fingerprint:              "fp-ovpn",
		CanonicalConfigEncrypted: canonical,
	}, "hx-node-ovpn", map[string]string{})
	if err != nil {
		t.Fatalf("decryptNode() error = %v", err)
	}
	if config["type"] != "openvpn" || config["remote-dns-resolve"] != true {
		t.Fatalf("emitted node = %#v", config)
	}
	if config["name"] != "hx-node-ovpn" {
		t.Fatalf("name = %v", config["name"])
	}
}
