package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

// consumerNodeService is a listener service that publishes one exports-ready
// entry point, so the contract can be exercised without a database.
type consumerNodeService struct {
	edgeListenerService
	export listener.ShareExport
	err    error
}

func (s *consumerNodeService) ExportByShareToken(context.Context, string, string) (listener.ShareExport, error) {
	if s.err != nil {
		return listener.ShareExport{}, s.err
	}
	return s.export, nil
}

// consumerResidentialExporter stands in for a residential channel: it claims a
// token and answers with its own node set, exactly like a real channel does.
type consumerResidentialExporter struct {
	stubResidentialService
	exports []listener.ShareExport
	name    string
	matched bool
	err     error
}

func (s *consumerResidentialExporter) ShareExportsByShareToken(
	context.Context, string, string,
) ([]listener.ShareExport, string, bool, error) {
	return s.exports, s.name, s.matched, s.err
}

func newConsumerTestServer(t *testing.T, listeners ListenerService, residentialService ResidentialService) *httptest.Server {
	t.Helper()
	options := []Option{WithListeners(listeners)}
	if residentialService != nil {
		options = append(options, WithResidential(residentialService))
	}
	server, err := NewServer(&stubBundleService{}, slog.New(slog.NewTextHandler(io.Discard, nil)), options...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	return testServer
}

const consumerTestToken = "0123456789abcdef0123456789abcdef"

func TestConsumerNodesContract(t *testing.T) {
	t.Parallel()

	service := &consumerNodeService{export: listener.NewShareExport(
		"香港专线", "mixed", "proxy.example.com", 7890,
		[]listener.ShareNode{{Name: "香港专线-01", Auth: &listener.Auth{Username: "svc-3f9c", Password: "secret"}}},
		listener.Transport{},
		listener.PublicEndpoint{Host: "proxy.example.com", Port: 443},
	)}
	testServer := newConsumerTestServer(t, service, nil)

	response := doRequest(t, http.MethodGet, testServer.URL+listener.ConsumerNodesPath+consumerTestToken, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET nodes status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var payload struct {
		Name             string            `json:"name"`
		SharePath        string            `json:"share_path"`
		SubscriptionURLs map[string]string `json:"subscription_urls"`
		Nodes            []struct {
			Name              string `json:"name"`
			Protocol          string `json:"protocol"`
			Host              string `json:"host"`
			Port              int    `json:"port"`
			Transport         string `json:"transport"`
			TLS               bool   `json:"tls"`
			BrowserCompatible bool   `json:"browser_compatible"`
			Auth              *struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"auth"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Name != "香港专线" {
		t.Fatalf("name = %q", payload.Name)
	}
	if payload.SharePath != "/sub/"+consumerTestToken {
		t.Fatalf("share_path = %q", payload.SharePath)
	}
	for _, format := range listener.ConsumerSubscriptionFormats {
		want := "/sub/" + consumerTestToken + "?format=" + format
		if payload.SubscriptionURLs[format] != want {
			t.Fatalf("subscription_urls[%q] = %q, want %q", format, payload.SubscriptionURLs[format], want)
		}
	}
	if len(payload.Nodes) != 1 {
		t.Fatalf("nodes = %d", len(payload.Nodes))
	}
	node := payload.Nodes[0]
	if node.Protocol != "mixed" || node.Transport != "tcp" || !node.BrowserCompatible {
		t.Fatalf("node = %+v", node)
	}
	if node.Host != "proxy.example.com" || node.Port != 7890 {
		t.Fatalf("node endpoint = %s:%d", node.Host, node.Port)
	}
	if node.Auth == nil || node.Auth.Username != "svc-3f9c" || node.Auth.Password != "secret" {
		t.Fatalf("node auth = %+v", node.Auth)
	}
}

// The contract collapses every export failure into 404. A consumer must not be
// able to tell "token does not exist" from "token exists but is disabled" — and
// must not retry a not-found as though it were transient.
func TestConsumerNodesUnknownTokenIsNotFound(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: listener.ErrNotFound},
		{name: "share disabled", err: listener.ErrShareDisabled},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testServer := newConsumerTestServer(t, &consumerNodeService{err: testCase.err}, nil)
			response := doRequest(t, http.MethodGet, testServer.URL+listener.ConsumerNodesPath+consumerTestToken, nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", response.StatusCode)
			}
		})
	}
}

