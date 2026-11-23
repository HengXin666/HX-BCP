package mihomo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// chainRepository serves whole node records plus the candidates node_ids resolve
// against, which is all the compiler needs to exercise egress chaining.
type chainRepository struct {
	groups     []store.ProxyGroupRecord
	nodes      []store.NodeConfigRecord
	candidates []store.GroupNodeCandidate
}

func (r chainRepository) ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error) {
	return r.groups, nil
}
func (r chainRepository) ListListeners(context.Context) ([]store.ListenerRecord, error) {
	return nil, nil
}
func (r chainRepository) ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error) {
	return r.nodes, nil
}
func (r chainRepository) ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error) {
	return r.candidates, nil
}
func (r chainRepository) ListResidentialClientRoutes(context.Context) ([]store.ResidentialClientRouteRecord, error) {
	return nil, nil
}
func (r chainRepository) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return nil, nil
}
func (r chainRepository) GetMetadata(context.Context, string) (string, error) {
	return "", store.ErrNotFound
}

func chainNodeRecord(t *testing.T, id, fingerprint string) store.NodeConfigRecord {
	t.Helper()
	canonical, err := json.Marshal(map[string]any{"type": "http", "server": "127.0.0.1", "port": 1080})
	if err != nil {
		t.Fatal(err)
	}
	return store.NodeConfigRecord{
		ID:                       id,
		Fingerprint:              fingerprint,
		DisplayName:              id,
		CanonicalConfigEncrypted: canonical,
	}
}

func compileChainRepository(t *testing.T, repository chainRepository) (map[string]any, []byte) {
	t.Helper()
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return decodeDocument(t, compiled.YAML), compiled.YAML
}

func chainProxyByName(t *testing.T, document map[string]any, name string) map[string]any {
	t.Helper()
	raw, ok := document["proxies"].([]any)
	if !ok {
		t.Fatalf("proxies = %T, want a list", document["proxies"])
	}
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if ok && entry["name"] == name {
			return entry
		}
	}
	t.Fatalf("no proxy named %q in the compiled document", name)
	return nil
}

func chainGroupByName(t *testing.T, document map[string]any, name string) map[string]any {
	t.Helper()
	raw, ok := document["proxy-groups"].([]any)
	if !ok {
		t.Fatalf("proxy-groups = %T, want a list", document["proxy-groups"])
	}
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if ok && entry["name"] == name {
			return entry
		}
	}
	t.Fatalf("no group named %q in the compiled document", name)
	return nil
}

func chainMemberNames(t *testing.T, document map[string]any, groupName string) []string {
	t.Helper()
	raw, ok := chainGroupByName(t, document, groupName)["proxies"].([]any)
	if !ok {
		t.Fatalf("group %q has no proxies list", groupName)
	}
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		names = append(names, item.(string))
	}
	return names
}

// A chained group must not carry dialer-proxy itself: Mihomo rejects that shape
// and only logs it, which silently turns the chain into a direct dial. Instead
// each member is emitted as a derived copy that dials through the other group.
func TestCompileDerivesChainedMemberProxy(t *testing.T) {
	t.Parallel()

	nodeA := chainNodeRecord(t, "node-a", "aaaaaaaaaaaaaaaa0000")
	nodeB := chainNodeRecord(t, "node-b", "bbbbbbbbbbbbbbbb0000")
	repository := chainRepository{
		groups: []store.ProxyGroupRecord{
			{ID: "group-upstream", Name: "upstream", Strategy: "manual", Enabled: true, SourceSpecJSON: `{"node_ids":["node-a"]}`},
			{ID: "group-exit", Name: "exit", Strategy: "manual", Enabled: true,
				SourceSpecJSON: `{"node_ids":["node-b"]}`, DialerProxyGroupID: "group-upstream"},
		},
		nodes: []store.NodeConfigRecord{nodeA, nodeB},
		candidates: []store.GroupNodeCandidate{
			{NodeConfigRecord: nodeA},
			{NodeConfigRecord: nodeB},
		},
	}
	document, _ := compileChainRepository(t, repository)

	derivedName := "hx-node-bbbbbbbbbbbbbbbb" + derivedProxySeparator + "group-exit"
	derived := chainProxyByName(t, document, derivedName)
	if derived["dialer-proxy"] != "upstream" {
		t.Fatalf("derived dialer-proxy = %v, want upstream", derived["dialer-proxy"])
	}
	if got := chainMemberNames(t, document, "exit"); len(got) != 1 || got[0] != derivedName {
		t.Fatalf("exit members = %v, want [%s]", got, derivedName)
	}
	// The upstream group is not itself chained, so its member stays the plain
	// node name; only the chaining group gets a derived member.
	if got := chainMemberNames(t, document, "upstream"); len(got) != 1 || got[0] != "hx-node-aaaaaaaaaaaaaaaa" {
		t.Fatalf("upstream members = %v, want the plain node name", got)
	}
	// The group must never carry the rejected group-level key.
	if _, exists := chainGroupByName(t, document, "exit")["dialer-proxy"]; exists {
		t.Fatal("a proxy group must not carry dialer-proxy; Mihomo ignores it and degrades to direct")
	}
}

