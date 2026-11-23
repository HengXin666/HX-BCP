package residential

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"
)

// bpbRawPayload encodes the BPB-Worker-Panel /sub/raw response shape: a
// base64 blob of VLESS and Trojan share URI lines.
func bpbRawPayload(t *testing.T, lines ...string) []byte {
	t.Helper()
	joined := strings.Join(lines, "\n") + "\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(joined))
	return []byte(encoded)
}

func TestParseWorkerConfigVlessAndTrojan(t *testing.T) {
	t.Parallel()
	body := bpbRawPayload(t,
		"vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@104.16.0.1:443?encryption=none&host=example.com&type=ws&security=tls&path=%2Fvl%2Fabc%3Fed%3D2560&sni=example.com&fp=chrome&alpn=http%2F1.1#BPB VLESS",
		"trojan://secret-pass@104.16.0.2:8443?host=example.com&type=ws&security=tls&path=%2Ftr%2Fxyz%3Fed%3D2560&sni=example.com#BPB Trojan",
	)
	nodes, err := parseWorkerConfig(body)
	if err != nil {
		t.Fatalf("parseWorkerConfig() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("parseWorkerConfig() = %d nodes, want 2", len(nodes))
	}
	if nodes[0].Protocol != "vless" || nodes[0].Server != "104.16.0.1" || nodes[0].Port != 443 {
		t.Fatalf("first node = %+v", nodes[0])
	}
	if nodes[1].Protocol != "trojan" || nodes[1].Server != "104.16.0.2" || nodes[1].Port != 8443 {
		t.Fatalf("second node = %+v", nodes[1])
	}
	if nodes[0].Canonical["server"] != "104.16.0.1" {
		t.Fatalf("canonical server not carried through: %v", nodes[0].Canonical["server"])
	}
}

func TestNormalizeWorkerServer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{name: "bare cf subdomain gets workers.dev", in: "bpb.x70hlrsl-a5a", want: "bpb.x70hlrsl-a5a.workers.dev"},
		{name: "bare hyphenated subdomain", in: "bpb.fh7t6o7n", want: "bpb.fh7t6o7n.workers.dev"},
		{name: "public domain untouched", in: "www.speedtest.net", want: "www.speedtest.net"},
		{name: "qualified workers.dev untouched", in: "bpb.x70hlrsl-a5a.workers.dev", want: "bpb.x70hlrsl-a5a.workers.dev"},
		{name: "ipv4 untouched", in: "104.16.0.1", want: "104.16.0.1"},
		{name: "ipv6 untouched", in: "2606:4700::1111", want: "2606:4700::1111"},
		{name: "empty untouched", in: "", want: ""},
		{name: "bare subdomain with port", in: "bpb.x70hlrsl-a5a:443", want: "bpb.x70hlrsl-a5a.workers.dev:443"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeWorkerServer(test.in); got != test.want {
				t.Fatalf("normalizeWorkerServer(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestParseWorkerConfigSyncsCanonicalServerSuffix(t *testing.T) {
	t.Parallel()
	body := bpbRawPayload(t,
		"vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@bpb.x70hlrsl-a5a:443?encryption=none&host=bpb.x70hlrsl-a5a&type=ws&security=tls&path=%2Fvl%2Fabc%3Fed%3D2560&sni=bpb.x70hlrsl-a5a&fp=chrome&alpn=http%2F1.1#BPB Bare",
	)
	nodes, err := parseWorkerConfig(body)
	if err != nil {
		t.Fatalf("parseWorkerConfig() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("parseWorkerConfig() = %d nodes, want 1", len(nodes))
	}
	want := "bpb.x70hlrsl-a5a.workers.dev"
	if nodes[0].Server != want {
		t.Fatalf("node.Server = %q, want %q", nodes[0].Server, want)
	}
	if got := nodes[0].Canonical["server"]; got != want {
		t.Fatalf("canonical server = %v, want %q", got, want)
	}
	query, ok := nodes[0].Canonical["query"].(map[string]any)
	if !ok {
		t.Fatalf("canonical query = %#v, want map", nodes[0].Canonical["query"])
	}
	if got := query["host"]; got != want {
		t.Fatalf("canonical query host = %v, want %q", got, want)
	}
	if got := query["sni"]; got != want {
		t.Fatalf("canonical query sni = %v, want %q", got, want)
	}
}

func TestParseWorkerConfigRejectsBadPayloads(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		{},
		[]byte("   "),
		[]byte("not base64 or URIs"),
		[]byte(base64.StdEncoding.EncodeToString([]byte("ss://c2VjcmV0@1.2.3.4:8388"))),
	}
	for _, body := range cases {
		if _, err := parseWorkerConfig(body); err == nil {
			t.Errorf("parseWorkerConfig(%q) = nil error, want error", string(body))
		}
	}
}

func TestWorkerSessionsFiltersProtocolCapsAndDedupes(t *testing.T) {
	t.Parallel()
	nodes := []FetchedWorkerNode{
		{Protocol: "vless", Server: "1.1.1.1", Port: 443, Canonical: map[string]any{"type": "vless", "server": "1.1.1.1", "port": 443}},
		{Protocol: "vless", Server: "1.1.1.1", Port: 443, Canonical: map[string]any{"type": "vless", "server": "1.1.1.1", "port": 443}}, // duplicate
		{Protocol: "trojan", Server: "2.2.2.2", Port: 8443, Canonical: map[string]any{"type": "trojan", "server": "2.2.2.2", "port": 8443}},
		{Protocol: "vless", Server: "3.3.3.3", Port: 443, Canonical: map[string]any{"type": "vless", "server": "3.3.3.3", "port": 443}},
	}
	sessions := workerSessions(nodes, "vless", 2)
	if len(sessions) != 2 {
		t.Fatalf("workerSessions() = %d sessions, want 2", len(sessions))
	}
	for _, session := range sessions {
		if protocol := canonicalString(session.Canonical["type"]); protocol != "vless" {
			t.Fatalf("session protocol = %q, want vless", protocol)
		}
		if len(session.Canonical) == 0 {
			t.Fatalf("session canonical not carried: %+v", session)
		}
	}
}

func TestFetchWorkerConfigRejectsLoopback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := fetchWorkerConfig(ctx, "https://127.0.0.1:8443/sub/raw?app=xray", ""); err == nil {
		t.Fatal("fetchWorkerConfig() accepted a loopback endpoint")
	}
}

func TestCloudflareWorkerProviderTestConnection(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	harness := newHarness(t)
	session := Session{Server: "127.0.0.1", Port: port}
	result, err := harness.service.testCloudflareWorkerProvider(context.Background(), Provider{}, session)
	if err != nil {
		t.Fatalf("testCloudflareWorkerProvider() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("testCloudflareWorkerProvider() = %+v, want success", result)
	}
	if result.Detail == "" {
		t.Fatal("testCloudflareWorkerProvider() Detail is empty")
	}

	bad := Session{}
	badResult, err := harness.service.testCloudflareWorkerProvider(context.Background(), Provider{}, bad)
	if err != nil {
		t.Fatalf("testCloudflareWorkerProvider(bad) error = %v", err)
	}
	if badResult.Success {
		t.Fatal("testCloudflareWorkerProvider(bad) reported success")
	}
}

func TestCreateCloudflareWorkerProvider(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	provider, err := harness.service.CreateProvider(context.Background(), CreateProviderRequest{
		Name:         "bpb panel",
		Vendor:       "bpb-panel",
		Protocol:     "vless",
		WorkerURL:    "https://panel.example.com/securePath/sub/raw?app=xray",
		RotationMode: RotationCloudflareWorker,
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if !provider.WorkerURLConfigured {
		t.Fatal("provider.WorkerURLConfigured = false, want true")
	}
	if provider.SessionTTLSeconds != 0 {
		t.Fatalf("provider TTL = %d, want 0", provider.SessionTTLSeconds)
	}
	if provider.SupportsSticky != true {
		t.Fatal("cf-worker provider must support sticky channels")
	}
	if provider.CredentialsConfigured {
		t.Fatal("cf-worker provider must not advertise gateway credentials")
	}
	if provider.GatewayHost != cfWorkerGatewayPlaceholder {
		t.Fatalf("gateway host = %q, want %q", provider.GatewayHost, cfWorkerGatewayPlaceholder)
	}
}

func TestCreateCloudflareWorkerProviderRejectsInvalid(t *testing.T) {
	t.Parallel()
	harness := newHarness(t)
	base := CreateProviderRequest{
		Name:         "bpb panel",
		Vendor:       "bpb-panel",
		Protocol:     "vless",
		WorkerURL:    "https://panel.example.com/securePath/sub/raw?app=xray",
		RotationMode: RotationCloudflareWorker,
	}
	cases := map[string]func(*CreateProviderRequest){
		"missing worker_url": func(r *CreateProviderRequest) { r.WorkerURL = "" },
		"not https":          func(r *CreateProviderRequest) { r.WorkerURL = "http://panel.example.com/sub/raw" },
		"private host":       func(r *CreateProviderRequest) { r.WorkerURL = "https://10.0.0.1/sub/raw" },
		"bad protocol":       func(r *CreateProviderRequest) { r.Protocol = "http" },
		"embedded creds":     func(r *CreateProviderRequest) { r.WorkerURL = "https://user:pass@panel.example.com/sub/raw" },
		"fragment":           func(r *CreateProviderRequest) { r.WorkerURL = "https://panel.example.com/sub/raw#frag" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			if _, err := harness.service.CreateProvider(context.Background(), request); err == nil {
				t.Errorf("CreateProvider(%s) = nil error, want error", name)
			}
		})
	}
}

func TestProviderSessionsCloudflareWorker(t *testing.T) {
	t.Parallel()
	harness := newHarness(t, WithWorkerFetcher(func(_ context.Context, workerURL, _ string) ([]FetchedWorkerNode, error) {
		if !strings.Contains(workerURL, "panel.example.com") {
			t.Errorf("workerURL = %q, want panel.example.com", workerURL)
		}
		return []FetchedWorkerNode{
			{Protocol: "vless", Server: "104.16.0.1", Port: 443, Canonical: map[string]any{"server": "104.16.0.1", "port": 443}},
			{Protocol: "trojan", Server: "104.16.0.2", Port: 8443, Canonical: map[string]any{"server": "104.16.0.2", "port": 8443}},
		}, nil
	}))
	provider := Provider{
		Name:         "bpb panel",
		Protocol:     "vless",
		WorkerURL:    "https://panel.example.com/securePath/sub/raw?app=xray",
		RotationMode: RotationCloudflareWorker,
	}
	sessions, err := harness.service.providerSessions(
		context.Background(),
		provider,
		Credentials{},
		RegionSelection{Mode: RegionModeFixed},
		1,
	)
	if err != nil {
		t.Fatalf("providerSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("providerSessions() = %d sessions, want 1", len(sessions))
	}
	if sessions[0].Server != "104.16.0.1" || sessions[0].Port != 443 {
		t.Fatalf("session = %+v, want vless 104.16.0.1:443", sessions[0])
	}
	if len(sessions[0].Canonical) == 0 {
		t.Fatal("session canonical is empty")
	}
}

func TestCreateStickyChannelCloudflareWorkerFetchFailure(t *testing.T) {
	t.Parallel()
	harness := newHarness(t, WithWorkerFetcher(func(_ context.Context, _ string, _ string) ([]FetchedWorkerNode, error) {
		return nil, errors.New("lookup bpb.example.com: no such host")
	}))
	provider, err := harness.service.CreateProvider(context.Background(), CreateProviderRequest{
		Name:         "bpb panel",
		Vendor:       "bpb-panel",
		Protocol:     "vless",
		WorkerURL:    "https://bpb.example.com/securePath/sub/raw?app=xray",
		RotationMode: RotationCloudflareWorker,
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	_, err = harness.service.CreateChannel(context.Background(), CreateChannelRequest{
		Name:           "bpb sticky",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		Protocol:       "vless",
		SessionCount:   2,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if !errors.Is(err, ErrProviderUnreachable) {
		t.Fatalf("CreateChannel() error = %v, want ErrProviderUnreachable", err)
	}
	channels, listErr := harness.service.ListChannels(context.Background())
	if listErr != nil {
		t.Fatalf("ListChannels() error = %v", listErr)
	}
	if len(channels) != 0 {
		t.Fatalf("CreateChannel() left %d orphaned channels after fetch failure: %+v", len(channels), channels)
	}
}
