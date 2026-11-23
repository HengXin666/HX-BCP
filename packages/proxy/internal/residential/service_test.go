package residential

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type stubReconciler struct {
	calls    int
	failNext error
}

func (reconciler *stubReconciler) Apply(context.Context) error {
	reconciler.calls++
	if reconciler.failNext != nil {
		err := reconciler.failNext
		reconciler.failNext = nil
		return err
	}
	return nil
}

// recordingSelector captures the data-plane selector switches a rotation makes,
// which is how we assert that rotation avoids a configuration recompile.
type recordingSelector struct {
	mu       sync.Mutex
	selects  [][2]string
	failWith error
}

func (selector *recordingSelector) SelectProxy(_ context.Context, group, proxy string) error {
	selector.mu.Lock()
	defer selector.mu.Unlock()
	if selector.failWith != nil {
		return selector.failWith
	}
	selector.selects = append(selector.selects, [2]string{group, proxy})
	return nil
}

func (selector *recordingSelector) calls() [][2]string {
	selector.mu.Lock()
	defer selector.mu.Unlock()
	return append([][2]string(nil), selector.selects...)
}

type stubProber struct {
	ip  string
	err error
}

type recordingSessionRouter struct {
	applyCalls int
	closed     []string
	failApply  error
}

func (router *recordingSessionRouter) Apply(context.Context) error {
	router.applyCalls++
	if router.failApply != nil {
		err := router.failApply
		router.failApply = nil
		return err
	}
	return nil
}

func (router *recordingSessionRouter) CloseConnectionsByInboundUser(_ context.Context, _ string, username string) error {
	router.closed = append(router.closed, username)
	return nil
}

func (prober *stubProber) ProbeExitIP(context.Context, string) (string, error) {
	return prober.ip, prober.err
}

type testHarness struct {
	service    *Service
	store      *store.Store
	box        *secret.Box
	selector   *recordingSelector
	reconciler *stubReconciler
}

func newHarness(t *testing.T, options ...Option) *testHarness {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secret.New() error = %v", err)
	}
	reconciler := &stubReconciler{}
	groups, err := proxygroup.NewService(database, reconciler)
	if err != nil {
		t.Fatalf("proxygroup.NewService() error = %v", err)
	}
	listeners, err := listener.NewService(database, box, reconciler)
	if err != nil {
		t.Fatalf("listener.NewService() error = %v", err)
	}
	selector := &recordingSelector{}
	options = append([]Option{WithSelector(selector), WithRotateInterval(time.Nanosecond)}, options...)
	service, err := NewService(database, box, groups, listeners, options...)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &testHarness{
		service:    service,
		store:      database,
		box:        box,
		selector:   selector,
		reconciler: reconciler,
	}
}

func managedPublicEndpoint() listener.PublicEndpoint {
	return listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true}
}

