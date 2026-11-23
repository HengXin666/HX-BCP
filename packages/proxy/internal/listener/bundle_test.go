package listener

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestShareBundleRendersAllFormatsAcrossEndpoints(t *testing.T) {
	exports := []ShareExport{
		NewShareExport("edge", "vless", "proxy.example.com", 443, []ShareNode{{
			Name: "shared-name", Auth: &Auth{Password: "11111111-1111-4111-8111-111111111111"},
		}}, Transport{Type: "ws", WSPath: "/__hx-proxy__/edge"}, PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true}),
		NewShareExport("direct", "http", "203.0.113.10", 18443, []ShareNode{{
			Name: "shared-name", Auth: &Auth{Username: "client", Password: "secret"},
		}}, Transport{}, PublicEndpoint{}),
	}
	bundle := NewShareBundle("HX-Pxy", exports)
	if bundle.NodeCount() != 2 || bundle.Exports[1].Nodes[0].Name != "shared-name (2)" {
		t.Fatalf("bundle names = %+v", bundle.Exports)
	}

	clashBody, _, _, err := bundle.Render("clash")
	if err != nil {
		t.Fatal(err)
	}
	var clash struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(clashBody), &clash); err != nil {
		t.Fatal(err)
	}
	if len(clash.Proxies) != 2 || clash.Proxies[0]["type"] != "vless" || clash.Proxies[1]["type"] != "http" {
		t.Fatalf("clash proxies = %+v", clash.Proxies)
	}

	singBoxBody, _, _, err := bundle.Render("sing-box")
	if err != nil {
		t.Fatal(err)
	}
	var singBox struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(singBoxBody), &singBox); err != nil {
		t.Fatal(err)
	}
	if len(singBox.Outbounds) != 2 {
		t.Fatalf("sing-box outbounds = %+v", singBox.Outbounds)
	}

	uriBody, _, _, err := bundle.Render("uri")
	if err != nil || !strings.Contains(uriBody, "vless://") || !strings.Contains(uriBody, "http://") {
		t.Fatalf("uri render = %q, %v", uriBody, err)
	}
	v2rayNBody, _, _, err := bundle.Render("v2rayn")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(v2rayNBody)
	if err != nil || string(decoded) != uriBody {
		t.Fatalf("v2rayn decoded = %q, %v", decoded, err)
	}
}
