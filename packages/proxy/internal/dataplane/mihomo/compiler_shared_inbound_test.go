package mihomo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
	"gopkg.in/yaml.v3"
)

// sharedInboundRepository is the smallest repository that satisfies the
// compiler for a shared-inbound scenario: two proxy groups, one credential per
// member and the matching aggregate rows.
type sharedInboundRepository struct {
	groups    []store.ProxyGroupRecord
	listeners []store.ListenerRecord
}

func (repository sharedInboundRepository) ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error) {
	return repository.groups, nil
}
func (repository sharedInboundRepository) ListListeners(context.Context) ([]store.ListenerRecord, error) {
	return repository.listeners, nil
}
func (repository sharedInboundRepository) ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error) {
	return nil, nil
}
func (repository sharedInboundRepository) ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error) {
	return nil, nil
}
func (repository sharedInboundRepository) ListResidentialClientRoutes(context.Context) ([]store.ResidentialClientRouteRecord, error) {
	return nil, nil
}
func (repository sharedInboundRepository) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return nil, nil
}
func (repository sharedInboundRepository) GetMetadata(context.Context, string) (string, error) {
	// Every shared-inbound test runs with the default (shared) settings.
	return "", store.ErrNotFound
}

// plaintextCipher returns the stored credential unchanged so the test can pin
// the credentials without an encryption round-trip.
type plaintextCipher struct{}

func (plaintextCipher) Open(content, _ []byte) ([]byte, error) { return content, nil }

func memberListener(t *testing.T, id, name, kind, groupID, username, password, owner string, port int) store.ListenerRecord {
	t.Helper()
	auth, err := json.Marshal(listener.Auth{Username: username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	transport := "{}"
	if kind == "vless" || kind == "vmess" || kind == "trojan" {
		encoded, encodeErr := json.Marshal(listener.Transport{Type: "ws", WSPath: listener.SharedInboundRoutePath()})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		transport = string(encoded)
	}
	return store.ListenerRecord{
		ID:                  id,
		Name:                name,
		Kind:                kind,
		BindAddress:         "127.0.0.1",
		Port:                port,
		ProxyGroupID:        groupID,
		AuthMode:            "userpass",
		AuthConfigEncrypted: auth,
		TransportJSON:       transport,
		PublicEndpointJSON:  "{}",
		SharedInbound:       owner,
		Enabled:             true,
	}
}

func aggregateListener(id, kind, owner string, port int) store.ListenerRecord {
	transport := "{}"
	if kind == "vless" || kind == "vmess" || kind == "trojan" {
		encoded, _ := json.Marshal(listener.Transport{Type: "ws", WSPath: listener.SharedInboundRoutePath()})
		transport = string(encoded)
	}
	return store.ListenerRecord{
		ID:                 id,
		Name:               "hx-shared-inbound-" + owner + "-" + kind,
		Kind:               kind,
		BindAddress:        "127.0.0.1",
		Port:               port,
		ProxyGroupID:       "group-a",
		AuthMode:           "none",
		TransportJSON:      transport,
		PublicEndpointJSON: "{}",
		SharedInbound:      owner,
		Enabled:            true,
	}
}

func compileShared(t *testing.T, repository sharedInboundRepository) map[string]any {
	t.Helper()
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return decodeDocument(t, compiled.YAML)
}

// decodeDocument reads a compiled configuration back into the generic shape the
// assertions inspect, so a test never depends on YAML key order.
func decodeDocument(t *testing.T, config []byte) map[string]any {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(config, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func listenersOf(t *testing.T, document map[string]any) []map[string]any {
	t.Helper()
	raw, ok := document["listeners"].([]any)
	if !ok {
		t.Fatalf("listeners = %T, want a list", document["listeners"])
	}
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("listener entry = %T, want a mapping", item)
		}
		result = append(result, entry)
	}
	return result
}

func rulesOf(t *testing.T, document map[string]any) []string {
	t.Helper()
	raw, ok := document["rules"].([]any)
	if !ok {
		t.Fatalf("rules = %T, want a list", document["rules"])
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("rule = %T, want a string", item)
		}
		result = append(result, text)
	}
	return result
}

func TestCompileSharedInboundPublishesOneListenerPerFamily(t *testing.T) {
	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{
			testGroup("group-a"),
			testGroup("group-b"),
		},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 7890),
			aggregateListener("agg-vless", "vless", listener.SharedInboundWebSocketOwner, 7891),
			memberListener(t, "member-a", "service-a", "mixed", "group-a", "svc-group-a", "pass-a", listener.SharedInboundStandardOwner, 7890),
			memberListener(t, "member-b", "service-b", "mixed", "group-b", "svc-group-b", "pass-b", listener.SharedInboundStandardOwner, 7890),
			memberListener(t, "member-c", "service-c", "vless", "group-a", "svc-group-a-c", "11111111-1111-1111-1111-111111111111", listener.SharedInboundWebSocketOwner, 7891),
		},
	}
	document := compileShared(t, repository)
	listeners := listenersOf(t, document)
	// The two aggregate rows compile; the members must NOT become ports of
	// their own, which is the whole point of the shared inbound.
	if len(listeners) != 2 {
		t.Fatalf("listeners = %d, want 2 (one mixed + one vless): %+v", len(listeners), listeners)
	}
	byName := make(map[string]map[string]any, len(listeners))
	for _, entry := range listeners {
		byName[entry["name"].(string)] = entry
	}
	mixed, ok := byName[SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")]
	if !ok {
		t.Fatalf("missing shared mixed listener: %+v", byName)
	}
	if mixed["type"] != "mixed" || mixed["port"] != 7890 || mixed["listen"] != "127.0.0.1" {
		t.Fatalf("shared mixed listener = %+v", mixed)
	}
	users, ok := mixed["users"].([]any)
	if !ok || len(users) != 2 {
		t.Fatalf("shared mixed users = %+v, want two members", mixed["users"])
	}
	// Members are keyed by the group id so one service can never reach another
	// service's egress.
	first := users[0].(map[string]any)
	if first["username"] != "svc-group-a" || first["password"] != "pass-a" {
		t.Fatalf("first shared member = %+v", first)
	}
	if _, leaked := first["uuid"]; leaked {
		t.Fatalf("mixed member leaked a uuid: %+v", first)
	}

	// Membership must be routed before any site rule, and by IN-USER.
	rules := rulesOf(t, document)
	expectedA := "AND,((IN-NAME," + SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed") + "),(IN-USER," + first["username"].(string) + ")),group-a"
	expectedB := "AND,((IN-NAME," + SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed") + "),(IN-USER," + users[1].(map[string]any)["username"].(string) + ")),group-b"
	if indexOfRule(rules, expectedA) < 0 || indexOfRule(rules, expectedB) < 0 {
		t.Fatalf("shared member rules missing: %v", rules)
	}
	if indexOfRule(rules, expectedA) > indexOfRule(rules, "MATCH,DIRECT") ||
		indexOfRule(rules, expectedB) > indexOfRule(rules, "MATCH,DIRECT") {
		t.Fatalf("shared member rules must precede the fallback: %v", rules)
	}
}

