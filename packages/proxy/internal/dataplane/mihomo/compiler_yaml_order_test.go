package mihomo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/routingrules"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

// yamlOrderRepository serves one routing document and, optionally, TUN settings.
type yamlOrderRepository struct {
	routes    routingrules.Config
	settings  string
	groups    []store.ProxyGroupRecord
	listeners []store.ListenerRecord
	clients   []store.ResidentialClientRouteRecord
	nodes     []store.NodeConfigRecord
}

func (repository yamlOrderRepository) ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error) {
	return repository.groups, nil
}
func (repository yamlOrderRepository) ListListeners(context.Context) ([]store.ListenerRecord, error) {
	return repository.listeners, nil
}
func (repository yamlOrderRepository) ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error) {
	return repository.nodes, nil
}
func (repository yamlOrderRepository) ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error) {
	return nil, nil
}
func (repository yamlOrderRepository) ListResidentialClientRoutes(context.Context) ([]store.ResidentialClientRouteRecord, error) {
	return repository.clients, nil
}
func (repository yamlOrderRepository) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return nil, nil
}
func (repository yamlOrderRepository) GetMetadata(_ context.Context, key string) (string, error) {
	switch key {
	case systemsettings.MetadataKey:
		if repository.settings == "" {
			return "", store.ErrNotFound
		}
		return repository.settings, nil
	case routingrules.MetadataKey:
		encoded, err := json.Marshal(repository.routes)
		return string(encoded), err
	default:
		return "", store.ErrNotFound
	}
}

func yamlSiteRule() routingrules.RuleSet {
	return routingrules.RuleSet{
		ID: "yaml-01", Name: "yaml", Enabled: true, Priority: 10,
		Action: routingrules.Action{Type: "direct"},
		Rules:  []routingrules.Rule{{Type: "domain", Value: "keep.example"}},
	}
}

func browserListeners(t *testing.T) []store.ListenerRecord {
	t.Helper()
	return []store.ListenerRecord{
		{
			ID: "listener-browser", Name: "browser", Kind: "mixed", BindAddress: "127.0.0.1", Port: 18080,
			ProxyGroupID: "group-a", AuthMode: "none", TransportJSON: "{}", PublicEndpointJSON: "{}", Enabled: true,
		},
		aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 7890),
		memberListener(t, "member-b", "service-b", "mixed", "group-b", "svc-b", "pass-b", listener.SharedInboundStandardOwner, 7890),
	}
}

func compileYAMLOrder(t *testing.T, repository yamlOrderRepository) (map[string]any, []string) {
	t.Helper()
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	document := decodeDocument(t, compiled.YAML)
	return document, rulesOf(t, document)
}

func TestYAMLDefaultProxyKeepsOtherListenersAndFollowsSiteRules(t *testing.T) {
	document, rules := compileYAMLOrder(t, yamlOrderRepository{
		routes:    routingrules.Config{Default: routingrules.DefaultProxy, RuleSets: []routingrules.RuleSet{yamlSiteRule()}},
		groups:    []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
		listeners: browserListeners(t),
	})
	keptProxy := false
	for _, entry := range listenersOf(t, document) {
		if entry["name"] == listenerConfigName("listener-browser") && entry["proxy"] == "group-a" {
			keptProxy = true
		}
	}
	if !keptProxy {
		t.Fatalf("unscoped default removed another listener's proxy: %+v", listenersOf(t, document))
	}
	site := indexOfRule(rules, "DOMAIN,keep.example,DIRECT")
	shared := indexOfRule(rules, "AND,((IN-NAME,"+SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")+"),(IN-USER,svc-b)),group-b")
	fallback := indexOfRule(rules, "MATCH,DIRECT")
	if site < 0 || shared < 0 || fallback < 0 || !(shared < site && site < fallback) {
		t.Fatalf("unscoped default must keep the other group's user ahead of the site rule: %v", rules)
	}
}

func TestYAMLAppliesToScopesDedicatedFallbackAfterSiteRule(t *testing.T) {
	document, rules := compileYAMLOrder(t, yamlOrderRepository{
		routes: routingrules.Config{
			Default: routingrules.DefaultProxy, AppliesTo: []string{"group-a"},
			RuleSets: []routingrules.RuleSet{yamlSiteRule()},
		},
		groups:    []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
		listeners: browserListeners(t),
	})
	for _, entry := range listenersOf(t, document) {
		if entry["name"] == listenerConfigName("listener-browser") {
			if _, forced := entry["proxy"]; forced {
				t.Fatalf("scoped listener still forces proxy: %+v", entry)
			}
		}
	}
	site := indexOfRule(rules, "DOMAIN,keep.example,DIRECT")
	ownGroup := indexOfRule(rules, "IN-NAME,"+listenerConfigName("listener-browser")+",group-a")
	shared := indexOfRule(rules, "AND,((IN-NAME,"+SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")+"),(IN-USER,svc-b)),group-b")
	end := indexOfRule(rules, "MATCH,DIRECT")
	if site < 0 || ownGroup < 0 || shared < 0 || end < 0 || !(shared < site && site < ownGroup && ownGroup < end) {
		t.Fatalf("want other group < site < own group < MATCH, rules = %v", rules)
	}
}