func (harness *testHarness) createProvider(t *testing.T) Provider {
	t.Helper()
	provider, err := harness.service.CreateProvider(context.Background(), CreateProviderRequest{
		Name:             "bestproxy-main",
		Vendor:           "bestproxy",
		Protocol:         "http",
		GatewayHost:      "gate.bestproxy.com",
		GatewayPort:      8000,
		Credentials:      &Credentials{Username: "acct123", Password: "s3cret"},
		UsernameTemplate: "{user}-region-{region}-session-{session}",
		RotationMode:     RotationSessionTemplate,
		PoolSize:         4,
		DefaultRegion:    "us",
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	return provider
}

func TestCreateProviderEncryptsCredentialsAndHidesPassword(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	provider := harness.createProvider(t)

	if provider.CredentialsConfigured != true {
		t.Fatal("provider should report configured credentials")
	}
	if provider.GatewayUsername != "acct123" {
		t.Fatalf("GatewayUsername = %q, want the account login", provider.GatewayUsername)
	}
	// The DTO must have no field capable of carrying the password back out.
	encoded, err := json.Marshal(provider)
	if err != nil {
		t.Fatalf("marshal provider: %v", err)
	}
	if strings.Contains(string(encoded), "s3cret") {
		t.Fatalf("provider JSON leaked the gateway password: %s", encoded)
	}

	record, err := harness.store.GetResidentialProvider(context.Background(), provider.ID)
	if err != nil {
		t.Fatalf("GetResidentialProvider() error = %v", err)
	}
	if strings.Contains(string(record.CredentialsEncrypted), "s3cret") {
		t.Fatal("credentials were stored in cleartext")
	}
	if !provider.SupportsSticky {
		t.Fatal("a session template provider must support sticky mode")
	}
}

func TestClientSessionsShareOneChannelButKeepIndependentRoutes(t *testing.T) {
	t.Parallel()
	router := &recordingSessionRouter{}
	harness := newHarness(t, WithSessionRouter(router))
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "multi-window", ProviderID: provider.ID, Mode: ModeSticky, PoolSize: 4,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}

	first, err := harness.service.EnsureClientSessionByToken(ctx, record.RotateToken, "window-01")
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionIndex != -1 || first.AllocatedAt == nil || first.ExpiresAt == nil {
		t.Fatalf("first lazy allocation = %+v", first)
	}
	encodedFirst, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedFirst), `"session_index":-1`) {
		t.Fatalf("lazy session marker missing from JSON: %s", encodedFirst)
	}
	second, err := harness.service.EnsureClientSessionByToken(ctx, record.RotateToken, "window-02")
	if err != nil {
		t.Fatal(err)
	}
	if first.ProxyUsername == second.ProxyUsername || first.ProxyPassword == second.ProxyPassword {
		t.Fatal("independent sessions received duplicate proxy credentials")
	}
	if first.ProxyPassword == "" || second.ProxyPassword == "" {
		t.Fatal("ensure session must return usable proxy credentials")
	}

	direct, err := harness.service.SwitchClientSessionRouteByToken(
		ctx, record.RotateToken, first.SessionID, ClientRouteDirect,
	)
	if err != nil {
		t.Fatal(err)
	}
	if direct.RouteMode != ClientRouteDirect || direct.SessionIndex != -1 {
		t.Fatalf("direct route = %+v", direct)
	}
	secondStatus, err := harness.service.GetClientSessionByToken(ctx, record.RotateToken, second.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if secondStatus.RouteMode != ClientRouteResidential || secondStatus.ExpiresAt == nil {
		t.Fatalf("switching first session changed second: %+v", secondStatus)
	}
	if len(router.closed) != 1 || router.closed[0] != first.ProxyUsername {
		t.Fatalf("closed users = %v, want only %q", router.closed, first.ProxyUsername)
	}

	restored, err := harness.service.SwitchClientSessionRouteByToken(
		ctx, record.RotateToken, first.SessionID, ClientRouteResidential,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.RouteMode != ClientRouteResidential || restored.ExpiresAt == nil {
		t.Fatalf("restored session did not receive a fresh allocation: %+v", restored)
	}
}

func TestResidentialChannelPersistsPublicEndpointAndRejectsLoopback(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	provider := harness.createProvider(t)
	ctx := context.Background()
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "public-residential", ProviderID: provider.ID, Mode: ModeSticky,
		PublicEndpoint: listener.PublicEndpoint{Host: "residential.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if channel.PublicEndpoint.Host != "residential.example.com" || channel.PublicEndpoint.Port != 443 || !channel.PublicEndpoint.TLS {
		t.Fatalf("public endpoint = %+v", channel.PublicEndpoint)
	}

	updated, err := harness.service.UpdateChannel(ctx, channel.ID, UpdateChannelRequest{
		Version:        channel.Version,
		Name:           channel.Name,
		PublicEndpoint: &listener.PublicEndpoint{Host: "127.0.0.1", Port: 443, TLS: true},
		Enabled:        channel.Enabled,
	})
	if !errors.Is(err, listener.ErrInvalid) {
		t.Fatalf("loopback endpoint update = %v, want listener.ErrInvalid", err)
	}
	if updated.ID != "" {
		t.Fatalf("failed endpoint update returned channel: %+v", updated)
	}
}

func TestResidentialChannelSupportsCloudflareWebSocketEntry(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	provider := harness.createProvider(t)
	ctx := context.Background()
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "residential-cloudflare-ws", ProviderID: provider.ID, Mode: ModePassthrough,
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if channel.Endpoint.Kind != "vless" || !strings.HasPrefix(channel.Endpoint.Transport.WSPath, listener.WebSocketPathPrefix+"residential/") {
		t.Fatalf("channel endpoint = %+v", channel.Endpoint)
	}
	if channel.Endpoint.SharePath == "" {
		t.Fatal("WebSocket residential channel did not expose its subscription path")
	}
	if !strings.HasPrefix(channel.SubscriptionURL, "https://proxy.example.com/sub/") || !strings.HasSuffix(channel.SubscriptionURL, "?format=clash") {
		t.Fatalf("subscription URL = %q, want a portless Clash path URL", channel.SubscriptionURL)
	}
	if strings.Contains(channel.SubscriptionURL, ":443") {
		t.Fatalf("subscription URL exposes default port: %q", channel.SubscriptionURL)
	}
	listenerRecord, err := harness.store.GetListener(ctx, channel.ListenerID)
	if err != nil {
		t.Fatal(err)
	}
	if listenerRecord.Kind != "vless" || !strings.Contains(listenerRecord.TransportJSON, listener.WebSocketPathPrefix+"residential/") {
		t.Fatalf("stored listener = %+v", listenerRecord)
	}

	updated, err := harness.service.UpdateChannel(ctx, channel.ID, UpdateChannelRequest{
		Version: channel.Version,
		Name:    channel.Name,
		PublicEndpoint: &listener.PublicEndpoint{
			Host: "proxy-updated.example.com", Port: 443, TLS: true,
		},
		Enabled: channel.Enabled,
	})
	if err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	if updated.Endpoint.Transport.WSPath != channel.Endpoint.Transport.WSPath {
		t.Fatalf("updated WebSocket path = %q, want stable %q", updated.Endpoint.Transport.WSPath, channel.Endpoint.Transport.WSPath)
	}
	if !strings.HasPrefix(updated.SubscriptionURL, "https://proxy-updated.example.com/sub/") {
		t.Fatalf("updated subscription URL = %q", updated.SubscriptionURL)
	}
}

func TestManagedResidentialChannelsAllocatePrivateVLESSListeners(t *testing.T) {
	harness := newHarness(t)
	provider := harness.createProvider(t)
	ctx := context.Background()

	create := func(name string) Channel {
		channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
			Name: name, ProviderID: provider.ID, Mode: ModeSticky, SessionCount: 2,
			PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 80},
		})
		if err != nil {
			t.Fatal(err)
		}
		return channel
	}
	first := create("managed-vless-one")
	second := create("managed-vless-two")
	if first.Endpoint.Kind != "vless" || first.Endpoint.BindAddress != "127.0.0.1" ||
		first.Endpoint.Port != residentialInternalPortStart || second.Endpoint.Port != residentialInternalPortStart+1 {
		t.Fatalf("managed endpoints = first:%+v second:%+v", first.Endpoint, second.Endpoint)
	}
	if first.Endpoint.Transport.Type != "ws" ||
		!strings.HasPrefix(first.Endpoint.Transport.WSPath, listener.WebSocketPathPrefix+"residential/") ||
		first.Endpoint.Transport.WSPath == second.Endpoint.Transport.WSPath {
		t.Fatalf("managed WebSocket paths = %q, %q", first.Endpoint.Transport.WSPath, second.Endpoint.Transport.WSPath)
	}
	if first.PublicEndpoint.Port != 443 || !first.PublicEndpoint.TLS || first.SubscriptionURL == "" {
		t.Fatalf("managed public endpoint = %+v, subscription = %q", first.PublicEndpoint, first.SubscriptionURL)
	}
	stored, err := harness.store.GetListener(ctx, first.ListenerID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AuthMode != "userpass" || len(stored.AuthConfigEncrypted) == 0 {
		t.Fatalf("managed listener bootstrap credential was not encrypted: %+v", stored)
	}
}

