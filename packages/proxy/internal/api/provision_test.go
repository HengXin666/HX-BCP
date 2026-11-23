package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/residential"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

func provisionTestServer(t *testing.T, service ResidentialService, settings systemsettings.Settings) *httptest.Server {
	t.Helper()
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithResidential(service),
		WithSettings(&fakeSettingsService{settings: settings}),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	return testServer
}

// The provision endpoint answers with every enabled cf-worker subscription URL
// when the token matches; consumers dial those workers directly (config
// center, not a relay). 20260821.
func TestProvisionConfigReturnsCfWorkerSubscriptions(t *testing.T) {
	service := &stubResidentialService{
		cfSubscriptions: []residential.CfSubscription{
			{Name: "a", URL: "https://alder42.alexrennie293.workers.dev/ab7851d339bf/sub?app=xray"},
			{Name: "b", URL: "https://willow95.alexrennie293.workers.dev/345b09e69e47/sub?app=xray"},
		},
	}
	settings := systemsettings.Default()
	settings.Provision.Enabled = true
	settings.Provision.Token = "cfg-token-42"
	testServer := provisionTestServer(t, service, settings)

	response, err := http.Get(testServer.URL + "/provision/cfg-token-42")
	if err != nil {
		t.Fatalf("GET /provision/<token> error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "alder42.alexrennie293.workers.dev") ||
		!strings.Contains(string(body), "willow95.alexrennie293.workers.dev") {
		t.Fatalf("body missing subscription URLs: %s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(body)), "sub?app=xray") {
		t.Fatalf("body should end with a subscription URL: %s", body)
	}
}

// Wrong, missing, or disabled tokens must 404 so the route existence stays
// unprobeable.
func TestProvisionConfigRejectsBadTokens(t *testing.T) {
	service := &stubResidentialService{
		cfSubscriptions: []residential.CfSubscription{{Name: "a", URL: "https://x.workers.dev/s/sub?app=xray"}},
	}
	enabled := systemsettings.Default()
	enabled.Provision.Enabled = true
	enabled.Provision.Token = "real-token"
	disabled := systemsettings.Default()
	disabled.Provision.Enabled = false

	cases := []struct {
		name     string
		settings systemsettings.Settings
		path     string
	}{
		{"wrong token", enabled, "/provision/not-the-token"},
		{"disabled center", disabled, "/provision/real-token"},
		{"empty token path", enabled, "/provision/"},
		{"path traversal", enabled, "/provision/real-token/.."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := provisionTestServer(t, service, tc.settings)
			response, err := http.Get(server.URL + tc.path)
			if err != nil {
				t.Fatalf("GET error = %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", response.StatusCode)
			}
		})
	}
}
