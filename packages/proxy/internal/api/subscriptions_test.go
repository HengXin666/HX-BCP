package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/subscription"
)

func TestSubscriptionAPIHidesSourceConfigurationAndEnforcesOptimisticLocking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	box, err := secret.New(bytes.Repeat([]byte{0x67}, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	subscriptions, err := subscription.NewService(database, box)
	if err != nil {
		t.Fatalf("subscription.NewService() error = %v", err)
	}
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSubscriptions(subscriptions),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	sourceURL := "https://subscription.example.invalid/example-list"
	createBody := `{
		"name":"api-airport",
		"source_type":"remote",
		"source_config":{
			"url":"` + sourceURL + `",
			"headers":{"X-Example":"example-value"},
			"timeout_seconds":15
		}
	}`
	response := doRequest(t, http.MethodPost, testServer.URL+"/api/v1/subscriptions", strings.NewReader(createBody))
	createdPayload := readResponseBytes(t, response, http.StatusCreated)
	assertDoesNotLeak(t, createdPayload, sourceURL, "example-value", "\"source_config\":")
	var created subscription.Subscription
	if err := json.Unmarshal(createdPayload, &created); err != nil {
		t.Fatalf("decode created subscription: %v", err)
	}
	if created.ID == "" || created.Version != 1 || !created.SourceConfigured {
		t.Fatalf("created subscription = %#v", created)
	}

	response = doRequest(t, http.MethodGet, testServer.URL+"/api/v1/subscriptions", nil)
	listPayload := readResponseBytes(t, response, http.StatusOK)
	assertDoesNotLeak(t, listPayload, sourceURL, "example-value", "\"source_config\":")

	response = doRequest(t, http.MethodGet, testServer.URL+"/api/v1/subscriptions/"+created.ID, nil)
	detailPayload := readResponseBytes(t, response, http.StatusOK)
	assertDoesNotLeak(t, detailPayload, sourceURL, "example-value", "\"source_config\":")

	updateBody := `{
		"version":1,
		"name":"api-airport-updated",
		"source_type":"inline",
		"source_config":{"inline":"vless://example-node"},
		"enabled":false,
		"refresh_interval_seconds":7200
	}`
	response = doRequest(t, http.MethodPut, testServer.URL+"/api/v1/subscriptions/"+created.ID, strings.NewReader(updateBody))
	updatedPayload := readResponseBytes(t, response, http.StatusOK)
	assertDoesNotLeak(t, updatedPayload, "vless://example-node", "\"source_config\":")
	var updated subscription.Subscription
	if err := json.Unmarshal(updatedPayload, &updated); err != nil {
		t.Fatalf("decode updated subscription: %v", err)
	}
	if updated.Version != 2 || updated.Enabled {
		t.Fatalf("updated subscription = %#v", updated)
	}

	metadataBody := `{
		"version":2,
		"name":"metadata-only",
		"source_type":"inline",
		"enabled":true,
		"refresh_interval_seconds":3600
	}`
	response = doRequest(t, http.MethodPut, testServer.URL+"/api/v1/subscriptions/"+created.ID, strings.NewReader(metadataBody))
	metadataPayload := readResponseBytes(t, response, http.StatusOK)
	var metadataUpdated subscription.Subscription
	if err := json.Unmarshal(metadataPayload, &metadataUpdated); err != nil {
		t.Fatalf("decode metadata update: %v", err)
	}
	keptSource, err := subscriptions.SourceConfig(ctx, created.ID)
	if err != nil || metadataUpdated.Version != 3 || keptSource.Inline != "vless://example-node" {
		t.Fatalf("metadata update = %#v, source = %#v, error = %v", metadataUpdated, keptSource, err)
	}

	response = doRequest(t, http.MethodPut, testServer.URL+"/api/v1/subscriptions/"+created.ID, strings.NewReader(updateBody))
	conflictPayload := readResponseBytes(t, response, http.StatusConflict)
	if !bytes.Contains(conflictPayload, []byte(`"subscription_conflict"`)) {
		t.Fatalf("conflict response = %s", conflictPayload)
	}

	response = doRequest(t, http.MethodDelete, testServer.URL+"/api/v1/subscriptions/"+created.ID+"?version=3", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE subscription status = %d", response.StatusCode)
	}
	response = doRequest(t, http.MethodGet, testServer.URL+"/api/v1/subscriptions/"+created.ID, nil)
	readResponseBytes(t, response, http.StatusNotFound)
}

func TestSubscriptionRefreshAPI(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	box, err := secret.New(bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	subscriptions, err := subscription.NewService(
		database,
		box,
		subscription.WithRefresh(subscription.NewDefaultSourceLoader(), filepath.Join(root, "snapshots")),
	)
	if err != nil {
		t.Fatalf("subscription.NewService() error = %v", err)
	}
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSubscriptions(subscriptions),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	createBody := `{
		"name":"refresh-api",
		"source_type":"inline",
		"source_config":{"inline":"vless://one\nvmess://two\n"}
	}`
	response := doRequest(t, http.MethodPost, testServer.URL+"/api/v1/subscriptions", strings.NewReader(createBody))
	createdPayload := readResponseBytes(t, response, http.StatusCreated)
	var created subscription.Subscription
	if err := json.Unmarshal(createdPayload, &created); err != nil {
		t.Fatalf("decode created subscription: %v", err)
	}

	response = doRequest(t, http.MethodPost, testServer.URL+"/api/v1/subscriptions/"+created.ID+"/refresh", nil)
	refreshPayload := readResponseBytes(t, response, http.StatusOK)
	var refreshed subscription.RefreshResult
	if err := json.Unmarshal(refreshPayload, &refreshed); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if !refreshed.Changed || refreshed.SnapshotID == "" || refreshed.EstimatedNodes != 2 {
		t.Fatalf("refresh response = %#v", refreshed)
	}

	response = doRequest(t, http.MethodPost, testServer.URL+"/api/v1/subscriptions/"+created.ID+"/refresh", nil)
	secondPayload := readResponseBytes(t, response, http.StatusOK)
	var second subscription.RefreshResult
	if err := json.Unmarshal(secondPayload, &second); err != nil {
		t.Fatalf("decode second refresh response: %v", err)
	}
	if second.Changed || second.SnapshotID != refreshed.SnapshotID {
		t.Fatalf("second refresh = %#v", second)
	}
}

func TestSubscriptionAPIRejectsUnknownFieldsAndInvalidURL(t *testing.T) {
	t.Parallel()

	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()
	box, err := secret.New(bytes.Repeat([]byte{0x78}, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	subscriptions, err := subscription.NewService(database, box)
	if err != nil {
		t.Fatalf("subscription.NewService() error = %v", err)
	}
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSubscriptions(subscriptions),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	unknown := httptest.NewRecorder()
	unknownRequest := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", strings.NewReader(`{"name":"x","unknown":true}`))
	unknownRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(unknown, unknownRequest)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, body=%s", unknown.Code, unknown.Body.String())
	}

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", strings.NewReader(`{
		"name":"invalid-url",
		"source_type":"remote",
		"source_config":{"url":"file:///etc/passwd"}
	}`))
	invalidRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid URL status = %d, body=%s", invalid.Code, invalid.Body.String())
	}
}

func readResponseBytes(t *testing.T, response *http.Response, expectedStatus int) []byte {
	t.Helper()
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response) error = %v", err)
	}
	if response.StatusCode != expectedStatus {
		t.Fatalf("response status = %d, want %d, body=%s", response.StatusCode, expectedStatus, payload)
	}
	return payload
}

func assertDoesNotLeak(t *testing.T, payload []byte, values ...string) {
	t.Helper()
	for _, value := range values {
		if bytes.Contains(payload, []byte(value)) {
			t.Fatalf("response leaks %q: %s", value, payload)
		}
	}
}