// TestCompileSharedInboundUsesTheClientCredentialUsername pins the regression
// found in live testing: the compiled users list and the IN-USER rule must use
// the exact username the client authenticates with. A synthetic, server-derived
// name made every published subscription return 403.
func TestCompileSharedInboundUsesTheClientCredentialUsername(t *testing.T) {
	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{testGroup("group-a")},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 7890),
			// The administrator picked a name unrelated to the group id.
			memberListener(t, "member-a", "service-a", "mixed", "group-a", "Alice", "s3cret", listener.SharedInboundStandardOwner, 7890),
		},
	}
	document := compileShared(t, repository)
	listeners := listenersOf(t, document)
	if len(listeners) != 1 {
		t.Fatalf("listeners = %d, want 1", len(listeners))
	}
	users := listeners[0]["users"].([]any)
	user := users[0].(map[string]any)
	if user["username"] != "Alice" {
		t.Fatalf("compiled username = %v, want the client credential Alice", user["username"])
	}
	if user["password"] != "s3cret" {
		t.Fatalf("compiled password = %v", user["password"])
	}
	expected := "AND,((IN-NAME," + SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed") + "),(IN-USER,Alice)),group-a"
	if indexOfRule(rulesOf(t, document), expected) < 0 {
		t.Fatalf("IN-USER rule did not use the client credential: %v", rulesOf(t, document))
	}
}

// TestCompileSharedInboundRejectsDuplicateUsernames keeps the membership model
// sound: one username can only ever select one group.
func TestCompileSharedInboundRejectsDuplicateUsernames(t *testing.T) {
	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 7890),
			memberListener(t, "member-a", "service-a", "mixed", "group-a", "shared-name", "pass-a", listener.SharedInboundStandardOwner, 7890),
			memberListener(t, "member-b", "service-b", "mixed", "group-b", "shared-name", "pass-b", listener.SharedInboundStandardOwner, 7890),
		},
	}
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Compile(context.Background()); err == nil {
		t.Fatal("Compile() error = nil, want a duplicate-username error")
	}
}

func TestCompileSharedInboundDropsMembersWithoutCredentials(t *testing.T) {
	broken := memberListener(t, "member-broken", "service-broken", "mixed", "group-a", "svc-group-a", "pass", listener.SharedInboundStandardOwner, 7890)
	broken.AuthMode = "none"
	broken.AuthConfigEncrypted = nil
	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{testGroup("group-a")},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 7890),
			broken,
		},
	}
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Compile(context.Background()); err == nil {
		t.Fatal("Compile() error = nil, want a credential requirement")
	}
}

