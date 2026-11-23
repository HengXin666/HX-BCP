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
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/auth"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

// newGuardTestServer builds the smallest stack that can answer the guard's
// question: a real auth service (the guard keys off the credential, so a stub
// session would test nothing), a settable guard flag, and the stub group
// service so the guarded routes exist and a request that passes the guard is
// observably *not* rejected.
func newGuardTestServer(t *testing.T, denyAPIGroupCreate bool) (*httptest.Server, *auth.Service) {
	t.Helper()
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "guard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	tokenPath := filepath.Join(root, "admin-setup-token")
	authService, err := auth.NewService(database, tokenPath, slog.New(slog.NewTextHandler(io.Discard, nil)), box)
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	if err := authService.EnsureSetupToken(context.Background()); err != nil {
		t.Fatalf("ensure setup token: %v", err)
	}
	settings := systemsettings.Default()
	// A pointer: the guard is tri-state so an omitted field can mean "unchanged".
	settings.Guard.DenyAPIGroupCreate = &denyAPIGroupCreate
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuth(authService),
		WithSettings(&fakeSettingsService{settings: settings}),
		WithProxyGroups(&stubProxyGroupService{}),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	rawToken, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	response := postJSON(t, testServer.Client(), testServer.URL+"/api/v1/auth/setup", map[string]string{
		"setup_token": strings.TrimSpace(string(rawToken)),
		"username":    "admin",
		"password":    "correct horse battery",
	}, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("setup failed with %d", response.StatusCode)
	}
	return testServer, authService
}

func guardAPIKey(t *testing.T, service *auth.Service) string {
	t.Helper()
	created, err := service.CreateAPIKey(context.Background(), "guard-test")
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	if created.Key == "" {
		t.Fatal("CreateAPIKey returned no secret")
	}
	return created.Key
}

// postWithHeaders sends a POST and returns only the status and decoded error
// code, which is all this file asserts on.
func postWithHeaders(t *testing.T, client *http.Client, url string, headers map[string]string) (int, string) {
	t.Helper()
	response := postJSON(t, client, url, map[string]string{}, headers)
	defer response.Body.Close()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(response.Body).Decode(&body)
	return response.StatusCode, body.Error.Code
}

// TestGroupCreationGuardBlocksOnlyMachineCreates pins the guard's whole
// contract in one place. Each case exists because a plausible implementation
// would get it wrong: blocking every caller instead of machine credentials,
// blocking reads as well, or guarding /proxy-groups while leaving the two
// endpoints that create a group as their first step wide open.
func TestGroupCreationGuardBlocksOnlyMachineCreates(t *testing.T) {
	guarded, authService := newGuardTestServer(t, true)
	key := guardAPIKey(t, authService)
	machine := map[string]string{"Authorization": "Bearer " + key}

	for _, path := range []string{"/api/v1/proxy-groups", "/api/v1/proxy-services", "/api/v1/quickstart"} {
		status, code := postWithHeaders(t, guarded.Client(), guarded.URL+path, machine)
		if status != http.StatusForbidden || code != "api_group_create_denied" {
			t.Errorf("guard on, API key %s = %d/%s, want 403/api_group_create_denied", path, status, code)
		}
	}

	// A read must stay open: the guard constrains who writes, not who reads.
	request, err := http.NewRequest(http.MethodGet, guarded.URL+"/api/v1/proxy-groups", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := guarded.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		t.Errorf("guard must not block GET /proxy-groups, got %d", response.StatusCode)
	}

	// A browser session is the operator, so it is never blocked — otherwise the
	// guard would remove the only way to create the multi-node groups it is
	// meant to leave as the supported shape.
	login := postJSON(t, guarded.Client(), guarded.URL+"/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "correct horse battery",
	}, nil)
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.NewDecoder(login.Body).Decode(&session)
	login.Body.Close()
	var cookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c.String()
		}
	}
	if cookie == "" || session.CSRFToken == "" {
		t.Fatal("login did not yield a session cookie and CSRF token")
	}
	status, code := postWithHeaders(t, guarded.Client(), guarded.URL+"/api/v1/proxy-groups",
		map[string]string{"Cookie": cookie, "X-CSRF-Token": session.CSRFToken})
	if status == http.StatusForbidden && code == "api_group_create_denied" {
		t.Error("a browser session must not be blocked by the guard")
	}
}

// TestGroupCreationGuardOffIsSilent is the negative control: without it, a
// guard that rejected every API-key POST unconditionally would still pass the
// test above.
func TestGroupCreationGuardOffIsSilent(t *testing.T) {
	unguarded, authService := newGuardTestServer(t, false)
	key := guardAPIKey(t, authService)
	status, code := postWithHeaders(t, unguarded.Client(), unguarded.URL+"/api/v1/proxy-groups",
		map[string]string{"Authorization": "Bearer " + key})
	if status == http.StatusForbidden && code == "api_group_create_denied" {
		t.Fatal("guard off must not reject API-key group creation")
	}
}
