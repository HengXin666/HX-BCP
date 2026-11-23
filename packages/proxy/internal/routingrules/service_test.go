package routingrules

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestCompileScopesRulesToAssignedGroupListeners(t *testing.T) {
	config := Config{RuleSets: []RuleSet{
		{ID: "ads", Name: "Ads", Enabled: true, Priority: 10, AppliedGroupIDs: []string{"group-a"}, Action: Action{Type: "reject"}, Rules: []Rule{{Type: "domain_suffix", Value: "ads.example"}}},
		{ID: "github", Name: "GitHub", Enabled: true, Priority: 20, Action: Action{Type: "proxy_group", ProxyGroupID: "group-b"}, Rules: []Rule{{Type: "domain", Value: "github.com"}}},
	}}
	groups := []store.ProxyGroupRecord{{ID: "group-a", Name: "A", Enabled: true}, {ID: "group-b", Name: "B", Enabled: true}}
	listeners := []store.ListenerRecord{{ID: "listener-a", ProxyGroupID: "group-a", Enabled: true}, {ID: "listener-b", ProxyGroupID: "group-b", Enabled: true}}
	rules := Compile(config, groups, listeners, func(id string) string { return "in-" + id })
	if len(rules) != 2 || rules[0] != "AND,((IN-NAME,in-listener-a),(DOMAIN-SUFFIX,ads.example)),REJECT" || rules[1] != "DOMAIN,github.com,B" {
		t.Fatalf("compiled rules = %v", rules)
	}
}

func TestCompileAllowsIndependentActionsPerProxyGroup(t *testing.T) {
	config := Config{RuleSets: []RuleSet{{
		ID: "ai", Name: "AI sites", Enabled: true, Priority: 10,
		Routes: []GroupRoute{
			{ProxyGroupID: "group-a", Action: Action{Type: "direct"}},
			{ProxyGroupID: "group-b", Action: Action{Type: "reject"}},
		},
		Rules: []Rule{{Type: "domain_suffix", Value: "example.ai"}},
	}}}
	groups := []store.ProxyGroupRecord{{ID: "group-a", Name: "A", Enabled: true}, {ID: "group-b", Name: "B", Enabled: true}}
	listeners := []store.ListenerRecord{{ID: "listener-a", ProxyGroupID: "group-a", Enabled: true}, {ID: "listener-b", ProxyGroupID: "group-b", Enabled: true}}

	if err := Validate(config, groups); err != nil {
		t.Fatal(err)
	}
	rules := Compile(config, groups, listeners, func(id string) string { return "in-" + id })
	want := []string{
		"AND,((IN-NAME,in-listener-a),(DOMAIN-SUFFIX,example.ai)),DIRECT",
		"AND,((IN-NAME,in-listener-b),(DOMAIN-SUFFIX,example.ai)),REJECT",
	}
	if len(rules) != len(want) || rules[0] != want[0] || rules[1] != want[1] {
		t.Fatalf("compiled rules = %v, want %v", rules, want)
	}
}

func TestValidateRejectsUnsafeAndMissingReferences(t *testing.T) {
	groups := []store.ProxyGroupRecord{{ID: "group-a"}}
	tests := []Config{
		{RuleSets: []RuleSet{{ID: "bad", Name: "Bad", Enabled: true, Action: Action{Type: "proxy_group", ProxyGroupID: "missing"}, Rules: []Rule{{Type: "domain", Value: "example.com"}}}}},
		{RuleSets: []RuleSet{{ID: "bad", Name: "Bad", Enabled: true, Action: Action{Type: "reject"}, Rules: []Rule{{Type: "domain", Value: "example.com,REJECT"}}}}},
		{RuleSets: []RuleSet{{ID: "bad", Name: "Bad", Enabled: true, AppliedGroupIDs: []string{"missing"}, Action: Action{Type: "direct"}, Rules: []Rule{{Type: "domain", Value: "example.com"}}}}},
		{Default: "sideways", RuleSets: []RuleSet{{ID: "bad", Name: "Bad", Enabled: true, Action: Action{Type: "reject"}, Rules: []Rule{{Type: "domain", Value: "example.com"}}}}},
		{Default: DefaultProxy, AppliesTo: []string{"missing"}, RuleSets: []RuleSet{{ID: "bad", Name: "Bad", Enabled: true, Action: Action{Type: "reject"}, Rules: []Rule{{Type: "domain", Value: "example.com"}}}}},
	}
	for index, config := range tests {
		if err := Validate(config, groups); err == nil {
			t.Fatalf("case %d unexpectedly valid", index)
		}
	}
}

type memoryRoutingRepository struct {
	mu     sync.Mutex
	values map[string]string
	groups []store.ProxyGroupRecord
}

func (repository *memoryRoutingRepository) GetMetadata(_ context.Context, key string) (string, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.values[key]
	if !ok {
		return "", store.ErrNotFound
	}
	return value, nil
}

func (repository *memoryRoutingRepository) SetMetadata(_ context.Context, key, value string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.values == nil {
		repository.values = map[string]string{}
	}
	repository.values[key] = value
	return nil
}

func (repository *memoryRoutingRepository) ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error) {
	return repository.groups, nil
}

type gatingApplier struct {
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	current int
	max     int
}

func (applier *gatingApplier) Apply(context.Context) error {
	applier.mu.Lock()
	applier.current++
	if applier.current > applier.max {
		applier.max = applier.current
	}
	applier.mu.Unlock()
	applier.entered <- struct{}{}
	<-applier.release
	applier.mu.Lock()
	applier.current--
	applier.mu.Unlock()
	return nil
}

