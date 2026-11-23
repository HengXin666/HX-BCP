package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/artifact"
	"github.com/HengXin666/hx-pxy/internal/bundle"
)

func TestBackupAPIFlow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"ready":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	catalog, err := artifact.NewCatalog(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	bundles, err := bundle.NewService(catalog, []bundle.Source{{
		Name:     "state",
		Path:     statePath,
		Scope:    bundle.ScopeBackup | bundle.ScopeExport,
		Required: true,
	}}, "test")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := NewServer(bundles, logger)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	record := createArtifact(t, testServer.URL+"/api/v1/backups", `{"description":"api backup"}`, http.StatusCreated)
	if record.Kind != artifact.KindBackup {
		t.Fatalf("created kind = %q", record.Kind)
	}

	response := doRequest(t, http.MethodGet, testServer.URL+"/api/v1/backups", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET backups status = %d", response.StatusCode)
	}
	var list struct {
		Items []artifact.Record `json:"items"`
	}
	decodeResponse(t, response, &list)
	if len(list.Items) != 1 || list.Items[0].ID != record.ID {
		t.Fatalf("GET backups response = %#v", list)
	}

	response = doRequest(t, http.MethodPost, testServer.URL+"/api/v1/backups/"+record.ID+"/verify", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST verify status = %d", response.StatusCode)
	}
	var verification bundle.VerifyResult
	decodeResponse(t, response, &verification)
	if !verification.Valid || verification.ArtifactID != record.ID {
		t.Fatalf("verification = %#v", verification)
	}

	response = doRequest(t, http.MethodGet, testServer.URL+"/api/v1/backups/"+record.ID+"/download", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET download status = %d", response.StatusCode)
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(download) error = %v", err)
	}
	if int64(len(payload)) != record.Size {
		t.Fatalf("download size = %d, want %d", len(payload), record.Size)
	}
	if response.Header.Get("ETag") == "" {
		t.Fatal("download ETag is empty")
	}

	response = doRequest(t, http.MethodDelete, testServer.URL+"/api/v1/backups/"+record.ID, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE backup status = %d", response.StatusCode)
	}
	response = doRequest(t, http.MethodGet, testServer.URL+"/api/v1/backups/"+record.ID, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted backup status = %d", response.StatusCode)
	}
}

func TestExportAPIRejectsPlaintextSecrets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	if err := os.WriteFile(statePath, []byte("state"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	catalog, err := artifact.NewCatalog(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	bundles, err := bundle.NewService(catalog, []bundle.Source{{Name: "state", Path: statePath, Scope: bundle.ScopeExport}}, "test")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server, err := NewServer(bundles, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exports", bytes.NewBufferString(`{"include_secrets":true}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST export status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != "secret_export_disabled" {
		t.Fatalf("error response = %#v", response)
	}
}

func TestHealthReadiness(t *testing.T) {
	t.Parallel()

	service := &stubBundleService{}
	server, err := NewServer(service, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	server.SetReady(false)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d", recorder.Code)
	}
}

func createArtifact(t *testing.T, endpoint, body string, expectedStatus int) artifact.Record {
	t.Helper()
	response := doRequest(t, http.MethodPost, endpoint, bytes.NewBufferString(body))
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("POST artifact status = %d, body=%s", response.StatusCode, payload)
	}
	var record artifact.Record
	decodeResponse(t, response, &record)
	return record
}

func doRequest(t *testing.T, method, endpoint string, body io.Reader) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, endpoint, body)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

type stubBundleService struct{}

func (*stubBundleService) Create(context.Context, artifact.Kind, bundle.CreateOptions) (artifact.Record, error) {
	return artifact.Record{}, nil
}
func (*stubBundleService) List(artifact.Kind) ([]artifact.Record, error) { return nil, nil }
func (*stubBundleService) Open(string) (artifact.Record, *os.File, error) {
	return artifact.Record{}, nil, artifact.ErrNotFound
}
func (*stubBundleService) Delete(string) error { return nil }
func (*stubBundleService) Verify(context.Context, string) (bundle.VerifyResult, error) {
	return bundle.VerifyResult{}, nil
}
