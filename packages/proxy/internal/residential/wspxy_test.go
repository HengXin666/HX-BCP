package residential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

type fakeWsPxyControl struct {
	mu        sync.Mutex
	nextID    int
	sessions  map[string]fakeWsPxySession
	creates   []string
	rotates   []string
	destroys  []string
	healthErr error
	createErr error
	rotateErr error
}

type fakeWsPxySession struct {
	ID     string
	Server string
	Port   int
	Pin    string
	ln     net.Listener
}

func newFakeWsPxyControl() *fakeWsPxyControl {
	return &fakeWsPxyControl{
		sessions: make(map[string]fakeWsPxySession),
	}
}

func (fake *fakeWsPxyControl) Create(_ context.Context, controlURL string) (WsPxySession, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.creates = append(fake.creates, controlURL)
	if fake.createErr != nil {
		return WsPxySession{}, fake.createErr
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return WsPxySession{}, err
	}
	fake.nextID++
	id := fmt.Sprintf("sess-%d", fake.nextID)
	port := listener.Addr().(*net.TCPAddr).Port
	session := fakeWsPxySession{ID: id, Server: "127.0.0.1", Port: port, Pin: "104.16.0.1", ln: listener}
	fake.sessions[id] = session
	return WsPxySession{ID: id, Server: session.Server, Port: session.Port, Pin: session.Pin}, nil
}

func (fake *fakeWsPxyControl) Rotate(_ context.Context, _ string, id string) (WsPxySession, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.rotates = append(fake.rotates, id)
	if fake.rotateErr != nil {
		return WsPxySession{}, fake.rotateErr
	}
	session, exists := fake.sessions[id]
	if !exists {
		return WsPxySession{}, errors.New("no such session")
	}
	session.Pin = "188.114.96.1"
	fake.sessions[id] = session
	return WsPxySession{ID: session.ID, Server: session.Server, Port: session.Port, Pin: session.Pin}, nil
}

func (fake *fakeWsPxyControl) Destroy(_ context.Context, _ string, id string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.destroys = append(fake.destroys, id)
	if session, exists := fake.sessions[id]; exists && session.ln != nil {
		_ = session.ln.Close()
	}
	delete(fake.sessions, id)
	return nil
}

func (fake *fakeWsPxyControl) Health(_ context.Context, _ string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.healthErr
}

func (fake *fakeWsPxyControl) snapshot() (creates, rotates, destroys int, live int) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return len(fake.creates), len(fake.rotates), len(fake.destroys), len(fake.sessions)
}

func TestValidateWsPxyControlURL(t *testing.T) {
	t.Parallel()

	got, err := validateWsPxyControlURL("http://127.0.0.1:2470")
	if err != nil || got != "http://127.0.0.1:2470" {
		t.Fatalf("validateWsPxyControlURL(loopback) = %q, %v", got, err)
	}
	if _, err := validateWsPxyControlURL("http://localhost:2470"); err != nil {
		t.Fatalf("localhost rejected: %v", err)
	}
	for _, raw := range []string{
		"",
		"https://panel.example.com:443",
		"http://10.0.0.1:2470",
		"http://127.0.0.1",
		"http://127.0.0.1:2470/session",
		"http://127.0.0.1:2470?x=1",
		"socks5://127.0.0.1:2470",
		"http://user:pass@127.0.0.1:2470",
	} {
		if _, err := validateWsPxyControlURL(raw); err == nil {
			t.Errorf("validateWsPxyControlURL(%q) = nil error, want error", raw)
		}
	}
}