func TestYAMLDirectAndRejectStayInsideAppliesTo(t *testing.T) {
	listeners := browserListeners(t)
	listeners = append(listeners, memberListener(t, "member-a", "service-a", "mixed", "group-a", "svc-a", "pass-a", listener.SharedInboundStandardOwner, 7890))
	for _, fallback := range []string{routingrules.DefaultDirect, routingrules.DefaultReject} {
		_, rules := compileYAMLOrder(t, yamlOrderRepository{
			routes: routingrules.Config{
				Default: fallback, AppliesTo: []string{"group-b"},
				RuleSets: []routingrules.RuleSet{yamlSiteRule()},
			},
			groups:    []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
			listeners: listeners,
		})
		site := indexOfRule(rules, "DOMAIN,keep.example,DIRECT")
		kept := indexOfRule(rules, "AND,((IN-NAME,"+SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")+"),(IN-USER,svc-a)),group-a")
		dropped := indexOfRule(rules, "AND,((IN-NAME,"+SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")+"),(IN-USER,svc-b)),group-b")
		end := indexOfRule(rules, "MATCH,DIRECT")
		if site < 0 || kept < 0 || end < 0 || dropped >= 0 || !(kept < site && site < end) {
			t.Fatalf("%s rules = %v", fallback, rules)
		}
		reject := indexOfRule(rules, "NETWORK,tcp,REJECT")
		if fallback == routingrules.DefaultReject {
			if reject < 0 || !(site < reject && reject < end) {
				t.Fatalf("reject must follow the site rule and precede MATCH: %v", rules)
			}
			continue
		}
		if reject >= 0 {
			t.Fatalf("direct added a reject rule: %v", rules)
		}
	}
}

func TestYAMLDefaultRejectFollowsResidentialFallbackAndTUN(t *testing.T) {
	_, rules := compileYAMLOrder(t, yamlOrderRepository{
		settings: `{"tun":{"enabled":true,"proxy_group_id":"group-a"}}`,
		routes: routingrules.Config{
			Default: routingrules.DefaultReject, AppliesTo: []string{"group-a"},
			RuleSets: []routingrules.RuleSet{yamlSiteRule()},
		},
		groups: []store.ProxyGroupRecord{testGroup("group-a")},
		listeners: []store.ListenerRecord{{
			ID: "listener-res", Name: "residential", Kind: "mixed", BindAddress: "127.0.0.1", Port: 18081,
			ProxyGroupID: "group-a", AuthMode: "none", TransportJSON: "{}", PublicEndpointJSON: "{}", Enabled: true,
		}},
		clients: []store.ResidentialClientRouteRecord{{
			ResidentialClientSessionRecord: store.ResidentialClientSessionRecord{
				ChannelID: "channel-1", SessionID: "lazy",
				AuthUsername: "lazy-user", AuthPasswordEncrypted: []byte("secret"),
				RouteMode: "residential",
			},
			ListenerID: "listener-res", ChannelEnabled: true,
		}},
	})
	order := []string{
		"IN-NAME," + listenerConfigName("listener-res") + ",group-a",
		"DOMAIN,keep.example,DIRECT",
		"IN-TYPE,TUN,group-a",
		"NETWORK,tcp,REJECT",
		"NETWORK,udp,REJECT",
		"MATCH,DIRECT",
	}
	previous := -1
	for _, rule := range order {
		index := indexOfRule(rules, rule)
		if index < 0 || index < previous {
			t.Fatalf("rule %q out of order in %v", rule, rules)
		}
		previous = index
	}
	if indexOfRule(rules, "AND,((IN-NAME,"+listenerConfigName("listener-res")+"),(IN-USER,lazy-user)),group-a") >= 0 {
		t.Fatalf("unallocated residential session emitted its own rule: %v", rules)
	}
}

