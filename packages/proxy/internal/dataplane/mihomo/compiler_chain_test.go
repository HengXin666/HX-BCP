package mihomo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestDecryptNodeResolvesResidentialDialerProxyGroup(t *testing.T) {
	t.Parallel()

	canonical, err := json.Marshal(map[string]any{
		"type":                                 "http",
		"server":                               "proxy.example.com",
		"port":                                 2312,
		store.ResidentialDialerProxyGroupIDKey: "group-overseas",
	})
	if err != nil {
		t.Fatal(err)
	}
	compiler := &Compiler{cipher: benchmarkCipher{}}
	config, err := compiler.decryptNode(store.NodeConfigRecord{
		DisplayName:              "residential #01",
		Fingerprint:              "fingerprint-1",
		CanonicalConfigEncrypted: canonical,
	}, "hx-node-residential", map[string]string{"group-overseas": "overseas"})
	if err != nil {
		t.Fatalf("decryptNode() error = %v", err)
	}
	if config["dialer-proxy"] != "overseas" {
		t.Fatalf("dialer-proxy = %v, want overseas", config["dialer-proxy"])
	}
	if _, exists := config[store.ResidentialDialerProxyGroupIDKey]; exists {
		t.Fatal("internal dialer group id leaked into Mihomo node config")
	}
}

func TestConvertNodeConfigStripsWsPxySessionID(t *testing.T) {
	t.Parallel()
	config, err := convertNodeConfig(map[string]any{
		"type":                             "http",
		"server":                           "127.0.0.1",
		"port":                             24800,
		store.ResidentialWsPxySessionIDKey: "sess-1",
	})
	if err != nil {
		t.Fatalf("convertNodeConfig() error = %v", err)
	}
	if _, exists := config[store.ResidentialWsPxySessionIDKey]; exists {
		t.Fatal("hx-cf-wspxy session id leaked into Mihomo node config")
	}
	if config["server"] != "127.0.0.1" || config["type"] != "http" {
		t.Fatalf("config = %#v", config)
	}
}

func TestDecryptNodeRejectsMissingOrDisabledResidentialDialerGroup(t *testing.T) {
	t.Parallel()

	canonical, err := json.Marshal(map[string]any{
		"type": store.ResidentialDialerProxyGroupIDKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The canonical type is intentionally replaced below so the error comes from
	// resolving the group rather than node protocol validation.
	var values map[string]any
	if err := json.Unmarshal(canonical, &values); err != nil {
		t.Fatal(err)
	}
	values["type"] = "http"
	values["server"] = "proxy.example.com"
	values["port"] = 2312
	values[store.ResidentialDialerProxyGroupIDKey] = "missing-group"
	canonical, err = json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	compiler := &Compiler{cipher: benchmarkCipher{}}
	_, err = compiler.decryptNode(store.NodeConfigRecord{DisplayName: "residential", Fingerprint: "fp", CanonicalConfigEncrypted: canonical}, "node", map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "missing or disabled") {
		t.Fatalf("decryptNode() error = %v, want missing/disabled group error", err)
	}
}

func TestValidateResidentialDialerChainsRejectsDirectAndNestedCycles(t *testing.T) {
	t.Parallel()

	groups := []store.ProxyGroupRecord{
		{ID: "group-residential", Name: "residential", SourceSpecJSON: `{"node_ids":["node-residential"]}`},
		{ID: "group-upstream", Name: "upstream", SourceSpecJSON: `{"group_ids":["group-residential"]}`},
		{ID: "group-nested", Name: "nested", SourceSpecJSON: `{"group_ids":["group-upstream"]}`},
	}
	nodeRecords := []store.NodeConfigRecord{{ID: "node-residential", DisplayName: "residential #01"}}
	nodes := map[string]compiledNode{"node-residential": {Config: map[string]any{"dialer-proxy": "nested"}}}
	groupNames := map[string]string{
		"group-residential": "residential",
		"group-upstream":    "upstream",
		"group-nested":      "nested",
	}
	if err := validateResidentialDialerChains(groups, nodeRecords, nodes, newMemberResolver(nil, nil, time.Time{}), groupNames); err == nil {
		t.Fatal("validateResidentialDialerChains() = nil, want nested cycle error")
	}

	groups[1].SourceSpecJSON = `{"node_ids":["node-foreign"]}`
	groups[2].SourceSpecJSON = `{"node_ids":["node-foreign"]}`
	if err := validateResidentialDialerChains(groups, nodeRecords, nodes, newMemberResolver(nil, nil, time.Time{}), groupNames); err != nil {
		t.Fatalf("validateResidentialDialerChains() valid chain error = %v", err)
	}
}
