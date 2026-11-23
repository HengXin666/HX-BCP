package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/HengXin666/hx-pxy/internal/residential"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// stubResidentialService records calls and returns canned results so the HTTP
// layer can be tested without a data plane.
type stubResidentialService struct {
	rotateByTokenCalls []string
	statusByTokenCalls []string
	rotateResult       residential.RotationResult
	rotateErr          error
	statusResult       residential.ChannelStatus
	statusErr          error
	channels           []residential.Channel
	providers          []residential.Provider
	testResult         residential.TestResult
	createdChannel     residential.CreateChannelRequest
	clientSession      residential.ClientSession
	clientSessionErr   error
	clientSessionCalls [][3]string
	cfSubscriptions    []residential.CfSubscription
	cfSubscriptionsErr error
}

type countryAwareStubResidentialService struct {
	*stubResidentialService
	requestedCountry string
}

type declaredControlStubResidentialService struct {
	*stubResidentialService
	controlCalls []string
}

func (s *declaredControlStubResidentialService) ControlNodesByToken(_ context.Context, token string) (residential.ControlNodeList, error) {
	s.controlCalls = append(s.controlCalls, "list:"+token)
	return residential.ControlNodeList{Channel: "residential-us", Nodes: []residential.ControlNode{{
		Index: 1, NodeName: "residential-us-01", RouteMode: residential.ClientRouteResidential,
		Endpoints: []residential.ControlEndpoint{{
			Protocol: "vless", Transport: "ws", URI: "vless://credential@proxy.example.com:443", BrowserCompatible: false,
		}},
		ResidentialEndpoint: &residential.ControlResidentialEndpoint{
			Protocol: "http", Server: "11.22.33.44", Port: 8000,
			Username: "endpoint-user", Password: "endpoint-secret",
		},
	}}}, s.clientSessionErr
}

func (s *declaredControlStubResidentialService) RotateDeclaredSession(_ context.Context, channelID string, index int) (residential.ChannelSession, error) {
	s.controlCalls = append(s.controlCalls, fmt.Sprintf("admin-next:%s:%d", channelID, index))
	return residential.ChannelSession{Index: index, NodeName: "residential-us-01"}, s.clientSessionErr
}

func (s *declaredControlStubResidentialService) RotateDeclaredSessionByControlToken(_ context.Context, token string, index int, _ residential.RotateOptions) (residential.ControlNode, error) {
	s.controlCalls = append(s.controlCalls, fmt.Sprintf("next:%s:%d", token, index))
	return residential.ControlNode{Index: index, NodeName: "residential-us-01", RouteMode: residential.ClientRouteResidential}, s.clientSessionErr
}

func (s *declaredControlStubResidentialService) SwitchDeclaredSessionRouteByControlToken(_ context.Context, token string, index int, routeMode string, _ residential.RotateOptions) (residential.ControlNode, error) {
	s.controlCalls = append(s.controlCalls, fmt.Sprintf("route:%s:%d:%s", token, index, routeMode))
	return residential.ControlNode{Index: index, NodeName: "residential-us-01", RouteMode: routeMode}, s.clientSessionErr
}

func (s *declaredControlStubResidentialService) ClaimDeclaredNodeByControlToken(_ context.Context, token string, index int, _ residential.LeaseRequest) (residential.ControlNode, error) {
	s.controlCalls = append(s.controlCalls, fmt.Sprintf("claim:%s:%d", token, index))
	return residential.ControlNode{Index: index, NodeName: "residential-us-01", RouteMode: residential.ClientRouteResidential}, s.clientSessionErr
}

func (s *declaredControlStubResidentialService) HeartbeatDeclaredNodeByControlToken(_ context.Context, token string, index int, _ residential.LeaseHeartbeatRequest) (residential.ControlNode, error) {
	s.controlCalls = append(s.controlCalls, fmt.Sprintf("heartbeat:%s:%d", token, index))
	return residential.ControlNode{Index: index, NodeName: "residential-us-01", RouteMode: residential.ClientRouteResidential}, s.clientSessionErr
}

