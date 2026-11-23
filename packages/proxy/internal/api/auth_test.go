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
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/auth"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func newAuthTestServer(t *testing.T) (*httptest.Server, *auth.Service, string) {
	t.Helper()
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "api-auth.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	tokenPath := filepath.Join(root, "admin-setup-token")
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	authService, err := auth.NewService(database, tokenPath, slog.New(slog.NewTextHandler(io.Discard, nil)), box)
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	if err := authService.EnsureSetupToken(context.Background()); err != nil {
		t.Fatalf("ensure setup token: %v", err)
	}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithAuth(authService))
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	return testServer, authService, tokenPath
}

func postJSON(t *testing.T, client *http.Client, url string, body map[string]string, headers map[string]string) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestAuthBootstrapAndEnforcement(t *testing.T) {
	testServer, _, tokenPath := newAuthTestServer(t)
	client := testServer.Client()

	// Before setup, the loopback-bound API stays reachable.
	response, err := client.Get(testServer.URL + "/api/v1/backups")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("pre-setup API must remain open, got %d", response.StatusCode)
	}

	// Status reports not configured.
	response, err = client.Get(testServer.URL + "/api/v1/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	_ = json.NewDecoder(response.Body).Decode(&status)
	response.Body.Close()
	if status["configured"] != false {
		t.Fatalf("expected configured=false, got %v", status)
	}

	// Complete setup with the on-disk token.
	rawToken, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/setup", map[string]string{
		"setup_token": strings.TrimSpace(string(rawToken)),
		"username":    "admin",
		"password":    "correct horse battery",
	}, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("setup failed with %d", response.StatusCode)
	}

	// After setup, unauthenticated requests are rejected.
	response, err = client.Get(testServer.URL + "/api/v1/backups")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after setup, got %d", response.StatusCode)
	}

	// Login yields a session cookie and CSRF token.
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "correct horse battery",
	}, nil)
	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(response.Body).Decode(&login)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || login.CSRFToken == "" {
		t.Fatalf("login failed: %d", response.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly {
		t.Fatal("login must set an HttpOnly session cookie")
	}

	authedRequest, _ := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/backups", nil)
	authedRequest.AddCookie(sessionCookie)
	response, err = client.Do(authedRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated GET must succeed, got %d", response.StatusCode)
	}

	// TOTP setup returns the secret only during the explicit enrollment flow.
	requestHeaders := map[string]string{"Cookie": sessionCookie.String(), "X-CSRF-Token": login.CSRFToken}
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/2fa/setup", map[string]string{}, requestHeaders)
	var twoFactorSetup auth.TwoFactorSetup
	if err := json.NewDecoder(response.Body).Decode(&twoFactorSetup); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || twoFactorSetup.Secret == "" || twoFactorSetup.OTPAuthURL == "" {
		t.Fatalf("2FA setup failed: %d %+v", response.StatusCode, twoFactorSetup)
	}
	code, err := auth.TOTPCode(twoFactorSetup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/2fa/enable", map[string]string{"code": code}, requestHeaders)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("2FA enable failed: %d", response.StatusCode)
	}
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/2fa/verify", map[string]string{"code": code}, requestHeaders)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("2FA verify failed: %d", response.StatusCode)
	}

	authedRequest, _ = http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/auth/2fa/status", nil)
	authedRequest.AddCookie(sessionCookie)
	response, err = client.Do(authedRequest)
	if err != nil {
		t.Fatal(err)
	}
	var twoFactorStatus auth.TwoFactorStatus
	if err := json.NewDecoder(response.Body).Decode(&twoFactorStatus); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !twoFactorStatus.Enabled || !twoFactorStatus.Verified {
		t.Fatalf("unexpected 2FA status: %d %+v", response.StatusCode, twoFactorStatus)
	}

	// Mutating request without CSRF header is rejected.
	response = postJSON(t, client, testServer.URL+"/api/v1/backups", map[string]string{}, map[string]string{
		"Cookie": sessionCookie.String(),
	})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF token, got %d", response.StatusCode)
	}

	// Mutating request with CSRF header passes the auth layer.
	response = postJSON(t, client, testServer.URL+"/api/v1/backups", map[string]string{}, map[string]string{
		"Cookie":       sessionCookie.String(),
		"X-CSRF-Token": login.CSRFToken,
	})
	response.Body.Close()
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
		t.Fatalf("CSRF-carrying request must pass auth, got %d", response.StatusCode)
	}

	// Health endpoints stay open for systemd probes.
	response, err = client.Get(testServer.URL + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health must stay unauthenticated, got %d", response.StatusCode)
	}
}