func TestManagedResidentialChannelRequiresPublicHostAndRejectsDirectListener(t *testing.T) {
	harness := newHarness(t)
	provider := harness.createProvider(t)
	ctx := context.Background()

	_, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "missing-public-host", ProviderID: provider.ID, Mode: ModeSticky, SessionCount: 1,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing public host error = %v, want ErrInvalid", err)
	}
	_, err = harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "direct-rejected", ProviderID: provider.ID, Mode: ModeSticky, SessionCount: 1,
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
		DirectListener: &ChannelListenerRequest{
			Kind: "http", BindAddress: "203.0.113.10", Port: 18080,
			Auth: &listener.Auth{Username: "user", Password: "password"},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("direct listener error = %v, want ErrInvalid", err)
	}

	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "managed-update", ProviderID: provider.ID, Mode: ModeSticky, SessionCount: 1,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = harness.service.UpdateChannel(ctx, channel.ID, UpdateChannelRequest{
		Version: channel.Version, Name: channel.Name, Enabled: channel.Enabled,
		DirectListener: &ChannelListenerRequest{
			Kind: "socks", BindAddress: "203.0.113.10", Port: 18081,
			Auth: &listener.Auth{Username: "user", Password: "password"},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("updated direct listener error = %v, want ErrInvalid", err)
	}
}

func TestResidentialChannelRejectsClientManagedListener(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	provider := harness.createProvider(t)

	_, err := harness.service.CreateChannel(context.Background(), CreateChannelRequest{
		Name: "public-bind-rejected", ProviderID: provider.ID, Mode: ModeSticky,
		Listener:       ChannelListenerRequest{Kind: "mixed", BindAddress: "0.0.0.0", Port: 29309},
		PublicEndpoint: managedPublicEndpoint(),
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("client-managed listener error = %v, want ErrInvalid", err)
	}

	_, err = harness.service.CreateChannel(context.Background(), CreateChannelRequest{
		Name: "public-port-rejected", ProviderID: provider.ID, Mode: ModePassthrough,
		Listener: ChannelListenerRequest{
			Kind: "vless", BindAddress: "127.0.0.1", Port: 29310,
			Auth:      &listener.Auth{Username: "entry", Password: "550e8400-e29b-41d4-a716-446655440000"},
			Transport: listener.Transport{Type: "ws", WSPath: "/non443"},
		},
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 32825, TLS: true},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("client-managed WebSocket error = %v, want ErrInvalid", err)
	}
}

func TestResidentialWebSocketSessionGetsUUIDCredential(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	provider := harness.createProvider(t)
	ctx := context.Background()
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "residential-vless-session", ProviderID: provider.ID, Mode: ModeSticky,
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(channel.RotationURL, "https://proxy.example.com/rot/") || strings.Contains(channel.RotationURL, ":443") {
		t.Fatalf("rotation URL = %q, want a portless HTTPS path URL", channel.RotationURL)
	}
	record, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := harness.service.EnsureClientSessionByToken(ctx, record.RotateToken, "window-vless")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.ProxyPassword) != 36 || session.ProxyPassword[8] != '-' || session.ProxyPassword[13] != '-' || session.ProxyPassword[18] != '-' || session.ProxyPassword[23] != '-' {
		t.Fatalf("session credential = %q, want UUID", session.ProxyPassword)
	}
	if session.ProxyEndpoint == nil || session.ProxyEndpoint.Type != "vless-ws" ||
		session.ProxyEndpoint.Server != "proxy.example.com" ||
		session.ProxyEndpoint.Path != channel.Endpoint.Transport.WSPath {
		t.Fatalf("session endpoint = %+v", session.ProxyEndpoint)
	}
}

func TestClientSessionRejectsConflictingCountryPin(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name: "country-pinned", Vendor: "custom", Protocol: "http",
		GatewayHost: "gateway.example.com", GatewayPort: 8000,
		Credentials:      &Credentials{Username: "acct", Password: "secret"},
		UsernameTemplate: "{user}-region-{region}-session-{session}",
		RotationMode:     RotationSessionTemplate, PoolSize: 4,
		DefaultRegionMode:    RegionModeApplicationRandom,
		DefaultRandomRegions: []string{"US", "JP"},
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "country-pinned-channel", ProviderID: provider.ID, Mode: ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	record, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := harness.service.EnsureClientSessionByTokenWithOptions(
		ctx, record.RotateToken, "window-country", ClientSessionOptions{CountryCode: "US"},
	)
	if err != nil {
		t.Fatalf("first country session = %v", err)
	}
	if first.CountryCode != "US" {
		t.Fatalf("first country = %q, want US", first.CountryCode)
	}
	if _, err := harness.service.EnsureClientSessionByTokenWithOptions(
		ctx, record.RotateToken, "window-country", ClientSessionOptions{CountryCode: "JP"},
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("conflicting country error = %v, want ErrInvalid", err)
	}
}

func TestClientSessionApplyFailureRollsBackCreation(t *testing.T) {
	t.Parallel()
	router := &recordingSessionRouter{failApply: errors.New("apply failed")}
	harness := newHarness(t, WithSessionRouter(router))
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "multi-window-rollback", ProviderID: provider.ID, Mode: ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.EnsureClientSessionByToken(ctx, record.RotateToken, "window-01"); err == nil {
		t.Fatal("EnsureClientSessionByToken() succeeded despite apply failure")
	}
	if _, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "window-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed creation left a session record: %v", err)
	}
}

