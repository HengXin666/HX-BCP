package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/auth"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	terminalservice "github.com/HengXin666/hx-pxy/internal/terminal"
	"log/slog"
)

// terminalFixture is a fully authenticated terminal-enabled server with a
// 2FA-verified administrator session.
type terminalFixture struct {
	server       *httptest.Server
	authService  *auth.Service
	sessionToken string
	csrfToken    string
	t            *testing.T
}

func newTerminalFixture(t *testing.T) *terminalFixture {
	return newTerminalFixtureWithSocket(t, "")
}

func newTerminalFixtureWithSocket(t *testing.T, socketPath string) *terminalFixture {
	t.Helper()
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "terminal-files.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	authService, err := auth.NewService(database, filepath.Join(root, "admin-setup-token"), logger, box)
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnsureSetupToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	setupToken, err := os.ReadFile(filepath.Join(root, "admin-setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.Setup(context.Background(), strings.TrimSpace(string(setupToken)), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	setup, err := authService.BeginTwoFactorSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnableTwoFactor(context.Background(), code); err != nil {
		t.Fatal(err)
	}
	session, err := authService.Login(context.Background(), "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.VerifyTwoFactor(context.Background(), session.Token, "127.0.0.1", code); err != nil {
		t.Fatal(err)
	}
	terminalConfig := terminalservice.Config{Enabled: true, Shell: "/bin/sh"}
	if socketPath != "" {
		terminalConfig.PrivilegedSocket = socketPath
	}
	terminalService, err := terminalservice.NewService(terminalConfig, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminalService.Shutdown)
	server, err := NewServer(&stubBundleService{}, logger, WithAuth(authService), WithTerminal(terminalService))
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	return &terminalFixture{
		server:       testServer,
		authService:  authService,
		sessionToken: session.Token,
		csrfToken:    session.CSRFToken,
		t:            t,
	}
}

func (f *terminalFixture) request(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, f.server.URL+path, body)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Cookie", sessionCookieName+"="+f.sessionToken)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-CSRF-Token", f.csrfToken)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return http.DefaultClient.Do(req)
}

