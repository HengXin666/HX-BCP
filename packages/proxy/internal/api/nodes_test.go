package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/artifact"
	"github.com/HengXin666/hx-pxy/internal/bundle"
	"github.com/HengXin666/hx-pxy/internal/node"
)

type fakeNodeService struct {
	items    []node.Node
	settings node.QualitySettings
	// lastFilter records the projection the API asked for, so tests can assert
	// that view=summary actually reaches the service instead of being parsed
	// and dropped on the floor.
	lastFilter node.Filter
}

func (service *fakeNodeService) QualitySettings(context.Context) (node.QualitySettings, error) {
	if reflect.DeepEqual(service.settings, node.QualitySettings{}) {
		return node.DefaultQualitySettings(), nil
	}
	return service.settings, nil
}

func (service *fakeNodeService) UpdateQualitySettings(_ context.Context, settings node.QualitySettings) (node.QualitySettings, error) {
	service.settings = settings
	return settings, nil
}

func newNodeTestServer(t *testing.T, items []node.Node) *Server {
	t.Helper()
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithNodes(&fakeNodeService{items: items}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func (service *fakeNodeService) List(_ context.Context, filter node.Filter) ([]node.Node, error) {
	service.lastFilter = filter
	items := make([]node.Node, 0, len(service.items))
	for _, item := range service.items {
		if filter.Protocol != "" && item.Protocol != filter.Protocol {
			continue
		}
		if filter.State != "" && item.LifecycleState != filter.State {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *fakeNodeService) Count(_ context.Context, filter node.Filter) (int, error) {
	items, err := service.List(context.Background(), filter)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func (service *fakeNodeService) Get(_ context.Context, id string) (node.Node, error) {
	for _, item := range service.items {
		if item.ID == id {
			return item, nil
		}
	}
	return node.Node{}, node.ErrNotFound
}

func (service *fakeNodeService) Disable(ctx context.Context, id string) (node.Node, error) {
	return service.setState(ctx, id, "disabled")
}

func (service *fakeNodeService) Enable(ctx context.Context, id string) (node.Node, error) {
	return service.setState(ctx, id, "candidate")
}

func (service *fakeNodeService) setState(_ context.Context, id, state string) (node.Node, error) {
	for index, item := range service.items {
		if item.ID == id {
			service.items[index].LifecycleState = state
			return service.items[index], nil
		}
	}
	return node.Node{}, node.ErrNotFound
}

func (service *fakeNodeService) Check(_ context.Context, id string) (node.CheckResult, error) {
	item, err := service.Get(context.Background(), id)
	if err != nil {
		return node.CheckResult{}, err
	}
	latency := 42
	return node.CheckResult{
		Node:      item,
		Success:   true,
		LatencyMS: &latency,
		CheckedAt: time.Now().UTC(),
		TestURL:   "https://example.com/generate_204",
	}, nil
}

func (service *fakeNodeService) CheckMany(ctx context.Context, ids []string) ([]node.CheckResult, error) {
	if len(ids) == 0 {
		for _, item := range service.items {
			ids = append(ids, item.ID)
		}
	}
	results := make([]node.CheckResult, 0, len(ids))
	for _, id := range ids {
		result, err := service.Check(ctx, id)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (service *fakeNodeService) CheckManyProgress(ctx context.Context, ids []string, notify func(node.CheckProgress) error) error {
	results, err := service.CheckMany(ctx, ids)
	if err != nil {
		return err
	}
	for index, result := range results {
		if err := notify(node.CheckProgress{Completed: index + 1, Total: len(results), NodeID: result.Node.ID, Result: result}); err != nil {
			return err
		}
	}
	return nil
}

func TestNodeAPIExposesMetadataWithoutCanonicalConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	if err := os.WriteFile(statePath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := artifact.NewCatalog(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := bundle.NewService(catalog, []bundle.Source{{
		Name:     "state",
		Path:     statePath,
		Scope:    bundle.ScopeBackup,
		Required: true,
	}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	nodes := &fakeNodeService{items: []node.Node{{
		ID:             "node-1",
		Fingerprint:    "abc123",
		DisplayName:    "Tokyo",
		Protocol:       "vless",
		LifecycleState: "candidate",
		FirstSeenAt:    now,
		LastSeenAt:     now,
		Version:        1,
		SourceCount:    2,
	}}}
	server, err := NewServer(
		bundles,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithNodes(nodes),
	)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	response, err := http.Get(testServer.URL + "/api/v1/nodes?protocol=vless&state=candidate")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var raw map[string]any
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"canonical", "password", "username", "uuid"} {
		if jsonContainsString(encoded, forbidden) {
			t.Fatalf("node API response contains forbidden field marker %q: %s", forbidden, encoded)
		}
	}
	items, ok := raw["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", raw["items"])
	}
}

func TestNodeAPILeanProjectionAndTotal(t *testing.T) {
	t.Parallel()
	nodes := &fakeNodeService{items: []node.Node{
		{ID: "node-a", Protocol: "vless", LifecycleState: "healthy"},
		{ID: "node-b", Protocol: "vless", LifecycleState: "candidate"},
		{ID: "node-c", Protocol: "trojan", LifecycleState: "healthy"},
	}}
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithNodes(nodes),
	)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	response, err := http.Get(testServer.URL + "/api/v1/nodes?view=summary&with_total=1&protocol=vless")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var payload struct {
		Items []node.Node `json:"items"`
		Total *int        `json:"total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(payload.Items))
	}
	if payload.Total == nil || *payload.Total != 2 {
		t.Fatalf("total = %v, want 2 (the filtered size, not the page size)", payload.Total)
	}
	if !nodes.lastFilter.SkipHealthChecks {
		t.Fatal("view=summary did not reach the service as SkipHealthChecks")
	}

	// Without with_total the field must stay absent: the count is opt-in.
	plain, err := http.Get(testServer.URL + "/api/v1/nodes?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Body.Close()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(plain.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["total"]; present {
		t.Fatal("total was returned without with_total=1")
	}
	if nodes.lastFilter.SkipHealthChecks {
		t.Fatal("the default view must stay the full projection")
	}

	invalid, err := http.Get(testServer.URL + "/api/v1/nodes?view=lean")
	if err != nil {
		t.Fatal(err)
	}
	defer invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown view status = %d, want 400", invalid.StatusCode)
	}
}

func TestNodeAPIChecksSelectedNodesInOneRequest(t *testing.T) {
	t.Parallel()
	server := newNodeTestServer(t, []node.Node{{ID: "node-a"}, {ID: "node-b"}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/nodes", strings.NewReader(`{"node_ids":["node-b"]}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Items []node.CheckResult `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].Node.ID != "node-b" {
		t.Fatalf("items = %+v", response.Items)
	}
}

func TestNodeAPIStreamsEachCompletedCheck(t *testing.T) {
	t.Parallel()
	server := newNodeTestServer(t, []node.Node{{ID: "node-a"}, {ID: "node-b"}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/nodes?stream=1", strings.NewReader(`{"node_ids":["node-a","node-b"]}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	decoder := json.NewDecoder(recorder.Body)
	for completed := 1; completed <= 2; completed++ {
		var progress node.CheckProgress
		if err := decoder.Decode(&progress); err != nil {
			t.Fatal(err)
		}
		if progress.Completed != completed || progress.Total != 2 || progress.NodeID == "" {
			t.Fatalf("progress = %+v", progress)
		}
	}
}

func jsonContainsString(payload []byte, value string) bool {
	for index := 0; index+len(value) <= len(payload); index++ {
		match := true
		for offset := range value {
			left := payload[index+offset]
			right := value[offset]
			if left >= 'A' && left <= 'Z' {
				left += 'a' - 'A'
			}
			if right >= 'A' && right <= 'Z' {
				right += 'a' - 'A'
			}
			if left != right {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