func TestPinnedIdentityBeatsYAMLSiteRule(t *testing.T) {
	pinned := memberListener(t, "member-pin", "pinned", "mixed", "group-b", "gh-pool", "pass-pin", listener.SharedInboundStandardOwner, 7890)
	pinned.NodeID = "node-1"
	_, rules := compileYAMLOrder(t, yamlOrderRepository{
		routes: routingrules.Config{Default: routingrules.DefaultProxy, RuleSets: []routingrules.RuleSet{yamlSiteRule()}},
		groups: []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 7890),
			pinned,
		},
		nodes: []store.NodeConfigRecord{{
			ID: "node-1", Fingerprint: "abcdef1234567890", DisplayName: "pin", Protocol: "http", LifecycleState: "healthy",
			CanonicalConfigEncrypted: []byte(`{"type":"http","server":"1.2.3.4","port":8080}`),
		}},
	})
	identity := indexOfRule(rules, "AND,((IN-NAME,"+SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")+"),(IN-USER,gh-pool)),"+nodeProxyName("abcdef1234567890"))
	site := indexOfRule(rules, "DOMAIN,keep.example,DIRECT")
	if identity < 0 || site < 0 || identity > site {
		t.Fatalf("pinned identity must precede the site rule: %v", rules)
	}
}

func TestLegacyRoutingKeepsListenerProxyAheadOfSiteRules(t *testing.T) {
	document, rules := compileYAMLOrder(t, yamlOrderRepository{
		routes:    routingrules.Config{RuleSets: []routingrules.RuleSet{yamlSiteRule()}},
		groups:    []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
		listeners: browserListeners(t),
	})
	forced := false
	for _, entry := range listenersOf(t, document) {
		if entry["name"] == listenerConfigName("listener-browser") && entry["proxy"] == "group-a" {
			forced = true
		}
	}
	if !forced {
		t.Fatalf("legacy listener dropped its proxy binding: %+v", listenersOf(t, document))
	}
	shared := "AND,((IN-NAME," + SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed") + "),(IN-USER,svc-b)),group-b"
	if indexOfRule(rules, shared) < 0 || indexOfRule(rules, shared) > indexOfRule(rules, "DOMAIN,keep.example,DIRECT") {
		t.Fatalf("legacy shared user rule must stay ahead of site rules: %v", rules)
	}
}

func TestEmptyDefaultKeepsListenerProxyDespiteAppliesTo(t *testing.T) {
	document, _ := compileYAMLOrder(t, yamlOrderRepository{
		routes:    routingrules.Config{AppliesTo: []string{"group-a"}, RuleSets: []routingrules.RuleSet{yamlSiteRule()}},
		groups:    []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
		listeners: browserListeners(t),
	})
	for _, entry := range listenersOf(t, document) {
		if entry["name"] == listenerConfigName("listener-browser") && entry["proxy"] == "group-a" {
			return
		}
	}
	t.Fatalf("empty default dropped a listener proxy: %+v", listenersOf(t, document))
}

func TestSharedUserGroupIDWinsOverSeparatorInTheRuleText(t *testing.T) {
	rule := sharedUserRule{Text: "AND,((IN-NAME,hx),(IN-USER,svc)),a),b", GroupID: "group-b"}
	pinned, grouped := splitSharedUsers([]sharedUserRule{rule})
	if len(pinned) != 0 || len(grouped) != 1 {
		t.Fatalf("pinned = %+v, grouped = %+v", pinned, grouped)
	}
}

func TestYAMLGroupNameSeparatorStaysBehindSiteRulesWhenScoped(t *testing.T) {
	groupB := testGroup("group-b")
	groupB.Name = "a),b"
	_, rules := compileYAMLOrder(t, yamlOrderRepository{
		routes: routingrules.Config{
			Default: routingrules.DefaultProxy, AppliesTo: []string{"group-b"},
			RuleSets: []routingrules.RuleSet{yamlSiteRule()},
		},
		groups: []store.ProxyGroupRecord{testGroup("group-a"), groupB},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 7890),
			memberListener(t, "member-a", "service-a", "mixed", "group-a", "svc-a", "pass-a", listener.SharedInboundStandardOwner, 7890),
			memberListener(t, "member-b", "service-b", "mixed", "group-b", "svc-b", "pass-b", listener.SharedInboundStandardOwner, 7890),
		},
	})
	name := SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")
	other := indexOfRule(rules, "AND,((IN-NAME,"+name+"),(IN-USER,svc-a)),group-a")
	site := indexOfRule(rules, "DOMAIN,keep.example,DIRECT")
	named := indexOfRule(rules, "AND,((IN-NAME,"+name+"),(IN-USER,svc-b)),a),b")
	if other < 0 || site < 0 || named < 0 || !(other < site && site < named) {
		t.Fatalf("scoped group whose name contains '), ' must follow the site rule: %v", rules)
	}
}
