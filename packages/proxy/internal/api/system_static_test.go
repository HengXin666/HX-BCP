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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/auth"
	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
)

type systemInfoDataPlane struct{}

type systemUpdateAuth struct{ verified bool }

func (*systemUpdateAuth) Configured(context.Context) (bool, error)            { return true, nil }
func (*systemUpdateAuth) Setup(context.Context, string, string, string) error { return nil }
func (*systemUpdateAuth) Login(context.Context, string, string, string) (auth.Session, error) {
	return auth.Session{}, nil
}
func (*systemUpdateAuth) Authenticate(context.Context, string) (auth.Session, error) {
	return auth.Session{Username: "admin", CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*systemUpdateAuth) Logout(context.Context, string) error                 { return nil }
func (*systemUpdateAuth) LogoutAll(context.Context) error                      { return nil }
func (*systemUpdateAuth) ChangePassword(context.Context, string, string) error { return nil }
func (*systemUpdateAuth) ChangeUsername(context.Context, string, string) error { return nil }
func (service *systemUpdateAuth) TwoFactorStatus(context.Context, string) (auth.TwoFactorStatus, error) {
	return auth.TwoFactorStatus{Configured: true, Enabled: true, Verified: service.verified}, nil
}
func (*systemUpdateAuth) BeginTwoFactorSetup(context.Context) (auth.TwoFactorSetup, error) {
	return auth.TwoFactorSetup{}, nil
}
func (*systemUpdateAuth) EnableTwoFactor(context.Context, string) error  { return nil }
func (*systemUpdateAuth) DisableTwoFactor(context.Context, string) error { return nil }
func (*systemUpdateAuth) VerifyTwoFactor(context.Context, string, string, string) error {
	return nil
}
func (*systemUpdateAuth) RenewTwoFactorVerification(context.Context, string) error { return nil }
func (*systemUpdateAuth) AuthenticateAPIKey(context.Context, string) (auth.Session, error) {
	return auth.Session{Username: "admin"}, nil
}
func (*systemUpdateAuth) CreateAPIKey(context.Context, string) (auth.APIKey, error) {
	return auth.APIKey{}, nil
}
func (*systemUpdateAuth) ListAPIKeys(context.Context) ([]auth.APIKey, error) {
	return nil, nil
}
func (*systemUpdateAuth) RevokeAPIKey(context.Context, string) error { return nil }

type systemUpdateService struct{ calls int }

func (service *systemUpdateService) TriggerUpdate(context.Context) error {
	service.calls++
	return nil
}

func (systemInfoDataPlane) Apply(context.Context) error                       { return nil }
func (systemInfoDataPlane) SelectProxy(context.Context, string, string) error { return nil }
func (systemInfoDataPlane) ReadGroup(context.Context, string) (mihomo.GroupSelection, error) {
	return mihomo.GroupSelection{}, nil
}
func (systemInfoDataPlane) Status() mihomo.Status {
	return mihomo.Status{Version: "Mihomo Meta 1.20.0"}
}

func TestSystemInfoAPI(t *testing.T) {
	t.Parallel()
	info := SystemInfo{
		Application:        "HX-Pxy",
		Version:            "v1.2.3",
		RepositoryURL:      "https://github.com/HengXin666/hx-pxy",
		UpdateCommand:      "sudo hx-pxy-install upgrade",
		SupportedProtocols: []string{"VLESS", "TUIC"},
	}
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSystemInfo(info),
		WithDataPlane(systemInfoDataPlane{}),
	)
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET system info status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		SystemInfo
		DataPlaneVersion string `json:"dataplane_version"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Application != info.Application ||
		response.Version != info.Version ||
		response.RepositoryURL != info.RepositoryURL ||
		response.UpdateCommand != info.UpdateCommand ||
		!slices.Equal(response.SupportedProtocols, info.SupportedProtocols) ||
		response.DataPlaneVersion != "Mihomo Meta 1.20.0" {
		t.Fatalf("system info = %#v", response)
	}

	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/system/info", nil))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST system info status = %d, Allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
}

func TestSystemUpdateRequiresStepUpAndSchedulesFixedUpdater(t *testing.T) {
	authService := &systemUpdateAuth{}
	updater := &systemUpdateService{}
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuth(authService),
		WithUpdater(updater),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := func() *http.Request {
		result := httptest.NewRequest(http.MethodPost, "/api/v1/system/update", nil)
		result.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
		result.Header.Set("X-CSRF-Token", "csrf")
		return result
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request())
	if recorder.Code != http.StatusForbidden || updater.calls != 0 {
		t.Fatalf("unverified update status=%d calls=%d", recorder.Code, updater.calls)
	}
	authService.verified = true
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request())
	if recorder.Code != http.StatusAccepted || updater.calls != 1 {
		t.Fatalf("verified update status=%d calls=%d body=%s", recorder.Code, updater.calls, recorder.Body.String())
	}
}

func TestSPAHandlerServesAssetsAndRouteFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>HX application</main>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app.js"), []byte("window.HX=true"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithWebRoot(root),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	for _, test := range []struct {
		name   string
		method string
		path   string
		status int
		body   string
	}{
		{name: "asset", method: http.MethodGet, path: "/assets/app.js", status: http.StatusOK, body: "window.HX=true"},
		{name: "route fallback", method: http.MethodGet, path: "/about", status: http.StatusOK, body: "HX application"},
		{name: "head fallback", method: http.MethodHead, path: "/subscriptions", status: http.StatusOK},
		{name: "write rejected", method: http.MethodPost, path: "/about", status: http.StatusMethodNotAllowed, body: "method not allowed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.body) {
				t.Fatalf("%s %s status=%d body=%q", test.method, test.path, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestWithWebRootRequiresIndex(t *testing.T) {
	t.Parallel()
	_, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithWebRoot(t.TempDir()),
	)
	if err == nil {
		t.Fatal("WithWebRoot accepted a directory without index.html")
	}
}