func TestConsumerNodesRejectsBadTokenAndMethod(t *testing.T) {
	t.Parallel()

	testServer := newConsumerTestServer(t, &consumerNodeService{err: listener.ErrNotFound}, nil)
	short := doRequest(t, http.MethodGet, testServer.URL+listener.ConsumerNodesPath+"short", nil)
	defer short.Body.Close()
	if short.StatusCode != http.StatusNotFound {
		t.Fatalf("short token status = %d, want 404", short.StatusCode)
	}
	missing := doRequest(t, http.MethodGet, testServer.URL+listener.ConsumerNodesPath, nil)
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing token status = %d, want 404", missing.StatusCode)
	}
	post := doRequest(t, http.MethodPost, testServer.URL+listener.ConsumerNodesPath+consumerTestToken, strings.NewReader("{}"))
	defer post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", post.StatusCode)
	}
}

// A residential channel token must yield the channel's declared sessions, not
// the listener's provisioning credential, so both token owners must produce the
// same payload shape.
func TestConsumerNodesResidentialTokenUsesChannelExports(t *testing.T) {
	t.Parallel()

	exports := []listener.ShareExport{listener.NewShareExport(
		"住宅美国", "vless", "proxy.example.com", 443,
		[]listener.ShareNode{{Name: "住宅美国-01", Auth: &listener.Auth{Password: "uuid-value"}}},
		listener.Transport{WSPath: "/__hx-proxy__/shared"},
		listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	)}
	residential := &consumerResidentialExporter{exports: exports, name: "住宅美国", matched: true}
	testServer := newConsumerTestServer(t, &consumerNodeService{err: errors.New("listener export must not be consulted")}, residential)

	response := doRequest(t, http.MethodGet, testServer.URL+listener.ConsumerNodesPath+consumerTestToken, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "住宅美国-01") {
		t.Fatalf("channel node missing from %s", body)
	}
	if strings.Contains(string(body), "uuid-value") == false {
		t.Fatalf("channel credential missing from %s", body)
	}
}

// TestConsumerNodesCoexistsWithAdminNodeRoutes pins the production wiring.
//
// The consumer listing and the administrator node list are different resources
// with different auth models, so they must never share a path: net/http's
// ServeMux panics on a duplicate registration, and an earlier revision that
// mounted the public listing at /api/v1/nodes took the whole daemon down at
// startup. Neither route's own test caught it, because each registered only the
// service it cares about — this one registers both, exactly as
// cmd/hx-pxyd does.
func TestConsumerNodesCoexistsWithAdminNodeRoutes(t *testing.T) {
	t.Parallel()

	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithNodes(&fakeNodeService{}),
		WithListeners(&consumerNodeService{err: listener.ErrNotFound}),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	// Handler() builds the mux; a duplicate pattern panics here.
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	// The public listing answers on its own token namespace.
	consumer := doRequest(t, http.MethodGet, testServer.URL+listener.ConsumerNodesPath+consumerTestToken, nil)
	defer consumer.Body.Close()
	if consumer.StatusCode != http.StatusNotFound {
		t.Fatalf("consumer route status = %d, want 404 from the stub", consumer.StatusCode)
	}
	// The administrator route keeps its own path and does not absorb the token.
	admin := doRequest(t, http.MethodGet, testServer.URL+"/api/v1/nodes", nil)
	defer admin.Body.Close()
	if admin.StatusCode == http.StatusNotFound {
		t.Fatalf("admin node route status = %d, want a non-404 admin response", admin.StatusCode)
	}
}
