package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/routingrules"
)

type fakeRoutingRulesService struct {
	config  routingrules.Config
	applied string
	err     error
}

func (service *fakeRoutingRulesService) Get(context.Context) (routingrules.Config, error) {
	return service.config, nil
}
func (service *fakeRoutingRulesService) Update(_ context.Context, config routingrules.Config) (routingrules.Config, error) {
	service.config = config
	return config, nil
}
func (service *fakeRoutingRulesService) UpdateRuleSets(_ context.Context, ruleSets []routingrules.RuleSet) (routingrules.Config, error) {
	service.config.RuleSets = ruleSets
	return service.config, nil
}
func (service *fakeRoutingRulesService) ApplyYAML(_ context.Context, raw string) (routingrules.Config, error) {
	service.applied = raw
	if service.err != nil {
		return routingrules.Config{}, service.err
	}
	service.config = routingrules.Config{RuleSets: []routingrules.RuleSet{{ID: "yaml-01", Name: "from-yaml", Enabled: true}}}
	return service.config, nil
}

func TestRoutingRulesAPIUpdatesWholeDesiredState(t *testing.T) {
	service := &fakeRoutingRulesService{}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRoutingRules(service))
	if err != nil {
		t.Fatal(err)
	}
	config := routingrules.Config{RuleSets: []routingrules.RuleSet{{ID: "ads", Name: "Ads", Enabled: true, Action: routingrules.Action{Type: "reject"}, Rules: []routingrules.Rule{{Type: "domain_suffix", Value: "ads.example"}}}}}
	payload, _ := json.Marshal(config)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/routing-rules", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(service.config.RuleSets) != 1 {
		t.Fatalf("status = %d, config = %+v", recorder.Code, service.config)
	}
}

func TestRoutingRulesApplyPostsYAMLBody(t *testing.T) {
	service := &fakeRoutingRulesService{}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRoutingRules(service))
	if err != nil {
		t.Fatal(err)
	}
	body := "version: 1\nname: env\ndefault: proxy\nrules: []\n"
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/routing-rules/apply", strings.NewReader(body))
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.applied != body || service.config.RuleSets[0].ID != "yaml-01" {
		t.Fatalf("status = %d, applied = %q, config = %+v", recorder.Code, service.applied, service.config)
	}
}

func TestRoutingRulesApplyRejectsOversizedBody(t *testing.T) {
	service := &fakeRoutingRulesService{}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRoutingRules(service))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/routing-rules/apply", strings.NewReader(strings.Repeat("a", 64<<10+1)))
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge || service.applied != "" || !strings.Contains(recorder.Body.String(), "payload_too_large") {
		t.Fatalf("status = %d, applied = %q, body = %s", recorder.Code, service.applied, recorder.Body.String())
	}
}

func TestRoutingRulesPutKeepsYAMLDefaultWhenOmitted(t *testing.T) {
	service := &fakeRoutingRulesService{config: routingrules.Config{Default: routingrules.DefaultReject, AppliesTo: []string{"group-a"}}}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRoutingRules(service))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/routing-rules", strings.NewReader(`{"default":"proxy","applies_to":["other"],"rule_sets":[]}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.config.Default != routingrules.DefaultReject || len(service.config.AppliesTo) != 1 {
		t.Fatalf("status = %d, config = %+v", recorder.Code, service.config)
	}
}

func TestRoutingMCPApplyAndRead(t *testing.T) {
	service := &fakeRoutingRulesService{}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRoutingRules(service))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"apply_rules","arguments":{"yaml":"version: 1\n"}}}`
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(body)))
	if recorder.Code != http.StatusOK || service.applied == "" || !strings.Contains(recorder.Body.String(), `"isError":false`) {
		t.Fatalf("status = %d, applied = %q, body = %s", recorder.Code, service.applied, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "list_groups") || !strings.Contains(recorder.Body.String(), "get_rules") {
		t.Fatalf("tools/list = %s", recorder.Body.String())
	}
}

func TestRoutingMCPRejectsTrailingJSONAndOversizedBody(t *testing.T) {
	service := &fakeRoutingRulesService{}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRoutingRules(service))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}{"extra":true}`
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(body)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "-32700") || strings.Contains(recorder.Body.String(), "list_groups") {
		t.Fatalf("trailing = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(strings.Repeat("a", 64<<10+1))))
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "payload_too_large") {
		t.Fatalf("oversize = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRoutingMCPHidesUnclassifiedToolErrors(t *testing.T) {
	service := &fakeRoutingRulesService{err: errors.New("dial unix /run/secret.sock: connection refused")}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithRoutingRules(service))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"apply_rules","arguments":{"yaml":"version: 1\n"}}}`
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(body)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "operation failed") || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("leaked = %s", recorder.Body.String())
	}
	service.err = fmt.Errorf("%w: yaml version must be 1", routingrules.ErrInvalid)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(body)))
	if !strings.Contains(recorder.Body.String(), "yaml version must be 1") {
		t.Fatalf("validation hidden = %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_groups"}}`)))
	if !strings.Contains(recorder.Body.String(), "proxy groups are not configured") {
		t.Fatalf("safe mcp error hidden = %s", recorder.Body.String())
	}
}