func TestCompileSharedInboundIgnoresMembersOfDisabledGroups(t *testing.T) {
	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{
			testGroup("group-a"),
			testGroup("group-off", false),
		},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 7890),
			memberListener(t, "member-a", "service-a", "mixed", "group-a", "svc-group-a", "pass", listener.SharedInboundStandardOwner, 7890),
			memberListener(t, "member-off", "service-off", "mixed", "group-off", "svc-group-off", "pass", listener.SharedInboundStandardOwner, 7890),
		},
	}
	document := compileShared(t, repository)
	rules := rulesOf(t, document)
	if indexOfRule(rules, "AND,((IN-NAME,"+SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")+"),(IN-USER,svc-group-off)),group-off") >= 0 {
		t.Fatalf("disabled group leaked a shared member rule: %v", rules)
	}
}

func TestCompileSharedInboundRoutesEveryWebSocketProtocol(t *testing.T) {
	groups := []store.ProxyGroupRecord{testGroup("group-a")}
	listeners := []store.ListenerRecord{}
	for _, kind := range []string{"vless", "vmess", "trojan"} {
		listeners = append(listeners, aggregateListener("agg-"+kind, kind, listener.SharedInboundWebSocketOwner, 7891))
		listeners = append(listeners, memberListener(t, "member-"+kind, "service-"+kind, kind, "group-a", "svc-group-a", uuidFor(kind), listener.SharedInboundWebSocketOwner, 7891))
	}
	document := compileShared(t, sharedInboundRepository{groups: groups, listeners: listeners})
	compiled := listenersOf(t, document)
	if len(compiled) != 3 {
		t.Fatalf("listeners = %d, want one per WebSocket protocol", len(compiled))
	}
	for _, entry := range compiled {
		kind := entry["type"].(string)
		if entry["ws-path"] != listener.SharedInboundRoutePath() {
			t.Fatalf("%s ws-path = %v, want %s", kind, entry["ws-path"], listener.SharedInboundRoutePath())
		}
	}
	rules := rulesOf(t, document)
	for _, kind := range []string{"vless", "vmess", "trojan"} {
		expected := "AND,((IN-NAME," + SharedListenerConfigName(listener.SharedInboundWebSocketOwner, kind) + "),(IN-USER,svc-group-a)),group-a"
		if indexOfRule(rules, expected) < 0 {
			t.Fatalf("%s member rule missing: %v", kind, rules)
		}
	}
}

func TestCompileSharedInboundDisabledKeepsDedicatedListeners(t *testing.T) {
	// An upgrade must not move ports: with the shared inbound disabled the
	// historical per-service listener is the only one compiled.
	repository := dedicatedRepository{
		settings: systemsettings.SharedInboundSettings{Mode: systemsettings.SharedInboundPerService},
		groups:   []store.ProxyGroupRecord{testGroup("group-a")},
		listeners: []store.ListenerRecord{{
			ID: "listener-1", Name: "service-a", Kind: "mixed", BindAddress: "127.0.0.1", Port: 7890,
			ProxyGroupID: "group-a", AuthMode: "none", TransportJSON: "{}", PublicEndpointJSON: "{}", Enabled: true,
		}},
	}
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(compiled.YAML, &document); err != nil {
		t.Fatal(err)
	}
	listeners := listenersOf(t, document)
	if len(listeners) != 1 || listeners[0]["name"] != listenerConfigName("listener-1") {
		t.Fatalf("dedicated listener missing: %+v", listeners)
	}
	if !strings.Contains(string(compiled.YAML), "proxy: group-a") {
		t.Fatalf("dedicated listener did not keep its proxy binding:\n%s", compiled.YAML)
	}
}

// dedicatedRepository lets a test pick the persisted global settings, which is
// what decides between the shared and per-service compile paths.
type dedicatedRepository struct {
	settings  systemsettings.SharedInboundSettings
	groups    []store.ProxyGroupRecord
	listeners []store.ListenerRecord
}

func (repository dedicatedRepository) ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error) {
	return repository.groups, nil
}
func (repository dedicatedRepository) ListListeners(context.Context) ([]store.ListenerRecord, error) {
	return repository.listeners, nil
}
func (repository dedicatedRepository) ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error) {
	return nil, nil
}
func (repository dedicatedRepository) ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error) {
	return nil, nil
}
func (repository dedicatedRepository) ListResidentialClientRoutes(context.Context) ([]store.ResidentialClientRouteRecord, error) {
	return nil, nil
}
func (repository dedicatedRepository) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return nil, nil
}
func (repository dedicatedRepository) GetMetadata(context.Context, string) (string, error) {
	settings := systemsettings.Default()
	settings.SharedInbound = repository.settings
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// testGroup builds a minimal enabled proxy group the compiler accepts.
func testGroup(id string, enabled ...bool) store.ProxyGroupRecord {
	active := true
	if len(enabled) > 0 {
		active = enabled[0]
	}
	return store.ProxyGroupRecord{
		ID: id, Name: id, Enabled: active, Strategy: "manual", SourceSpecJSON: "{}",
	}
}

func uuidFor(kind string) string {
	_ = kind
	return "11111111-1111-1111-1111-111111111111"
}

func indexOfRule(rules []string, wanted string) int {
	for index, rule := range rules {
		if rule == wanted {
			return index
		}
	}
	return -1
}