func (s *declaredControlStubResidentialService) ReleaseDeclaredNodeByControlToken(_ context.Context, token string, index int, _ residential.LeaseReleaseRequest) (residential.ControlNode, error) {
	s.controlCalls = append(s.controlCalls, fmt.Sprintf("release:%s:%d", token, index))
	return residential.ControlNode{Index: index, NodeName: "residential-us-01", RouteMode: residential.ClientRouteResidential}, s.clientSessionErr
}

func (s *declaredControlStubResidentialService) RotateChannelShareToken(context.Context, string) (residential.Channel, error) {
	s.controlCalls = append(s.controlCalls, "rotate-share")
	return residential.Channel{ID: "residential-channel-1"}, s.clientSessionErr
}

func (s *declaredControlStubResidentialService) RotateChannelControlToken(context.Context, string) (residential.Channel, error) {
	s.controlCalls = append(s.controlCalls, "rotate-control")
	return residential.Channel{ID: "residential-channel-1"}, s.clientSessionErr
}

func (s *countryAwareStubResidentialService) EnsureClientSessionByTokenWithOptions(
	_ context.Context,
	token, sessionID string,
	options residential.ClientSessionOptions,
) (residential.ClientSession, error) {
	s.requestedCountry = options.CountryCode
	s.clientSessionCalls = append(s.clientSessionCalls, [3]string{"ensure-options", token, sessionID})
	return s.clientSession, s.clientSessionErr
}

func (s *stubResidentialService) ListProviders(context.Context) ([]residential.Provider, error) {
	return s.providers, nil
}

func (s *stubResidentialService) GetProvider(context.Context, string) (residential.Provider, error) {
	if len(s.providers) == 0 {
		return residential.Provider{}, residential.ErrNotFound
	}
	return s.providers[0], nil
}

func (s *stubResidentialService) CreateProvider(
	_ context.Context,
	request residential.CreateProviderRequest,
) (residential.Provider, error) {
	return residential.Provider{ID: "residential-provider-1", Name: request.Name}, nil
}

func (s *stubResidentialService) UpdateProvider(
	context.Context,
	string,
	residential.UpdateProviderRequest,
) (residential.Provider, error) {
	return residential.Provider{ID: "residential-provider-1"}, nil
}

func (s *stubResidentialService) DeleteProvider(context.Context, string, int) error { return nil }

func (s *stubResidentialService) TestProvider(context.Context, string, string) (residential.TestResult, error) {
	return s.testResult, nil
}

func (s *stubResidentialService) ListChannels(context.Context) ([]residential.Channel, error) {
	return s.channels, nil
}

func (s *stubResidentialService) GetChannel(context.Context, string) (residential.Channel, error) {
	if len(s.channels) == 0 {
		return residential.Channel{}, residential.ErrNotFound
	}
	return s.channels[0], nil
}

func (s *stubResidentialService) CreateChannel(
	_ context.Context,
	request residential.CreateChannelRequest,
) (residential.Channel, error) {
	s.createdChannel = request
	return residential.Channel{ID: "residential-channel-1", Name: request.Name, Mode: request.Mode}, nil
}

func (s *stubResidentialService) UpdateChannel(
	context.Context,
	string,
	residential.UpdateChannelRequest,
) (residential.Channel, error) {
	return residential.Channel{ID: "residential-channel-1"}, nil
}

func (s *stubResidentialService) DeleteChannel(context.Context, string, int) error { return nil }

func (s *stubResidentialService) RotateChannel(context.Context, string) (residential.RotationResult, error) {
	return s.rotateResult, s.rotateErr
}

func (s *stubResidentialService) RotateChannelByToken(
	_ context.Context,
	token string,
) (residential.RotationResult, error) {
	s.rotateByTokenCalls = append(s.rotateByTokenCalls, token)
	return s.rotateResult, s.rotateErr
}

func (s *stubResidentialService) ChannelStatusByToken(
	_ context.Context,
	token string,
) (residential.ChannelStatus, error) {
	s.statusByTokenCalls = append(s.statusByTokenCalls, token)
	return s.statusResult, s.statusErr
}

func (s *stubResidentialService) RotateChannelToken(context.Context, string) (residential.Channel, error) {
	return residential.Channel{ID: "residential-channel-1", RotatePath: "/rot/new-token"}, nil
}

func (s *stubResidentialService) RefreshChannelPool(context.Context, string) error { return nil }

func (s *stubResidentialService) CfWorkerSubscriptions(context.Context) ([]residential.CfSubscription, error) {
	return s.cfSubscriptions, s.cfSubscriptionsErr
}

