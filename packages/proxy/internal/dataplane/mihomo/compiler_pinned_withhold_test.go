package mihomo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

// pinnedWithholdRepository serves the node records and listener rows a pinned
// listener compile needs. Its global settings decide which compile path the
// listeners take, so both can be exercised with one shape of document.
type pinnedWithholdRepository struct {
	settings  systemsettings.SharedInboundSettings
	nodes     []store.NodeConfigRecord
	listeners []store.ListenerRecord
}

func (r pinnedWithholdRepository) ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error) {
	return []store.ProxyGroupRecord{testGroup("group-a")}, nil
}
func (r pinnedWithholdRepository) ListListeners(context.Context) ([]store.ListenerRecord, error) {
	return r.listeners, nil
}
func (r pinnedWithholdRepository) ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error) {
	return r.nodes, nil
}
func (r pinnedWithholdRepository) ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error) {
	return nil, nil
}
func (r pinnedWithholdRepository) ListResidentialClientRoutes(context.Context) ([]store.ResidentialClientRouteRecord, error) {
	return nil, nil
}
func (r pinnedWithholdRepository) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return nil, nil
}
func (r pinnedWithholdRepository) GetMetadata(context.Context, string) (string, error) {
	settings := systemsettings.Default()
	settings.SharedInbound = r.settings
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func perServiceSettings() systemsettings.SharedInboundSettings {
	return systemsettings.SharedInboundSettings{Mode: systemsettings.SharedInboundPerService}
}

func pinnedListener(t *testing.T, id, name, nodeID string, port int) store.ListenerRecord {
	t.Helper()
	auth, err := json.Marshal(listener.Auth{Username: name, Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	return store.ListenerRecord{
		ID: id, Name: name, Kind: "mixed", BindAddress: "127.0.0.1", Port: port,
		ProxyGroupID: "group-a", NodeID: nodeID,
		AuthMode: "userpass", AuthConfigEncrypted: auth,
		TransportJSON: "{}", PublicEndpointJSON: "{}", Enabled: true,
	}
}

func compilePinnedWithhold(t *testing.T, repository pinnedWithholdRepository) Compiled {
	t.Helper()
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(context.Background())
	if err != nil {
		t.Fatalf("a stale node pin must not fail Compile: %v", err)
	}
	return compiled
}

// A pin whose node the document no longer carries leaves out that listener
// only: the healthy pin and the unpinned listener beside it still compile.
func TestCompileWithholdsListenerPinnedToMissingNode(t *testing.T) {
	t.Parallel()

	compiled := compilePinnedWithhold(t, pinnedWithholdRepository{
		settings: perServiceSettings(),
		nodes:    []store.NodeConfigRecord{chainNodeRecord(t, "node-live", "aaaaaaaaaaaaaaaa0000")},
		listeners: []store.ListenerRecord{
			pinnedListener(t, "listener-live", "svc-live", "node-live", 7901),
			pinnedListener(t, "listener-stale", "probe-resi-01", "node-retired", 7902),
			{ID: "listener-unpinned", Name: "svc-unpinned", Kind: "mixed", BindAddress: "127.0.0.1", Port: 7903,
				ProxyGroupID: "group-a", AuthMode: "none", TransportJSON: "{}", PublicEndpointJSON: "{}", Enabled: true},
		},
	})
	document := decodeDocument(t, compiled.YAML)
	want := WithheldEntry{ListenerID: "listener-stale", Reason: WithheldPinnedNodeUnavailable}
	if len(compiled.WithheldEntries) != 1 || compiled.WithheldEntries[0] != want {
		t.Fatalf("WithheldEntries = %+v, want exactly %+v", compiled.WithheldEntries, want)
	}
	endpoints := make(map[string]Endpoint, len(compiled.Endpoints))
	for _, endpoint := range compiled.Endpoints {
		endpoints[endpoint.ID] = endpoint
	}
	if _, served := endpoints["listener-stale"]; served {
		t.Fatalf("a withheld listener must not reserve an endpoint: %+v", compiled.Endpoints)
	}
	for _, id := range []string{"listener-live", "listener-unpinned"} {
		if _, served := endpoints[id]; !served {
			t.Fatalf("%s must still compile: %+v", id, compiled.Endpoints)
		}
	}

	names := make(map[string]bool)
	for _, entry := range listenersOf(t, document) {
		names[entry["name"].(string)] = true
	}
	if names[listenerConfigName("listener-stale")] {
		t.Fatalf("withheld listener is in the YAML listeners: %v", names)
	}
	for _, id := range []string{"listener-live", "listener-unpinned"} {
		if !names[listenerConfigName(id)] {
			t.Fatalf("%s missing from the YAML listeners: %v", id, names)
		}
	}
	stale := listenerConfigName("listener-stale")
	for _, rule := range rulesOf(t, document) {
		if strings.Contains(rule, stale) {
			t.Fatalf("withheld listener contributed a rule: %q", rule)
		}
	}
}

// A withheld pin must not fall back to the proxy group named on its row: a
// listener that names one exit must never publish another.
func TestCompileWithheldPinDoesNotFallBackToGroup(t *testing.T) {
	t.Parallel()

	compiled := compilePinnedWithhold(t, pinnedWithholdRepository{
		settings:  perServiceSettings(),
		nodes:     []store.NodeConfigRecord{chainNodeRecord(t, "node-live", "aaaaaaaaaaaaaaaa0000")},
		listeners: []store.ListenerRecord{pinnedListener(t, "listener-stale", "probe-resi-01", "node-retired", 7902)},
	})
	document := decodeDocument(t, compiled.YAML)
	if listeners := listenersOf(t, document); len(listeners) != 0 {
		t.Fatalf("listeners = %+v, want none", listeners)
	}
	// The group named on the row must not take over the listener either: the
	// only rule is the terminal one, and nothing names the withheld carrier.
	stale := listenerConfigName("listener-stale")
	for _, rule := range rulesOf(t, document) {
		if strings.Contains(rule, stale) {
			t.Fatalf("withheld listener left a rule behind: %q", rule)
		}
	}
	if strings.Contains(string(compiled.YAML), stale) {
		t.Fatalf("withheld listener is still referenced in the document:\n%s", compiled.YAML)
	}
}

// The aggregate families compile pinned members through a different path; a
// stale pin there is withheld there too, and a healthy one still works.
func TestCompileWithholdsSharedMemberPinnedToMissingNode(t *testing.T) {
	t.Parallel()

	live := memberListener(t, "member-live", "svc-live", "mixed", "group-a", "svc-live", "pass", listener.SharedInboundStandardOwner, 7890)
	live.NodeID = "node-live"
	stale := memberListener(t, "member-stale", "svc-stale", "mixed", "group-a", "svc-stale", "pass", listener.SharedInboundStandardOwner, 7890)
	stale.NodeID = "node-retired"

	// Shared mode is what puts these members on the aggregate carrier at all;
	// per-service settings would compile neither of them.
	compiled := compilePinnedWithhold(t, pinnedWithholdRepository{
		settings:  systemsettings.SharedInboundSettings{Mode: systemsettings.SharedInboundShared},
		nodes:     []store.NodeConfigRecord{chainNodeRecord(t, "node-live", "aaaaaaaaaaaaaaaa0000")},
		listeners: []store.ListenerRecord{live, stale},
	})
	want := WithheldEntry{ListenerID: "member-stale", Reason: WithheldPinnedNodeUnavailable}
	if len(compiled.WithheldEntries) != 1 || compiled.WithheldEntries[0] != want {
		t.Fatalf("WithheldEntries = %+v, want exactly %+v", compiled.WithheldEntries, want)
	}
	carrier := "IN-NAME," + SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")
	rules := rulesOf(t, decodeDocument(t, compiled.YAML))
	if indexOfRule(rules, "AND,(("+carrier+"),(IN-USER,svc-stale)),group-a") >= 0 {
		t.Fatalf("withheld member fell back to its group: %v", rules)
	}
	if indexOfRule(rules, "AND,(("+carrier+"),(IN-USER,svc-live)),"+nodeProxyName("aaaaaaaaaaaaaaaa0000")) < 0 {
		t.Fatalf("healthy pinned member is missing from the rules: %v", rules)
	}
}

// Only the pin is tolerated when stale; every other validation stays fatal.
func TestCompileKeepsUnpinnedMissingGroupFatal(t *testing.T) {
	t.Parallel()

	compiler, err := NewCompiler(pinnedWithholdRepository{
		settings: perServiceSettings(),
		listeners: []store.ListenerRecord{{
			ID: "listener-orphan", Name: "svc-orphan", Kind: "mixed", BindAddress: "127.0.0.1", Port: 7902,
			ProxyGroupID: "group-missing", AuthMode: "none", TransportJSON: "{}", PublicEndpointJSON: "{}", Enabled: true,
		}},
	}, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Compile(context.Background()); err == nil {
		t.Fatal("an unpinned listener with a missing group must still fail Compile")
	}
}