func TestTerminalFilesRequireTwoFactor(t *testing.T) {
	// Build a session that is authenticated but NOT 2FA-verified, then assert
	// the file manager refuses the listing.
	root := t.TempDir()
	database, _ := store.Open(context.Background(), filepath.Join(root, "no2fa.db"))
	t.Cleanup(func() { _ = database.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secret.New(make([]byte, 32))
	authService, _ := auth.NewService(database, filepath.Join(root, "admin-setup-token"), logger, box)
	_ = authService.EnsureSetupToken(context.Background())
	token, _ := os.ReadFile(filepath.Join(root, "admin-setup-token"))
	_ = authService.Setup(context.Background(), strings.TrimSpace(string(token)), "admin", "correct horse battery")
	setup, _ := authService.BeginTwoFactorSetup(context.Background())
	code, _ := auth.TOTPCode(setup.Secret, time.Now())
	_ = authService.EnableTwoFactor(context.Background(), code)
	session, err := authService.Login(context.Background(), "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	terminalService, err := terminalservice.NewService(terminalservice.Config{Enabled: true}, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminalService.Shutdown)
	server, _ := NewServer(&stubBundleService{}, logger, WithAuth(authService), WithTerminal(terminalService))
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	req, _ := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/terminal/files?"+url.Values{"path": {t.TempDir()}}.Encode(), nil)
	req.Header.Set("Cookie", sessionCookieName+"="+session.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without verified 2FA, got %d", resp.StatusCode)
	}
}

func TestTerminalFilesListAndUploadDownload(t *testing.T) {
	f := newTerminalFixture(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// List
	q := url.Values{"path": {dir}}.Encode()
	resp, err := f.request(http.MethodGet, "/api/v1/terminal/files?"+q, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("list status %d: %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Upload via multipart
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	field, err := writer.CreateFormFile("file", "uploaded.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := field.Write([]byte("uploaded payload")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadQ := url.Values{"path": {dir}}.Encode()
	resp, err = f.request(http.MethodPost, "/api/v1/terminal/files?"+uploadQ, body, writer.FormDataContentType())
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	written, err := os.ReadFile(filepath.Join(dir, "uploaded.txt"))
	if err != nil || string(written) != "uploaded payload" {
		t.Fatalf("uploaded file content mismatch: %v %q", err, written)
	}

	// Download
	dlQ := url.Values{"path": {filepath.Join(dir, "uploaded.txt")}, "op": {"download"}}.Encode()
	resp, err = f.request(http.MethodGet, "/api/v1/terminal/files?"+dlQ, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("download status %d: %s", resp.StatusCode, b)
	}
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "uploaded payload" {
		t.Fatalf("download body = %q", data)
	}

	// Reject relative path
	badQ := url.Values{"path": {"relative/../x"}}.Encode()
	resp, err = f.request(http.MethodGet, "/api/v1/terminal/files?"+badQ, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative path, got %d", resp.StatusCode)
	}
}

func TestTerminalHostSnapshotGating(t *testing.T) {
	f := newTerminalFixture(t)
	// Metrics SSE: just assert it starts streaming a valid JSON sample.
	resp, err := f.request(http.MethodGet, "/api/v1/terminal/metrics", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status %d", resp.StatusCode)
	}
	buf := make([]byte, 8192)
	n, err := resp.Body.Read(buf)
	if err != nil && n == 0 {
		t.Fatalf("read metrics: %v", err)
	}
	if !strings.Contains(string(buf[:n]), `"processes"`) {
		t.Fatalf("metrics payload missing processes: %q", buf[:n])
	}
}

func TestSystemResourcesRequiresAuth(t *testing.T) {
	root := t.TempDir()
	database, _ := store.Open(context.Background(), filepath.Join(root, "sysres.db"))
	t.Cleanup(func() { _ = database.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := secret.New(make([]byte, 32))
	authService, err := auth.NewService(database, filepath.Join(root, "admin-setup-token"), logger, box)
	if err != nil {
		t.Fatal(err)
	}
	_ = authService.EnsureSetupToken(context.Background())
	token, _ := os.ReadFile(filepath.Join(root, "admin-setup-token"))
	_ = authService.Setup(context.Background(), strings.TrimSpace(string(token)), "admin", "correct horse battery")
	terminalService, err := terminalservice.NewService(terminalservice.Config{Enabled: true}, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminalService.Shutdown)
	server, err := NewServer(&stubBundleService{}, logger, WithAuth(authService), WithTerminal(terminalService))
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	// Unauthenticated call must be rejected.
	resp, err := http.Get(testServer.URL + "/api/v1/system/resources")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated, got %d", resp.StatusCode)
	}

	// Authenticated call returns a snapshot (2FA not required for this endpoint).
	session, err := authService.Login(context.Background(), "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/system/resources", nil)
	req.Header.Set("Cookie", sessionCookieName+"="+session.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 authenticated, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"processes"`) {
		t.Fatalf("system resources missing processes: %q", body)
	}
}

func TestTerminalFileErrorClassification(t *testing.T) {
	tests := []struct {
		name       string
		operation  string
		err        error
		wantCode   string
		wantStatus int
	}{
		{"permission", "file_list", &terminalservice.FileError{Code: "forbidden", Message: "permission denied"}, "file_list_forbidden", http.StatusForbidden},
		{"missing", "file_list", &terminalservice.FileError{Code: "not_found", Message: "path does not exist"}, "file_list_not_found", http.StatusNotFound},
		{"generic", "file_upload", &terminalservice.FileError{Code: "failed", Message: "boom"}, "file_upload_failed", http.StatusBadRequest},
		{"wrapped permission", "remove", fmt.Errorf("remove: %w", &terminalservice.FileError{Code: "forbidden", Message: "permission denied"}), "remove_forbidden", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, status := terminalFileError(tt.operation, tt.err)
			if code != tt.wantCode {
				t.Fatalf("code = %q, want %q", code, tt.wantCode)
			}
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
		})
	}
}

// Listing a missing directory must be a 404 with a stable not-found code so
// the file panel can render an inline state instead of a generic failure toast.
func TestTerminalFilesListMissingPath(t *testing.T) {
	f := newTerminalFixture(t)
	q := url.Values{"path": {filepath.Join(t.TempDir(), "does-not-exist")}}.Encode()
	resp, err := f.request(http.MethodGet, "/api/v1/terminal/files?"+q, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "file_list_not_found" {
		t.Fatalf("code = %q, want file_list_not_found", payload.Error.Code)
	}
}

// Listing a directory the control-plane user cannot read must be a 403 with a
// stable forbidden code so the file panel can render the inline state. Root
// bypasses DAC checks, so the case is skipped when running as root.
func TestTerminalFilesListForbidden(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; directory permissions are not enforced")
	}
	f := newTerminalFixture(t)
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	q := url.Values{"path": {locked}}.Encode()
	resp, err := f.request(http.MethodGet, "/api/v1/terminal/files?"+q, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "file_list_forbidden" {
		t.Fatalf("code = %q, want file_list_forbidden", payload.Error.Code)
	}
}

// TestTerminalFilesThroughPrivilegedHelper verifies the whole HTTP file-manager
// surface when the terminal service routes through the privileged helper: the
// control plane can list/stat/download/upload/mkdir/remove exactly what the
// helper process can see (root in production, current user in this test).
func TestTerminalFilesThroughPrivilegedHelper(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "terminal.sock")
	helperCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	helperDone := make(chan error, 1)
	go func() {
		helperDone <- terminalservice.RunHelper(helperCtx, terminalservice.HelperConfig{
			SocketPath:  socketPath,
			Shell:       "/bin/sh",
			MaxSessions: 2,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-helperDone; err != nil {
			t.Errorf("stop helper: %v", err)
		}
	})
	waitForSocketFile(t, socketPath)

	f := newTerminalFixtureWithSocket(t, socketPath)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helper.txt"), []byte("helper sees me"), 0o644); err != nil {
		t.Fatal(err)
	}

	// List through the helper.
	q := url.Values{"path": {dir}}.Encode()
	resp, err := f.request(http.MethodGet, "/api/v1/terminal/files?"+q, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var list terminalFileListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(list.Entries) != 1 || list.Entries[0].Name != "helper.txt" {
		t.Fatalf("privileged list failed: status=%d entries=%+v", resp.StatusCode, list.Entries)
	}

	// Stat through the helper.
	statQ := url.Values{"path": {filepath.Join(dir, "helper.txt")}, "op": {"stat"}}.Encode()
	resp, err = f.request(http.MethodGet, "/api/v1/terminal/files?"+statQ, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var stat struct {
		Size  int64 `json:"size"`
		IsDir bool  `json:"is_dir"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stat); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || stat.Size != int64(len("helper sees me")) || stat.IsDir {
		t.Fatalf("privileged stat failed: status=%d stat=%+v", resp.StatusCode, stat)
	}

	// Download through the helper.
	dlQ := url.Values{"path": {filepath.Join(dir, "helper.txt")}, "op": {"download"}}.Encode()
	resp, err = f.request(http.MethodGet, "/api/v1/terminal/files?"+dlQ, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != "helper sees me" {
		t.Fatalf("privileged download failed: status=%d body=%q err=%v", resp.StatusCode, body, err)
	}

	// Mkdir + upload through the helper.
	sub := filepath.Join(dir, "nested")
	mkdirBody := strings.NewReader(`{"path":"` + sub + `"}`)
	resp, err = f.request(http.MethodPost, "/api/v1/terminal/files/mkdir", mkdirBody, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("privileged mkdir failed: status=%d", resp.StatusCode)
	}
	uploadBody := &bytes.Buffer{}
	writer := multipart.NewWriter(uploadBody)
	field, err := writer.CreateFormFile("file", "up.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := field.Write([]byte("up via helper")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	upQ := url.Values{"path": {sub}}.Encode()
	resp, err = f.request(http.MethodPost, "/api/v1/terminal/files?"+upQ, uploadBody, writer.FormDataContentType())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("privileged upload failed: status=%d", resp.StatusCode)
	}
	written, err := os.ReadFile(filepath.Join(sub, "up.txt"))
	if err != nil || string(written) != "up via helper" {
		t.Fatalf("privileged upload content mismatch: %v %q", err, written)
	}

	// Remove through the helper.
	removeBody := strings.NewReader(`{"path":"` + filepath.Join(sub, "up.txt") + `"}`)
	resp, err = f.request(http.MethodPost, "/api/v1/terminal/files/remove", removeBody, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("privileged remove failed: status=%d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(sub, "up.txt")); !os.IsNotExist(err) {
		t.Fatalf("privileged remove did not delete: %v", err)
	}
}

func waitForSocketFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("helper socket %s did not appear", path)
}