// Arbitrary depth: a derived member must reference the dialer GROUP name, so the
// next hop resolves through that group's own derived members.
func TestCompileChainsThroughTwoHops(t *testing.T) {
	t.Parallel()

	nodeA := chainNodeRecord(t, "node-a", "aaaaaaaaaaaaaaaa0000")
	nodeB := chainNodeRecord(t, "node-b", "bbbbbbbbbbbbbbbb0000")
	nodeC := chainNodeRecord(t, "node-c", "cccccccccccccccc0000")
	repository := chainRepository{
		groups: []store.ProxyGroupRecord{
			{ID: "g1", Name: "hop1", Strategy: "manual", Enabled: true, SourceSpecJSON: `{"node_ids":["node-a"]}`},
			{ID: "g2", Name: "hop2", Strategy: "manual", Enabled: true,
				SourceSpecJSON: `{"node_ids":["node-b"]}`, DialerProxyGroupID: "g1"},
			{ID: "g3", Name: "final", Strategy: "manual", Enabled: true,
				SourceSpecJSON: `{"node_ids":["node-c"]}`, DialerProxyGroupID: "g2"},
		},
		nodes: []store.NodeConfigRecord{nodeA, nodeB, nodeC},
		candidates: []store.GroupNodeCandidate{
			{NodeConfigRecord: nodeA}, {NodeConfigRecord: nodeB}, {NodeConfigRecord: nodeC},
		},
	}
	document, _ := compileChainRepository(t, repository)

	mid := chainProxyByName(t, document, "hx-node-bbbbbbbbbbbbbbbb"+derivedProxySeparator+"g2")
	if mid["dialer-proxy"] != "hop1" {
		t.Fatalf("mid dialer-proxy = %v, want hop1", mid["dialer-proxy"])
	}
	final := chainProxyByName(t, document, "hx-node-cccccccccccccccc"+derivedProxySeparator+"g3")
	if final["dialer-proxy"] != "hop2" {
		t.Fatalf("final dialer-proxy = %v, want hop2", final["dialer-proxy"])
	}
	// final -> hop2 -> hop1: hop2's own member is itself derived through hop1, so
	// the second hop resolves without concatenating a second suffix onto one name.
	if got := chainMemberNames(t, document, "hop2"); len(got) != 1 || got[0] != mid["name"] {
		t.Fatalf("hop2 members = %v, want the mid derived name", got)
	}
}