func TestExpiredClientSessionRotatesAllocationAndKeepsCredentials(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	harness := newHarness(t, WithClock(func() time.Time { return now }))
	ctx := context.Background()
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name: "ttl-rotate", Vendor: "custom", Protocol: "http",
		GatewayHost: "gateway.example.com", GatewayPort: 8000,
		Credentials:      &Credentials{Username: "acct", Password: "secret"},
		UsernameTemplate: "{user}-session-{session}", RotationMode: RotationSessionTemplate,
		SessionTTLSeconds: 60, SessionExpiryPolicy: "rotate",
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "ttl-rotate-channel", ProviderID: provider.ID, Mode: ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	channelRecord, _ := harness.store.GetResidentialChannel(ctx, channel.ID)
	first, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, "window-01")
	if err != nil {
		t.Fatal(err)
	}
	firstRecord, _ := harness.store.GetResidentialClientSession(ctx, channel.ID, first.SessionID)
	now = now.Add(61 * time.Second)
	rotated, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, _ := harness.store.GetResidentialClientSession(ctx, channel.ID, first.SessionID)
	if rotated.ProxyUsername != first.ProxyUsername || rotated.ProxyPassword != first.ProxyPassword {
		t.Fatal("expiry rotation changed client proxy credentials")
	}
	if firstRecord.NodeFingerprint == secondRecord.NodeFingerprint || secondRecord.RotateCount != 1 {
		t.Fatalf("allocation was not rotated: before=%+v after=%+v", firstRecord, secondRecord)
	}
}