func (s *stubResidentialService) EnsureClientSessionByToken(_ context.Context, token, sessionID string) (residential.ClientSession, error) {
	s.clientSessionCalls = append(s.clientSessionCalls, [3]string{"ensure", token, sessionID})
	return s.clientSession, s.clientSessionErr
}

func (s *stubResidentialService) GetClientSessionByToken(_ context.Context, token, sessionID string) (residential.ClientSession, error) {
	s.clientSessionCalls = append(s.clientSessionCalls, [3]string{"get", token, sessionID})
	return s.clientSession, s.clientSessionErr
}

func (s *stubResidentialService) ClientSessionConfigByToken(_ context.Context, token, sessionID string) (residential.ClientSession, error) {
	s.clientSessionCalls = append(s.clientSessionCalls, [3]string{"config", token, sessionID})
	return s.clientSession, s.clientSessionErr
}

func (s *stubResidentialService) RotateClientSessionByToken(_ context.Context, token, sessionID string) (residential.ClientSession, error) {
	s.clientSessionCalls = append(s.clientSessionCalls, [3]string{"next", token, sessionID})
	return s.clientSession, s.clientSessionErr
}

func (s *stubResidentialService) SwitchClientSessionRouteByToken(_ context.Context, token, sessionID, routeMode string) (residential.ClientSession, error) {
	s.clientSessionCalls = append(s.clientSessionCalls, [3]string{"route:" + routeMode, token, sessionID})
	return s.clientSession, s.clientSessionErr
}

func (s *stubResidentialService) DeleteClientSessionByToken(_ context.Context, token, sessionID string) error {
	s.clientSessionCalls = append(s.clientSessionCalls, [3]string{"delete", token, sessionID})
	return s.clientSessionErr
}

func newResidentialTestServer(t *testing.T, service ResidentialService) *httptest.Server {
	t.Helper()
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithResidential(service),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	return testServer
}

// The public rotate route must remain reachable without an administrator
// session, because the channel token is the credential. Everything under
// /api/v1/residential must stay behind authentication.
func TestPublicRotateRouteBypassesSessionAuthButAdminRoutesDoNot(t *testing.T) {
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "api-residential.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	authService, err := auth.NewService(
		database,
		filepath.Join(root, "admin-setup-token"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}
	if err := authService.EnsureSetupToken(context.Background()); err != nil {
		t.Fatalf("ensure setup token: %v", err)
	}
	// Configure an administrator so authentication is actually enforced.
	rawToken, err := os.ReadFile(filepath.Join(root, "admin-setup-token"))
	if err != nil {
		t.Fatalf("read setup token: %v", err)
	}
	if err := authService.Setup(
		context.Background(),
		strings.TrimSpace(string(rawToken)),
		"operator",
		"correct horse battery staple",
	); err != nil {
		t.Fatalf("setup admin: %v", err)
	}

	service := &stubResidentialService{
		rotateResult: residential.RotationResult{SessionIndex: 2, PoolSize: 4, RotatedAt: time.Now().UTC()},
		statusResult: residential.ChannelStatus{SessionIndex: 2, PoolSize: 4},
	}
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuth(authService),
		WithResidential(service),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	client := &http.Client{Timeout: 5 * time.Second}

	// Unauthenticated admin route: rejected.
	response, err := client.Get(testServer.URL + "/api/v1/residential/channels")
	if err != nil {
		t.Fatalf("GET channels: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin route status = %d, want 401", response.StatusCode)
	}

	// Unauthenticated public rotate: allowed, and no CSRF token required even
	// though it is a POST.
	rotateResponse, err := client.Post(testServer.URL+"/rot/consumer-token/next", "application/json", nil)
	if err != nil {
		t.Fatalf("POST rotate: %v", err)
	}
	defer rotateResponse.Body.Close()
	if rotateResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(rotateResponse.Body)
		t.Fatalf("public rotate status = %d, want 200, body = %s", rotateResponse.StatusCode, body)
	}
	if len(service.rotateByTokenCalls) != 1 || service.rotateByTokenCalls[0] != "consumer-token" {
		t.Fatalf("rotate token calls = %v", service.rotateByTokenCalls)
	}

	statusResponse, err := client.Get(testServer.URL + "/rot/consumer-token")
	if err != nil {
		t.Fatalf("GET rotate status: %v", err)
	}
	defer statusResponse.Body.Close()
	if statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("public status route = %d, want 200", statusResponse.StatusCode)
	}
}