// Identical input must produce byte-identical output so recompiles show a stable
// diff (AGENTS section 6). Group iteration order must not leak into the result.
func TestCompileChainedDocumentIsByteStable(t *testing.T) {
	t.Parallel()

	nodeA := chainNodeRecord(t, "node-a", "aaaaaaaaaaaaaaaa0000")
	nodeB := chainNodeRecord(t, "node-b", "bbbbbbbbbbbbbbbb0000")
	repository := chainRepository{
		groups: []store.ProxyGroupRecord{
			{ID: "g1", Name: "hop1", Strategy: "manual", Enabled: true, SourceSpecJSON: `{"node_ids":["node-a"]}`},
			{ID: "g2", Name: "hop2", Strategy: "manual", Enabled: true,
				SourceSpecJSON: `{"node_ids":["node-b"]}`, DialerProxyGroupID: "g1"},
		},
		nodes:      []store.NodeConfigRecord{nodeA, nodeB},
		candidates: []store.GroupNodeCandidate{{NodeConfigRecord: nodeA}, {NodeConfigRecord: nodeB}},
	}
	_, first := compileChainRepository(t, repository)
	_, second := compileChainRepository(t, repository)
	if string(first) != string(second) {
		t.Fatal("compiling the same chained input twice produced different YAML")
	}
}

// A node already carrying a provider dialer cannot take a second egress path.
// Honouring the group chain would silently drop the provider hop.
func TestCompileRejectsChainOverNodeWithOwnDialer(t *testing.T) {
	t.Parallel()

	nodeB := chainNodeRecord(t, "node-b", "bbbbbbbbbbbbbbbb0000")
	repository := chainRepository{
		groups: []store.ProxyGroupRecord{
			{ID: "g1", Name: "hop1", Strategy: "manual", Enabled: true, SourceSpecJSON: `{"node_ids":["node-a"]}`},
			{ID: "g2", Name: "hop2", Strategy: "manual", Enabled: true,
				SourceSpecJSON: `{"node_ids":["node-b"]}`, DialerProxyGroupID: "g1"},
		},
		nodes:      []store.NodeConfigRecord{nodeB},
		candidates: []store.GroupNodeCandidate{{NodeConfigRecord: nodeB}},
	}
	derived := newDerivedProxySet()
	_, err := derived.derive(compiledNode{
		Name:   "hx-node-bbbbbbbbbbbbbbbb",
		Config: map[string]any{"name": "hx-node-bbbbbbbbbbbbbbbb", "dialer-proxy": "provider-hop"},
	}, "g2", "hop2", "hop1")
	if err == nil || !strings.Contains(err.Error(), "two egress paths") {
		t.Fatalf("derive() error = %v, want a two-egress-paths error", err)
	}
	_ = repository
}

func TestValidateGroupDialerChainsRejectsMissingAndCyclicTargets(t *testing.T) {
	t.Parallel()

	missing := []store.ProxyGroupRecord{
		{ID: "g1", Name: "hop1", Enabled: true, SourceSpecJSON: `{"node_ids":["n"]}`, DialerProxyGroupID: "nope"},
	}
	if err := validateGroupDialerChains(missing); err == nil {
		t.Fatal("a missing dialer target must fail the compile, not degrade to a direct dial")
	}

	// A chain closed into a loop is not catchable by Mihomo's loader, so this is
	// the only place that can refuse it.
	cyclic := []store.ProxyGroupRecord{
		{ID: "g1", Name: "hop1", Enabled: true, SourceSpecJSON: `{"node_ids":["n"]}`, DialerProxyGroupID: "g2"},
		{ID: "g2", Name: "hop2", Enabled: true, SourceSpecJSON: `{"node_ids":["n"]}`, DialerProxyGroupID: "g1"},
	}
	if err := validateGroupDialerChains(cyclic); err == nil {
		t.Fatal("a cyclic egress chain must be rejected")
	}

	self := []store.ProxyGroupRecord{
		{ID: "g1", Name: "hop1", Enabled: true, SourceSpecJSON: `{"node_ids":["n"]}`, DialerProxyGroupID: "g1"},
	}
	if err := validateGroupDialerChains(self); err == nil {
		t.Fatal("a group dialling through itself must be rejected")
	}

	direct := []store.ProxyGroupRecord{
		{ID: "g1", Name: "hop1", Enabled: true, SourceSpecJSON: `{"node_ids":["n"]}`},
		{ID: "g2", Name: "hop2", Enabled: true, SourceSpecJSON: `{"node_ids":["n"],"include_direct":true}`, DialerProxyGroupID: "g1"},
	}
	if err := validateGroupDialerChains(direct); err == nil {
		t.Fatal("a chained group that also includes DIRECT must be rejected")
	}
}