// RapidProxy stores its sticky session time in the username (`stime`) in
// minutes. The generic TTL field defaults to 60 and is validated against the
// vendor's 1-180 minute range at provider save time.
func TestRapidProxyProviderDefaultsAndValidatesStime(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()

	// A RapidProxy provider with no explicit TTL falls back to 60 minutes (the
	// preset default), not the generic 600-second default.
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name: "rapid-1", Vendor: "rapidproxy", Protocol: "http",
		GatewayHost:      "us.rapidproxy.io",
		GatewayPort:      5001,
		Credentials:      &Credentials{Username: "acct", Password: "secret"},
		UsernameTemplate: "{user}-residential-{region}-session-{session}-stime-{ttl}",
		RotationMode:     RotationSessionTemplate,
		DefaultRegion:    "GLOBAL",
	})
	if err != nil {
		t.Fatalf("CreateProvider(RapidProxy) error = %v", err)
	}
	if got, want := provider.SessionTTLSeconds, 60; got != want {
		t.Fatalf("RapidProxy default stime = %d, want %d", got, want)
	}

	// stime below the documented 1-180 minute range is rejected.
	if _, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name: "rapid-bad", Vendor: "rapidproxy", Protocol: "http",
		GatewayHost:       "us.rapidproxy.io",
		GatewayPort:       5001,
		Credentials:       &Credentials{Username: "acct", Password: "secret"},
		UsernameTemplate:  "{user}-residential-{region}-session-{session}-stime-{ttl}",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 200,
		DefaultRegion:     "GLOBAL",
	}); err == nil {
		t.Fatal("RapidProxy provider with out-of-range stime 200 succeeded")
	}
}

func TestExpiredClientSessionCanBeConfiguredToExpire(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	harness := newHarness(t, WithClock(func() time.Time { return now }))
	ctx := context.Background()
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name: "ttl-expire", Vendor: "custom", Protocol: "http",
		GatewayHost: "gateway.example.com", GatewayPort: 8000,
		Credentials:      &Credentials{Username: "acct", Password: "secret"},
		UsernameTemplate: "{user}-session-{session}", RotationMode: RotationSessionTemplate,
		SessionTTLSeconds: 60, SessionExpiryPolicy: "expire",
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name: "ttl-expire-channel", ProviderID: provider.ID, Mode: ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	channelRecord, _ := harness.store.GetResidentialChannel(ctx, channel.ID)
	if _, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, "window-01"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(61 * time.Second)
	if _, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, "window-01"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired session error = %v, want ErrSessionExpired", err)
	}
	if _, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "window-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session was not deleted: %v", err)
	}
	nodes, err := harness.store.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil || len(nodes) != 0 {
		t.Fatalf("expired session nodes = %d, error = %v", len(nodes), err)
	}
}

func TestCreateProviderRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	base := CreateProviderRequest{
		Name:             "vendor",
		Protocol:         "http",
		GatewayHost:      "gate.example.com",
		GatewayPort:      8000,
		Credentials:      &Credentials{Username: "acct", Password: "pw"},
		UsernameTemplate: "{user}-session-{session}",
		RotationMode:     RotationSessionTemplate,
	}
	cases := map[string]func(*CreateProviderRequest){
		"empty name":             func(r *CreateProviderRequest) { r.Name = " " },
		"bad protocol":           func(r *CreateProviderRequest) { r.Protocol = "vmess" },
		"loopback gateway":       func(r *CreateProviderRequest) { r.GatewayHost = "127.0.0.1" },
		"localhost gateway":      func(r *CreateProviderRequest) { r.GatewayHost = "localhost" },
		"unspecified gateway":    func(r *CreateProviderRequest) { r.GatewayHost = "0.0.0.0" },
		"bad port":               func(r *CreateProviderRequest) { r.GatewayPort = 0 },
		"bad template":           func(r *CreateProviderRequest) { r.UsernameTemplate = "{nope}" },
		"sticky without session": func(r *CreateProviderRequest) { r.UsernameTemplate = "{user}" },
		"missing credentials":    func(r *CreateProviderRequest) { r.Credentials = nil },
		"colon in username":      func(r *CreateProviderRequest) { r.Credentials = &Credentials{Username: "a:b", Password: "pw"} },
		"api-list unsupported":   func(r *CreateProviderRequest) { r.RotationMode = RotationAPIList },
		"bad region":             func(r *CreateProviderRequest) { r.DefaultRegion = "us west" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			if _, err := harness.service.CreateProvider(context.Background(), request); !errors.Is(err, ErrInvalid) {
				t.Fatalf("CreateProvider() error = %v, want ErrInvalid", err)
			}
		})
	}
}

// A per-request gateway has no session to pin, so its pool collapses to one
// upstream and sticky channels must be refused.
func TestPerRequestProviderCannotBackStickyChannel(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()

	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name:             "rotating",
		Protocol:         "http",
		GatewayHost:      "gate.example.com",
		GatewayPort:      8000,
		Credentials:      &Credentials{Username: "acct", Password: "pw"},
		UsernameTemplate: "{user}",
		RotationMode:     RotationPerRequest,
		PoolSize:         8,
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if provider.PoolSize != 1 {
		t.Fatalf("per-request PoolSize = %d, want 1", provider.PoolSize)
	}
	if provider.SupportsSticky {
		t.Fatal("per-request provider must not advertise sticky support")
	}

	_, err = harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:       "sticky-attempt",
		ProviderID: provider.ID,
		Mode:       ModeSticky,
		Listener:   ChannelListenerRequest{Kind: "mixed", BindAddress: "127.0.0.1", Port: 29101},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateChannel(sticky) error = %v, want ErrInvalid", err)
	}
}

func TestCreateStickyChannelDefersIPAllocationUntilClientSession(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)

	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "sticky-us",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		Region:         "us",
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.ActiveSessionCount != 0 || channel.PoolSize != 0 {
		t.Fatalf("channel allocated IPs during creation: %+v", channel)
	}
	if channel.CanRotate {
		t.Fatal("an empty lazy channel must not expose global pool rotation")
	}
	if !strings.HasPrefix(channel.RotatePath, "/rot/") {
		t.Fatalf("RotatePath = %q, want a /rot/ path", channel.RotatePath)
	}
	if channel.Endpoint.Port != residentialInternalPortStart || channel.Endpoint.Kind != "vless" ||
		channel.Endpoint.BindAddress != "127.0.0.1" || channel.Endpoint.Transport.Type != "ws" {
		t.Fatalf("unexpected endpoint %+v", channel.Endpoint)
	}

	// Creating the channel must not consume an expiring vendor IP.
	pool, err := harness.store.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil {
		t.Fatalf("ListResidentialSessionNodes() error = %v", err)
	}
	if len(pool) != 0 {
		t.Fatalf("channel creation materialized %d nodes, want none", len(pool))
	}
	group, err := harness.store.GetProxyGroup(ctx, channel.ProxyGroupID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(group.SourceSpecJSON, `"allow_empty":true`) {
		t.Fatalf("dormant group is not explicitly fail-closed: %s", group.SourceSpecJSON)
	}
}