func TestCreateHXCFWsPxyProvider(t *testing.T) {
	t.Parallel()
	control := newFakeWsPxyControl()
	harness := newHarness(t, WithWsPxyControl(control))
	provider, err := harness.service.CreateProvider(context.Background(), CreateProviderRequest{
		Name:         "wspxy local",
		Vendor:       "hx-cf-wspxy",
		Protocol:     "http",
		APIURL:       "http://127.0.0.1:2470",
		RotationMode: RotationHXCFWsPxy,
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if !provider.APIURLConfigured {
		t.Fatal("api_url_configured = false")
	}
	if provider.CredentialsConfigured {
		t.Fatal("hx-cf-wspxy must not advertise gateway credentials")
	}
	if provider.SessionTTLSeconds != 0 {
		t.Fatalf("TTL = %d, want 0", provider.SessionTTLSeconds)
	}
	if !provider.SupportsSticky {
		t.Fatal("hx-cf-wspxy must support sticky channels")
	}
	if provider.GatewayHost != wsPxyGatewayPlaceholder {
		t.Fatalf("gateway host = %q", provider.GatewayHost)
	}
	encoded, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "127.0.0.1:2470") {
		t.Fatalf("provider JSON leaked control URL: %s", encoded)
	}
}

func TestCreateHXCFWsPxyProviderRejectsInvalid(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	base := CreateProviderRequest{
		Name:         "wspxy local",
		Vendor:       "hx-cf-wspxy",
		Protocol:     "http",
		APIURL:       "http://127.0.0.1:2470",
		RotationMode: RotationHXCFWsPxy,
	}
	cases := map[string]func(*CreateProviderRequest){
		"missing url":  func(r *CreateProviderRequest) { r.APIURL = "" },
		"public host":  func(r *CreateProviderRequest) { r.APIURL = "http://203.0.113.10:2470" },
		"https public": func(r *CreateProviderRequest) { r.APIURL = "https://panel.example.com/c" },
		"socks5":       func(r *CreateProviderRequest) { r.Protocol = "socks5" },
		"vless":        func(r *CreateProviderRequest) { r.Protocol = "vless" },
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

func TestHXCFWsPxyClientSessionRotateAndRelease(t *testing.T) {
	t.Parallel()
	control := newFakeWsPxyControl()
	harness := newHarness(t, WithWsPxyControl(control), WithSessionRouter(&recordingSessionRouter{}))
	ctx := context.Background()

	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name:         "wspxy local",
		Vendor:       "hx-cf-wspxy",
		Protocol:     "http",
		APIURL:       "http://127.0.0.1:2470",
		RotationMode: RotationHXCFWsPxy,
		PoolSize:     4,
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "wspxy-1",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	creates, rotates, destroys, live := control.snapshot()
	if creates != 0 || live != 0 {
		t.Fatalf("channel create minted %d sessions, want lazy allocation", live)
	}

	channelRecord, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	clientSession, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, "window-01")
	if err != nil {
		t.Fatal(err)
	}
	creates, rotates, destroys, live = control.snapshot()
	if creates != 1 || live != 1 || rotates != 0 {
		t.Fatalf("after ensure: creates=%d rotates=%d live=%d", creates, rotates, live)
	}

	pool, err := harness.store.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil || len(pool) != 1 {
		t.Fatalf("allocated nodes = %d, error = %v", len(pool), err)
	}
	plaintext, err := harness.box.Open(pool[0].CanonicalConfigEncrypted, []byte("node:"+pool[0].Fingerprint))
	if err != nil {
		t.Fatal(err)
	}
	var canonical map[string]any
	if err := json.Unmarshal(plaintext, &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical["type"] != "http" || canonical["server"] != "127.0.0.1" {
		t.Fatalf("canonical = %#v", canonical)
	}
	port := intFromAny(canonical["port"])
	if port < 1 {
		t.Fatalf("port = %v", canonical["port"])
	}
	sessionID, _ := canonical[store.ResidentialWsPxySessionIDKey].(string)
	if sessionID == "" {
		t.Fatal("canonical missing hx_wspxy_session_id")
	}
	fingerprint := pool[0].Fingerprint

	if _, err := harness.service.RotateClientSessionByToken(ctx, channelRecord.RotateToken, clientSession.SessionID); err != nil {
		t.Fatal(err)
	}
	creates, rotates, destroys, live = control.snapshot()
	if creates != 1 || rotates != 1 || live != 1 || destroys != 0 {
		t.Fatalf("after rotate: creates=%d rotates=%d destroys=%d live=%d", creates, rotates, destroys, live)
	}
	pool, err = harness.store.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil || len(pool) != 1 {
		t.Fatalf("nodes after rotate = %d, error = %v", len(pool), err)
	}
	if pool[0].Fingerprint != fingerprint {
		t.Fatal("rotate minted a new node instead of keeping the CONNECT port")
	}

	if err := harness.service.DeleteClientSessionByToken(ctx, channelRecord.RotateToken, clientSession.SessionID); err != nil {
		t.Fatal(err)
	}
	creates, rotates, destroys, live = control.snapshot()
	if live != 0 || destroys != 1 {
		t.Fatalf("after delete: destroys=%d live=%d", destroys, live)
	}
}

func TestHXCFWsPxyTestProviderDestroysProbeSession(t *testing.T) {
	t.Parallel()
	control := newFakeWsPxyControl()
	harness := newHarness(t, WithWsPxyControl(control))
	ctx := context.Background()
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name:         "wspxy local",
		Vendor:       "hx-cf-wspxy",
		Protocol:     "http",
		APIURL:       "http://127.0.0.1:2470",
		RotationMode: RotationHXCFWsPxy,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := harness.service.TestProvider(ctx, provider.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("TestProvider() = %#v", result)
	}
	creates, _, destroys, live := control.snapshot()
	if creates != 1 || destroys != 1 || live != 0 {
		t.Fatalf("probe leak: creates=%d destroys=%d live=%d", creates, destroys, live)
	}
}

func TestHXCFWsPxyRotateFallsBackToNewSession(t *testing.T) {
	t.Parallel()
	control := newFakeWsPxyControl()
	harness := newHarness(t, WithWsPxyControl(control), WithSessionRouter(&recordingSessionRouter{}))
	ctx := context.Background()
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name:         "wspxy local",
		Vendor:       "hx-cf-wspxy",
		Protocol:     "http",
		APIURL:       "http://127.0.0.1:2470",
		RotationMode: RotationHXCFWsPxy,
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "wspxy-fallback",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatal(err)
	}
	channelRecord, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	clientSession, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, "window-01")
	if err != nil {
		t.Fatal(err)
	}
	control.mu.Lock()
	control.rotateErr = errors.New("relay down")
	control.mu.Unlock()
	if _, err := harness.service.RotateClientSessionByToken(ctx, channelRecord.RotateToken, clientSession.SessionID); err != nil {
		t.Fatal(err)
	}
	creates, rotates, destroys, live := control.snapshot()
	if creates != 2 || rotates != 1 || live != 1 || destroys != 1 {
		t.Fatalf("fallback: creates=%d rotates=%d destroys=%d live=%d", creates, rotates, destroys, live)
	}
}

func TestSessionsFromWsPxyViewRejectsNonLoopback(t *testing.T) {
	t.Parallel()
	if _, err := sessionFromWsPxyView(wsPxyView{ID: "a", Proxy: "http://203.0.113.8:24800"}); err == nil {
		t.Fatal("accepted a public CONNECT host")
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		n, _ := strconv.Atoi(fmt.Sprint(value))
		return n
	}
}

func TestWsPxyEndpointJoin(t *testing.T) {
	t.Parallel()
	got, err := wsPxyEndpoint("http://127.0.0.1:2470", "/session/abc/rotate")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:2470/session/abc/rotate" {
		t.Fatalf("endpoint = %q", got)
	}
}
