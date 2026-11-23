package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/fleet"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// stubFleetService provides canned fleet data so the HTTP layer can be tested
// without a real CF account or the fleet maintenance loop.
type stubFleetService struct {
	accounts []store.CFAccountRecord
	workers  map[string][]store.FleetWorkerRecord
}

func (s *stubFleetService) Sweep(context.Context) (fleet.Summary, error) {
	return fleet.Summary{}, nil
}

func (s *stubFleetService) ListAccounts(context.Context) ([]store.CFAccountRecord, error) {
	return s.accounts, nil
}

func (s *stubFleetService) ListAccountWorkers(_ context.Context, accountID string) ([]store.FleetWorkerRecord, error) {
	return s.workers[accountID], nil
}

func (s *stubFleetService) AddAccount(context.Context, fleet.AddAccountRequest) (store.CFAccountRecord, error) {
	return store.CFAccountRecord{}, nil
}

func (s *stubFleetService) RemoveAccount(context.Context, string) error {
	return nil
}

func (s *stubFleetService) LastSummary() fleet.Summary {
	return fleet.Summary{}
}

func newFleetTestServer(t *testing.T, service FleetService) *httptest.Server {
	t.Helper()
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithFleet(service),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	return testServer
}

// A managed CF account may legitimately have zero workers (e.g. right after
// being linked, or when every worker was removed). The /api/v1/fleet/status
// contract must still expose an array so the UI does not crash on
// workers.filter(). A nil Go slice serializes to JSON null, so the handler must
// initialize the field. This is a regression test for the residential page
// crash "can't access property filter, t.workers is null".
func TestFleetStatusWorkersAlwaysArray(t *testing.T) {
	service := &stubFleetService{
		accounts: []store.CFAccountRecord{{
			ID:        "cf-account-empty",
			Email:     "empty@example.com",
			Status:    "active",
			BannedAt:  "",
			CreatedAt: time.Now().UTC(),
		}},
		workers: map[string][]store.FleetWorkerRecord{
			// No entry => the store returns a nil slice, exercising the nil path.
			"cf-account-empty": nil,
		},
	}
	testServer := newFleetTestServer(t, service)

	response, err := http.Get(testServer.URL + "/api/v1/fleet/status")
	if err != nil {
		t.Fatalf("GET fleet/status: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	var payload struct {
		Accounts []struct {
			Workers json.RawMessage `json:"workers"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(payload.Accounts))
	}
	raw := string(payload.Accounts[0].Workers)
	if raw == "null" {
		t.Fatal("workers serialized as null; regression: zero-worker account must emit []")
	}
	if raw != "[]" {
		t.Fatalf("workers = %s, want []", raw)
	}
}

// A ban list account with workers reports them and contributes to totals.
func TestFleetStatusCountsActiveWorkers(t *testing.T) {
	service := &stubFleetService{
		accounts: []store.CFAccountRecord{
			{ID: "cf-a", Email: "a@example.com", Status: "active"},
			{ID: "cf-b", Email: "b@example.com", Status: "banned"},
		},
		workers: map[string][]store.FleetWorkerRecord{
			"cf-a": {
				{WorkerName: "w1", Failures: 0},
				{WorkerName: "w2", Failures: 3},
			},
			"cf-b": {
				{WorkerName: "w3", Failures: 0},
				{WorkerName: "w4", Failures: 0},
			},
		},
	}
	testServer := newFleetTestServer(t, service)

	response, err := http.Get(testServer.URL + "/api/v1/fleet/status")
	if err != nil {
		t.Fatalf("GET fleet/status: %v", err)
	}
	defer response.Body.Close()

	var payload struct {
		Accounts []struct {
			ID      string `json:"id"`
			Workers []struct {
				WorkerName string `json:"worker_name"`
			} `json:"workers"`
		} `json:"accounts"`
		TotalWorkers int `json:"total_workers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.TotalWorkers != 2 {
		t.Fatalf("total_workers = %d, want 2 (only active accounts count)", payload.TotalWorkers)
	}
	if len(payload.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(payload.Accounts))
	}
	if want := 2; len(payload.Accounts[1].Workers) != want {
		t.Fatalf("banned account workers = %d, want %d", len(payload.Accounts[1].Workers), want)
	}
}