// Passthrough channels leave rotation to the vendor: one upstream, no rotate
// token, and the rotate API must refuse them.
func TestPassthroughChannelHasSingleUpstreamAndCannotRotate(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)

	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "passthrough-us",
		ProviderID:     provider.ID,
		Mode:           ModePassthrough,
		PoolSize:       8,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.PoolSize != 1 {
		t.Fatalf("passthrough PoolSize = %d, want 1", channel.PoolSize)
	}
	if channel.CanRotate || channel.RotatePath != "" {
		t.Fatalf("passthrough channel must not expose rotation: %+v", channel)
	}
	if !channel.Endpoint.AuthEnabled || channel.Endpoint.Kind != "vless" {
		t.Fatal("managed passthrough listener must use authenticated VLESS")
	}
	if _, err := harness.service.RotateChannel(ctx, channel.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("RotateChannel(passthrough) error = %v, want ErrInvalid", err)
	}
}

func TestCreateChannelRejectsUnauthenticatedPublicListener(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	provider := harness.createProvider(t)

	_, err := harness.service.CreateChannel(context.Background(), CreateChannelRequest{
		Name:       "public-open",
		ProviderID: provider.ID,
		Mode:       ModePassthrough,
		Listener:   ChannelListenerRequest{Kind: "mixed", BindAddress: "0.0.0.0", Port: 29104},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateChannel() error = %v, want ErrInvalid", err)
	}
	// The compensating delete must leave nothing behind.
	channels, err := harness.service.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("a failed create left %d channels behind", len(channels))
	}
	groups, err := harness.store.ListProxyGroups(context.Background())
	if err != nil {
		t.Fatalf("ListProxyGroups() error = %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("a failed create left %d proxy groups behind", len(groups))
	}
	nodes, err := harness.store.ListNodeConfigs(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListNodeConfigs() error = %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("a failed create left %d pooled nodes behind", len(nodes))
	}
}

func TestDeleteChannelRemovesEverythingItProvisioned(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)

	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "sticky-delete",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		PoolSize:       2,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if err := harness.service.DeleteChannel(ctx, channel.ID, channel.Version); err != nil {
		t.Fatalf("DeleteChannel() error = %v", err)
	}
	if _, err := harness.service.GetChannel(ctx, channel.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannel after delete error = %v, want ErrNotFound", err)
	}
	nodes, err := harness.store.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil {
		t.Fatalf("ListResidentialSessionNodes() error = %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("delete left %d pooled nodes behind", len(nodes))
	}
	listeners, err := harness.store.ListListeners(ctx)
	if err != nil {
		t.Fatalf("ListListeners() error = %v", err)
	}
	if len(listeners) != 0 {
		t.Fatalf("delete left %d listeners behind", len(listeners))
	}
	groups, err := harness.store.ListProxyGroups(ctx)
	if err != nil {
		t.Fatalf("ListProxyGroups() error = %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("delete left %d proxy groups behind", len(groups))
	}
}

func TestEnsureClientSessionLazyDeclaredAllocation(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)
	// 懒分配 channel：preallocate=false（默认）+ SessionCount=2
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "lazy-declared",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		SessionCount:   2,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 创建后：无任何 session 已分配（懒分配）
	sessions, err := harness.store.ListResidentialClientSessions(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.NodeFingerprint != "" {
			t.Fatalf("lazy channel provisioned an allocation at create: %s", session.SessionID)
		}
	}
	// 首个客户端请求 s01 → 触发补分配
	record, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	allocated, err := harness.service.EnsureClientSessionByToken(ctx, record.RotateToken, "s01")
	if err != nil {
		t.Fatalf("EnsureClientSessionByToken() error = %v", err)
	}
	if allocated.AllocatedAt == nil {
		t.Fatal("first client request did not trigger the lazy allocation")
	}
	// 第二个客户端请求 s02 → 独立分配
	second, err := harness.service.EnsureClientSessionByToken(ctx, record.RotateToken, "s02")
	if err != nil {
		t.Fatalf("EnsureClientSessionByToken(s02) error = %v", err)
	}
	if second.AllocatedAt == nil {
		t.Fatal("second client request did not trigger its allocation")
	}
	if allocated.ProxyUsername == second.ProxyUsername {
		t.Fatal("lazy sessions share credentials")
	}
}