func TestUpdateDoesNotOverlapApply(t *testing.T) {
	repository := &memoryRoutingRepository{groups: []store.ProxyGroupRecord{{ID: "group-a", Name: "A", Enabled: true}}}
	applier := &gatingApplier{entered: make(chan struct{}, 2), release: make(chan struct{})}
	service, err := NewService(repository, applier)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{RuleSets: []RuleSet{{
		ID: "ads", Name: "Ads", Enabled: true,
		Action: Action{Type: "reject"},
		Rules:  []Rule{{Type: "domain", Value: "example.com"}},
	}}}
	errCh := make(chan error, 2)
	go func() { _, err := service.Update(context.Background(), config); errCh <- err }()
	<-applier.entered
	go func() { _, err := service.Update(context.Background(), config); errCh <- err }()
	select {
	case <-applier.entered:
		t.Fatal("second apply overlapped the first")
	case <-time.After(50 * time.Millisecond):
	}
	applier.release <- struct{}{}
	<-applier.entered
	applier.release <- struct{}{}
	for index := 0; index < 2; index++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	if applier.max != 1 {
		t.Fatalf("concurrent applies = %d", applier.max)
	}
}

func TestValidateAllowsDisabledAppliesToGroup(t *testing.T) {
	groups := []store.ProxyGroupRecord{{ID: "group-a", Enabled: false}}
	config := Config{
		Default: DefaultProxy, AppliesTo: []string{"group-a"},
		RuleSets: []RuleSet{{
			ID: "ads", Name: "Ads", Enabled: true,
			Action: Action{Type: "reject"},
			Rules:  []Rule{{Type: "domain", Value: "example.com"}},
		}},
	}
	if err := Validate(config, groups); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRejectsAppliesToWithoutDefault(t *testing.T) {
	repository := &memoryRoutingRepository{groups: []store.ProxyGroupRecord{{ID: "group-a", Name: "A", Enabled: true}}}
	service, err := NewService(repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Update(context.Background(), Config{
		AppliesTo: []string{"group-a"},
		RuleSets: []RuleSet{{
			ID: "ads", Name: "Ads", Enabled: true,
			Action: Action{Type: "reject"},
			Rules:  []Rule{{Type: "domain", Value: "example.com"}},
		}},
	})
	if err == nil {
		t.Fatal("empty default with applies_to was accepted")
	}
}

func TestUpdateRuleSetsKeepsDisabledGroupAndDropsDeletedID(t *testing.T) {
	repository := &memoryRoutingRepository{groups: []store.ProxyGroupRecord{
		{ID: "group-a", Name: "A", Enabled: true},
		{ID: "group-b", Name: "B", Enabled: false},
	}}
	service, err := NewService(repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored := Config{
		Default:   DefaultReject,
		AppliesTo: []string{"group-a", "group-b", "deleted"},
		RuleSets: []RuleSet{{
			ID: "old", Name: "Old", Enabled: true,
			Action: Action{Type: "reject"},
			Rules:  []Rule{{Type: "domain", Value: "old.example"}},
		}},
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.SetMetadata(context.Background(), MetadataKey, string(encoded)); err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateRuleSets(context.Background(), []RuleSet{{
		ID: "ads", Name: "Ads", Enabled: true,
		Action: Action{Type: "reject"},
		Rules:  []Rule{{Type: "domain", Value: "example.com"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Default != DefaultReject || len(updated.AppliesTo) != 2 || updated.AppliesTo[0] != "group-a" || updated.AppliesTo[1] != "group-b" || updated.RuleSets[0].ID != "ads" {
		t.Fatalf("updated = %+v", updated)
	}
}

func TestUpdateRuleSetsWaitsForYAMLUpdate(t *testing.T) {
	repository := &memoryRoutingRepository{groups: []store.ProxyGroupRecord{{ID: "group-a", Name: "A", Enabled: true}}}
	applier := &gatingApplier{entered: make(chan struct{}, 2), release: make(chan struct{})}
	service, err := NewService(repository, applier)
	if err != nil {
		t.Fatal(err)
	}
	yamlDoc := Config{
		Default: DefaultReject, AppliesTo: []string{"group-a"},
		RuleSets: []RuleSet{{
			ID: "yaml-01", Name: "yaml", Enabled: true,
			Action: Action{Type: "direct"},
			Rules:  []Rule{{Type: "domain", Value: "keep.example"}},
		}},
	}
	errCh := make(chan error, 2)
	go func() { _, err := service.Update(context.Background(), yamlDoc); errCh <- err }()
	<-applier.entered
	go func() {
		_, err := service.UpdateRuleSets(context.Background(), []RuleSet{{
			ID: "ads", Name: "Ads", Enabled: true,
			Action: Action{Type: "reject"},
			Rules:  []Rule{{Type: "domain", Value: "ads.example"}},
		}})
		errCh <- err
	}()
	select {
	case <-applier.entered:
		t.Fatal("rule-set save overlapped the yaml apply")
	case <-time.After(50 * time.Millisecond):
	}
	applier.release <- struct{}{}
	<-applier.entered
	applier.release <- struct{}{}
	for index := 0; index < 2; index++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	got, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Default != DefaultReject || len(got.AppliesTo) != 1 || len(got.RuleSets) != 1 || got.RuleSets[0].ID != "ads" {
		t.Fatalf("stored = %+v", got)
	}
}