// The public rotate response must not disclose internal identifiers or secrets.
func TestPublicRotateResponseOmitsInternalIdentifiers(t *testing.T) {
	service := &stubResidentialService{
		rotateResult: residential.RotationResult{
			ChannelID:    "residential-channel-secret",
			SessionIndex: 1,
			PoolSize:     3,
			LatencyMS:    42,
			RotatedAt:    time.Now().UTC(),
		},
	}
	testServer := newResidentialTestServer(t, service)

	response, err := http.Post(testServer.URL+"/rot/tok/next", "application/json", nil)
	if err != nil {
		t.Fatalf("POST rotate: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.Contains(string(body), "residential-channel-secret") {
		t.Fatalf("public rotate response leaked the channel id: %s", body)
	}
	if strings.Contains(string(body), "tok") && strings.Contains(string(body), "token") {
		t.Fatalf("public rotate response leaked the token: %s", body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["session_index"] != float64(1) || payload["pool_size"] != float64(3) {
		t.Fatalf("unexpected payload %v", payload)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header.Get("Cache-Control"))
	}
}

// An unknown, disabled or non-rotatable token must all look identical so the
// route cannot be used to enumerate channels.
func TestPublicRotateHidesLookupFailures(t *testing.T) {
	for name, failure := range map[string]error{
		"unknown token": residential.ErrNotFound,
		"not rotatable": residential.ErrInvalid,
	} {
		t.Run(name, func(t *testing.T) {
			service := &stubResidentialService{rotateErr: failure, statusErr: failure}
			testServer := newResidentialTestServer(t, service)

			response, err := http.Post(testServer.URL+"/rot/whatever/next", "application/json", nil)
			if err != nil {
				t.Fatalf("POST rotate: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", response.StatusCode)
			}
		})
	}
}

// Rate limiting must surface as 429 so a consumer can back off correctly.
func TestPublicRotateReportsRateLimitAsTooManyRequests(t *testing.T) {
	service := &stubResidentialService{
		rotateErr: errors.New("wrapped: " + residential.ErrRateLimited.Error()),
	}
	// Use the sentinel itself so errors.Is matches.
	service.rotateErr = residential.ErrRateLimited
	testServer := newResidentialTestServer(t, service)

	response, err := http.Post(testServer.URL+"/rot/tok/next", "application/json", nil)
	if err != nil {
		t.Fatalf("POST rotate: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", response.StatusCode)
	}
}

func TestPublicRotateRejectsUnknownPathsAndMethods(t *testing.T) {
	service := &stubResidentialService{}
	testServer := newResidentialTestServer(t, service)

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/rot/", http.StatusNotFound},
		{http.MethodPost, "/rot/tok/bogus", http.StatusNotFound},
		{http.MethodPost, "/rot/tok", http.StatusMethodNotAllowed},
		{http.MethodGet, "/rot/tok/next", http.StatusMethodNotAllowed},
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, testCase := range cases {
		request, err := http.NewRequest(testCase.method, testServer.URL+testCase.path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", testCase.method, testCase.path, err)
		}
		response.Body.Close()
		if response.StatusCode != testCase.want {
			t.Errorf("%s %s status = %d, want %d", testCase.method, testCase.path, response.StatusCode, testCase.want)
		}
	}
}

func TestPublicResidentialClientSessionLifecycleRoutesByTokenAndSessionID(t *testing.T) {
	service := &stubResidentialService{clientSession: residential.ClientSession{
		SessionID: "window-01", ProxyUsername: "hx-session-user",
		ProxyPassword: "one-time-secret", RouteMode: residential.ClientRouteResidential,
		SessionIndex: 2, PoolSize: 8,
	}}
	testServer := newResidentialTestServer(t, service)
	client := &http.Client{Timeout: 5 * time.Second}

	request, err := http.NewRequest(http.MethodPut, testServer.URL+"/rot/shared-token/sessions/window-01", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var ensured residential.ClientSession
	if err := json.NewDecoder(response.Body).Decode(&ensured); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || ensured.ProxyPassword != "one-time-secret" {
		t.Fatalf("ensure response = %d %+v", response.StatusCode, ensured)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header.Get("Cache-Control"))
	}

	routeRequest, err := http.NewRequest(
		http.MethodPost,
		testServer.URL+"/rot/shared-token/sessions/window-01/route",
		strings.NewReader(`{"route_mode":"direct"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	routeRequest.Header.Set("Content-Type", "application/json")
	routeResponse, err := client.Do(routeRequest)
	if err != nil {
		t.Fatal(err)
	}
	routeResponse.Body.Close()
	if routeResponse.StatusCode != http.StatusOK {
		t.Fatalf("route response status = %d", routeResponse.StatusCode)
	}

	wantCalls := [][3]string{
		{"ensure", "shared-token", "window-01"},
		{"route:direct", "shared-token", "window-01"},
	}
	if len(service.clientSessionCalls) != len(wantCalls) {
		t.Fatalf("client session calls = %v", service.clientSessionCalls)
	}
	for index := range wantCalls {
		if service.clientSessionCalls[index] != wantCalls[index] {
			t.Fatalf("client session call %d = %v, want %v", index, service.clientSessionCalls[index], wantCalls[index])
		}
	}
}

func TestPublicResidentialClientSessionConfigReturnsClashYAML(t *testing.T) {
	service := &stubResidentialService{clientSession: residential.ClientSession{
		SessionID: "window-01", ProxyUsername: "hx-session-user",
		ProxyPassword: "550e8400-e29b-41d4-a716-446655440000",
		ProxyEndpoint: &residential.ClientProxyEndpoint{
			Type: "vless-ws", Server: "proxy.example.com", Port: 443,
			TLS: true, SNI: "proxy.example.com", Path: "/__hx-proxy__/residential",
		},
	}}
	testServer := newResidentialTestServer(t, service)
	response, err := http.Get(testServer.URL + "/rot/shared-token/sessions/window-01/config?format=clash")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("config status = %d, body = %s", response.StatusCode, body)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "type: vless") || !strings.Contains(string(body), "proxy.example.com") {
		t.Fatalf("unexpected Clash config: %s", body)
	}
	if response.Header.Get("X-HX-Subscription-Format") != "clash" {
		t.Fatalf("subscription format = %q", response.Header.Get("X-HX-Subscription-Format"))
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q", response.Header.Get("Cache-Control"))
	}
	if len(service.clientSessionCalls) != 1 || service.clientSessionCalls[0][0] != "config" {
		t.Fatalf("config calls = %v", service.clientSessionCalls)
	}
}

func TestPublicResidentialClientSessionAcceptsCountryCodeAndEchoesIt(t *testing.T) {
	service := &countryAwareStubResidentialService{
		stubResidentialService: &stubResidentialService{
			clientSession: residential.ClientSession{
				SessionID: "window-country", ProxyUsername: "hx-session-user",
				ProxyPassword: "one-time-secret", CountryCode: "US",
				RouteMode: residential.ClientRouteResidential,
			},
		},
	}
	testServer := newResidentialTestServer(t, service)
	request, err := http.NewRequest(
		http.MethodPut,
		testServer.URL+"/rot/shared-token/sessions/window-country",
		strings.NewReader(`{"country_code":"US"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("country session status = %d, body = %s", response.StatusCode, body)
	}
	var payload residential.ClientSession
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if service.requestedCountry != "US" || payload.CountryCode != "US" {
		t.Fatalf("country request/response = %q / %q", service.requestedCountry, payload.CountryCode)
	}
}

func TestPublicResidentialClientSessionHidesInvalidTokenAndSession(t *testing.T) {
	service := &stubResidentialService{clientSessionErr: residential.ErrNotFound}
	testServer := newResidentialTestServer(t, service)
	request, err := http.NewRequest(http.MethodPut, testServer.URL+"/rot/unknown/sessions/window-01", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown session token status = %d, want 404", response.StatusCode)
	}
}

func TestPublicResidentialClientSessionReportsExpiredSession(t *testing.T) {
	service := &stubResidentialService{clientSessionErr: residential.ErrSessionExpired}
	testServer := newResidentialTestServer(t, service)
	request, err := http.NewRequest(http.MethodPut, testServer.URL+"/rot/shared-token/sessions/window-01", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusGone {
		t.Fatalf("expired session status = %d, want 410", response.StatusCode)
	}
}

func TestPublicResidentialClientSessionReportsInvalidRouteMode(t *testing.T) {
	service := &stubResidentialService{clientSessionErr: residential.ErrInvalid}
	testServer := newResidentialTestServer(t, service)
	request, err := http.NewRequest(
		http.MethodPost,
		testServer.URL+"/rot/shared-token/sessions/window-01/route",
		strings.NewReader(`{"route_mode":"unsupported"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid route mode status = %d, want 422", response.StatusCode)
	}
}

// The preset catalog drives the admin UI, including the unverified warning for
// vendors whose gateway syntax has not been confirmed.
func TestResidentialPresetsExposeVerificationState(t *testing.T) {
	testServer := newResidentialTestServer(t, &stubResidentialService{})

	response, err := http.Get(testServer.URL + "/api/v1/residential/presets")
	if err != nil {
		t.Fatalf("GET presets: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var payload struct {
		Items []struct {
			Vendor   string `json:"vendor"`
			Verified bool   `json:"verified"`
			DocURL   string `json:"doc_url"`
		} `json:"items"`
		Placeholders []string `json:"placeholders"`
		Protocols    []string `json:"protocols"`
		RegionModes  []string `json:"region_modes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode presets: %v", err)
	}
	if len(payload.Items) == 0 || len(payload.Placeholders) == 0 || len(payload.Protocols) == 0 || len(payload.RegionModes) == 0 {
		t.Fatalf("preset catalog is incomplete: %+v", payload)
	}
	found := false
	for _, item := range payload.Items {
		if item.Vendor == "bestproxy" {
			found = true
			if !item.Verified {
				t.Error("bestproxy preset must be reported as verified")
			}
			if item.DocURL == "" {
				t.Error("verified preset must still carry a doc URL")
			}
		}
	}
	if !found {
		t.Fatal("bestproxy preset missing from the catalog")
	}
}

func TestResidentialChannelCreateAndActions(t *testing.T) {
	service := &stubResidentialService{
		channels:     []residential.Channel{{ID: "residential-channel-1", Mode: residential.ModeSticky}},
		rotateResult: residential.RotationResult{SessionIndex: 1, PoolSize: 2},
	}
	testServer := newResidentialTestServer(t, service)
	client := &http.Client{Timeout: 5 * time.Second}

	response, err := client.Post(
		testServer.URL+"/api/v1/residential/channels",
		"application/json",
		strings.NewReader(`{"name":"sticky-us","provider_id":"p1","mode":"sticky","public_endpoint":{"host":"proxy.example.com"}}`),
	)
	if err != nil {
		t.Fatalf("POST channels: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want 201, body = %s", response.StatusCode, body)
	}
	if service.createdChannel.Name != "sticky-us" || service.createdChannel.PublicEndpoint.Host != "proxy.example.com" ||
		service.createdChannel.Listener.Port != 0 {
		t.Fatalf("decoded create request = %+v", service.createdChannel)
	}

	for _, action := range []string{"rotate", "rotate-token", "refresh-pool"} {
		actionResponse, err := client.Post(
			testServer.URL+"/api/v1/residential/channels/residential-channel-1/"+action,
			"application/json",
			nil,
		)
		if err != nil {
			t.Fatalf("POST %s: %v", action, err)
		}
		actionResponse.Body.Close()
		if actionResponse.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d, want 200", action, actionResponse.StatusCode)
		}
	}
}

// Admin rotation surfaces the rate limit as 429 too.
func TestAdminRotateReportsRateLimit(t *testing.T) {
	service := &stubResidentialService{
		channels:  []residential.Channel{{ID: "residential-channel-1"}},
		rotateErr: residential.ErrRateLimited,
	}
	testServer := newResidentialTestServer(t, service)

	response, err := http.Post(
		testServer.URL+"/api/v1/residential/channels/residential-channel-1/rotate",
		"application/json",
		nil,
	)
	if err != nil {
		t.Fatalf("POST rotate: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", response.StatusCode)
	}
}

func TestDeclaredResidentialControlRoutes(t *testing.T) {
	service := &declaredControlStubResidentialService{stubResidentialService: &stubResidentialService{}}
	testServer := newResidentialTestServer(t, service)

	response, err := http.Get(testServer.URL + "/ctl/control-token/nodes")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET control nodes status = %d", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header.Get("Cache-Control"))
	}
	var payload residential.ControlNodeList
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Nodes) != 1 || len(payload.Nodes[0].Endpoints) != 1 ||
		payload.Nodes[0].Endpoints[0].Protocol != "vless" || payload.Nodes[0].Endpoints[0].Transport != "ws" {
		t.Fatal("GET control nodes returned an invalid endpoint shape")
	}
	if payload.Nodes[0].ResidentialEndpoint == nil {
		t.Fatal("GET control nodes omitted the residential endpoint")
	}

	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/ctl/control-token/nodes/1/next"},
		{path: "/ctl/control-token/nodes/1/route", body: `{"route_mode":"direct"}`},
		{path: "/ctl/control-token/nodes/1/claim", body: `{"holder":"service-a","ttl_seconds":300}`},
		{path: "/ctl/control-token/nodes/1/heartbeat", body: `{"lease_id":"lease-token","ttl_seconds":300}`},
		{path: "/ctl/control-token/nodes/1/release", body: `{"lease_id":"lease-token"}`},
		{path: "/api/v1/residential/channels/residential-channel-1/sessions/1/next"},
		{path: "/api/v1/residential/channels/residential-channel-1/rotate-share"},
		{path: "/api/v1/residential/channels/residential-channel-1/rotate-control"},
	} {
		request, err := http.NewRequest(http.MethodPost, testServer.URL+test.path, strings.NewReader(test.body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		result, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		result.Body.Close()
		if result.StatusCode != http.StatusOK {
			t.Errorf("POST %s status = %d", test.path, result.StatusCode)
		}
		if strings.HasPrefix(test.path, "/ctl/") && result.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("POST control response Cache-Control = %q, want no-store", result.Header.Get("Cache-Control"))
		}
	}
	if len(service.controlCalls) != 9 {
		t.Fatalf("control calls = %v", service.controlCalls)
	}
}

// The unified-window guards surface as stable 409 codes on the control API.
func TestDeclaredControlLeaseAndVersionGuardsMapTo409(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "lease held", err: residential.ErrLeaseHeld, code: "lease_held"},
		{name: "lease expired", err: residential.ErrLeaseExpired, code: "lease_expired"},
		{name: "alloc version changed", err: residential.ErrAllocVersionChanged, code: "alloc_version_changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &declaredControlStubResidentialService{
				stubResidentialService: &stubResidentialService{clientSessionErr: test.err},
			}
			testServer := newResidentialTestServer(t, service)
			response, err := http.Post(
				testServer.URL+"/ctl/control-token/nodes/1/next",
				"application/json",
				strings.NewReader(`{"lease_id":"stale","expected_alloc_version":1}`),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409", response.StatusCode)
			}
			var payload struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Error.Code != test.code {
				t.Fatalf("error code = %q, want %q", payload.Error.Code, test.code)
			}
		})
	}
}

func TestResidentialControlLogsRedactTokenAndEndpointSecrets(t *testing.T) {
	service := &declaredControlStubResidentialService{stubResidentialService: &stubResidentialService{}}
	var logs bytes.Buffer
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(&logs, nil)),
		WithResidential(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	response, err := http.Get(testServer.URL + "/ctl/control-token-must-be-redacted/nodes")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET control nodes status = %d", response.StatusCode)
	}
	logged := logs.String()
	if !strings.Contains(logged, "path=/ctl/[redacted]") ||
		strings.Contains(logged, "control-token-must-be-redacted") ||
		strings.Contains(logged, "endpoint-user") ||
		strings.Contains(logged, "endpoint-secret") {
		t.Fatal("control request log did not preserve the redaction boundary")
	}
}

func TestResidentialControlUnknownTokenIsOpaque(t *testing.T) {
	service := &declaredControlStubResidentialService{stubResidentialService: &stubResidentialService{clientSessionErr: residential.ErrNotFound}}
	testServer := newResidentialTestServer(t, service)
	response, err := http.Get(testServer.URL + "/ctl/unknown-token/nodes")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown control token status = %d, want 404", response.StatusCode)
	}
}