func TestAuthUsernameChangeRequiresPasswordAndRevokesSession(t *testing.T) {
	testServer, service, tokenPath := newAuthTestServer(t)
	rawToken, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Setup(context.Background(), strings.TrimSpace(string(rawToken)), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	client := testServer.Client()

	response := postJSON(t, client, testServer.URL+"/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "correct horse battery",
	}, nil)
	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	var sessionCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("login did not return a session cookie")
	}

	body, _ := json.Marshal(map[string]string{"current_password": "correct horse battery", "new_username": "renamed-admin"})
	request, _ := http.NewRequest(http.MethodPut, testServer.URL+"/api/v1/auth/username", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", login.CSRFToken)
	request.AddCookie(sessionCookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("username change failed with %d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/backups", nil)
	request.AddCookie(sessionCookie)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session must be revoked, got %d", response.StatusCode)
	}

	response = postJSON(t, client, testServer.URL+"/api/v1/auth/login", map[string]string{
		"username": "renamed-admin",
		"password": "correct horse battery",
	}, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("new username login failed with %d", response.StatusCode)
	}
}

func TestAPIKeyAuthAndManagement(t *testing.T) {
	testServer, _, tokenPath := newAuthTestServer(t)
	client := testServer.Client()

	// Setup + login yields the cookie session and CSRF token.
	rawToken, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	response := postJSON(t, client, testServer.URL+"/api/v1/auth/setup", map[string]string{
		"setup_token": strings.TrimSpace(string(rawToken)),
		"username":    "admin",
		"password":    "correct horse battery",
	}, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("setup failed with %d", response.StatusCode)
	}
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "correct horse battery",
	}, nil)
	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(response.Body).Decode(&login)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || login.CSRFToken == "" {
		t.Fatalf("login failed with %d", response.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("login must set a session cookie")
	}

	// Create a key with the cookie session + CSRF; plaintext returned once.
	createHeaders := map[string]string{"Cookie": sessionCookie.String(), "X-CSRF-Token": login.CSRFToken}
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/api-keys", map[string]string{"name": "ci runner"}, createHeaders)
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	_ = json.NewDecoder(response.Body).Decode(&created)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || created.ID == "" || created.Key == "" {
		t.Fatalf("create api key failed with %d", response.StatusCode)
	}
	if !strings.HasPrefix(created.Key, "hxk_") {
		t.Fatalf("key %q must carry the hxk_ prefix", created.Key)
	}

	// Bearer key authenticates without cookie or CSRF.
	authedRequest, _ := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/backups", nil)
	authedRequest.Header.Set("Authorization", "Bearer "+created.Key)
	response, err = client.Do(authedRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bearer GET must succeed, got %d", response.StatusCode)
	}

	// Cookie session without CSRF is still rejected on mutating requests.
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/api-keys", map[string]string{"name": "no csrf"}, map[string]string{"Cookie": sessionCookie.String()})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie without CSRF: got %d, want 403", response.StatusCode)
	}

	// X-API-Key header also authenticates mutating requests without CSRF.
	response = postJSON(t, client, testServer.URL+"/api/v1/auth/api-keys", map[string]string{"name": "second"}, map[string]string{"X-API-Key": created.Key})
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("X-API-Key create failed with %d", response.StatusCode)
	}

	// Listing via the key never leaks secret material.
	listRequest, _ := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/auth/api-keys", nil)
	listRequest.Header.Set("Authorization", "Bearer "+created.Key)
	response, err = client.Do(listRequest)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []auth.APIKey `json:"items"`
	}
	_ = json.NewDecoder(response.Body).Decode(&list)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || len(list.Items) != 2 {
		t.Fatalf("list api keys: got %d with %d items, want 200 with 2", response.StatusCode, len(list.Items))
	}
	for _, item := range list.Items {
		if item.Key != "" {
			t.Fatalf("list leaked key material: %+v", item)
		}
	}

	// Empty id and unsupported methods are rejected while the key is valid.
	notFoundRequest, _ := http.NewRequest(http.MethodDelete, testServer.URL+"/api/v1/auth/api-keys/", nil)
	notFoundRequest.Header.Set("Authorization", "Bearer "+created.Key)
	response, err = client.Do(notFoundRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("empty id: got %d, want 404", response.StatusCode)
	}
	putRequest, _ := http.NewRequest(http.MethodPut, testServer.URL+"/api/v1/auth/api-keys/"+created.ID, nil)
	putRequest.Header.Set("Authorization", "Bearer "+created.Key)
	response, err = client.Do(putRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("PUT api key: got %d, want 405", response.StatusCode)
	}

	// Revoke via the key itself; the key must then be rejected.
	revokeRequest, _ := http.NewRequest(http.MethodDelete, testServer.URL+"/api/v1/auth/api-keys/"+created.ID, nil)
	revokeRequest.Header.Set("Authorization", "Bearer "+created.Key)
	response, err = client.Do(revokeRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke failed with %d", response.StatusCode)
	}
	authedRequest, _ = http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/backups", nil)
	authedRequest.Header.Set("Authorization", "Bearer "+created.Key)
	response, err = client.Do(authedRequest)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked key must be rejected, got %d", response.StatusCode)
	}
}
