package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/auth"
	"github.com/HengXin666/hx-pxy/internal/hostnetwork"
	"github.com/HengXin666/hx-pxy/internal/metrics"
	"github.com/HengXin666/hx-pxy/internal/terminal"
)

type connectionTraffic struct{ fakeTrafficService }

type networkTestExecutor struct{ calls int }

func (e *networkTestExecutor) Execute(context.Context, string, time.Duration) (terminal.ExecResult, error) {
	e.calls++
	return terminal.ExecResult{Stdout: "diagnostic fixture"}, nil
}

func (*connectionTraffic) ActiveConnections() metrics.ActiveConnections {
	return metrics.ActiveConnections{Total: 2, Items: []metrics.ConnectionDetail{{ID: "a", Origin: "local"}, {ID: "b", Origin: "docker"}}}
}
func TestConnectionEndpointFiltersAndDoesNotCache(t *testing.T) {
	s, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithTraffic(&connectionTraffic{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, query string
		status        int
	}{
		{"GET", "?origin=docker", 200}, {"GET", "?origin=invalid", 422}, {"POST", "", 405},
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, "/api/v1/connections"+tc.query, nil))
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var value metrics.ActiveConnections
			if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			if len(value.Items) != 1 || value.Items[0].ID != "b" || value.Total != 2 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected snapshot: %+v", value)
			}
		}
	}
}
func TestNetworkOperationsRequireAuthConfirmationAndStepUp(t *testing.T) {
	ts, authService, tokenPath := newAuthTestServer(t)
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := authService.Setup(ctx, strings.TrimSpace(string(token)), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/connections", "/api/v1/system/network/inspect"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatalf("unauthenticated access: %s", path)
		}
	}
	session, err := authService.Login(ctx, "127.0.0.1", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{auth: authService, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"action":"enable-forwarding"}`, 422},
		{`{"action":"enable-forwarding","confirm":true}`, 403},
		{`{"action":"diagnose"}`, 503},
	} {
		r := httptest.NewRequest("POST", "/api/v1/system/network/operations", strings.NewReader(tc.body))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		w := httptest.NewRecorder()
		s.handleNetworkOperation(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
	setup, err := authService.BeginTwoFactorSetup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnableTwoFactor(ctx, code); err != nil {
		t.Fatal(err)
	}
	if err := authService.VerifyTwoFactor(ctx, session.Token, "127.0.0.1", code); err != nil {
		t.Fatal(err)
	}
	executor := &networkTestExecutor{}
	s.networkOps = hostnetwork.NewService(executor)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"action":"diagnose","container_id":"abcdef123456"}`, 200},
		{`{"action":"enable-forwarding","confirm":true}`, 200},
		{`{"action":"restart-docker","confirm":true}`, 422},
		{`{"action":"diagnose","container_id":";reboot"}`, 422},
	} {
		r := httptest.NewRequest("POST", "/api/v1/system/network/operations", strings.NewReader(tc.body))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		w := httptest.NewRecorder()
		s.handleNetworkOperation(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
	if executor.calls != 2 {
		t.Fatalf("invalid operations reached helper: %d", executor.calls)
	}
}
