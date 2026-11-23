package residential

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseAPINodesBestProxyJSON(t *testing.T) {
	t.Parallel()

	body := []byte(`{"code":200,"data":{"list":["1.2.3.4:8080","http://5.6.7.8:3128","bad","socks5://9.9.9.9:1080",""]}}`)
	nodes, err := parseAPINodes(body)
	if err != nil {
		t.Fatalf("parseAPINodes() error = %v", err)
	}
	want := []FetchedNode{
		{Server: "1.2.3.4", Port: 8080},
		{Server: "5.6.7.8", Port: 3128},
		{Server: "9.9.9.9", Port: 1080},
	}
	if len(nodes) != len(want) {
		t.Fatalf("parseAPINodes() = %+v, want %d nodes", nodes, len(want))
	}
	for index := range want {
		if nodes[index] != want[index] {
			t.Fatalf("node %d = %+v, want %+v", index, nodes[index], want[index])
		}
	}
}

func TestParseAPINodesPlainText(t *testing.T) {
	t.Parallel()

	nodes, err := parseAPINodes([]byte("1.2.3.4:8080\n5.6.7.8:3128\n"))
	if err != nil {
		t.Fatalf("parseAPINodes() error = %v", err)
	}
	if len(nodes) != 2 || nodes[0].Server != "1.2.3.4" || nodes[1].Port != 3128 {
		t.Fatalf("parseAPINodes() = %+v", nodes)
	}
}

func TestParseAPINodesAcceptsObjectAndAuthenticatedEntries(t *testing.T) {
	t.Parallel()

	body := []byte(`{"code":200,"data":{"list":[{"host":"1.2.3.4","port":8080,"username":"node-user","password":"node-pass"},{"ip":"5.6.7.8","port":"3128"}]}}`)
	nodes, err := parseAPINodes(body)
	if err != nil {
		t.Fatalf("parseAPINodes() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("parseAPINodes() = %+v, want two nodes", nodes)
	}
	if nodes[0].Username != "node-user" || nodes[0].Password != "node-pass" {
		t.Fatalf("authenticated node = %+v", nodes[0])
	}
	if nodes[1].Server != "5.6.7.8" || nodes[1].Port != 3128 {
		t.Fatalf("second node = %+v", nodes[1])
	}
}

func TestParseAPINodesRejectsBadPayloads(t *testing.T) {
	t.Parallel()

	cases := []string{"", "   ", "not a proxy", `{"code":500,"data":null}`, `{"code":200,"data":{"list":["junk"]}}`}
	for _, body := range cases {
		if _, err := parseAPINodes([]byte(body)); err == nil {
			t.Errorf("parseAPINodes(%q) = nil error, want error", body)
		}
	}
}

func TestSessionsFromNodesCapsAtPoolSize(t *testing.T) {
	t.Parallel()

	nodes := []FetchedNode{
		{Server: "1.1.1.1", Port: 1},
		{Server: "2.2.2.2", Port: 2},
		{Server: "3.3.3.3", Port: 3},
	}
	sessions := sessionsFromNodes(nodes, 2)
	if len(sessions) != 2 {
		t.Fatalf("sessionsFromNodes() = %d sessions, want 2", len(sessions))
	}
	if sessions[0].ID == sessions[1].ID || sessions[0].Server == sessions[1].Server {
		t.Fatalf("sessions must be distinct: %+v", sessions)
	}
	if sessions[0].Server != "1.1.1.1" || sessions[1].Port != 2 {
		t.Fatalf("sessionsFromNodes() = %+v", sessions)
	}
}

// An api-list provider has no gateway credentials and no username template; the
// channel pool is materialized from the extraction API and rotation moves
// between the fetched endpoints. Exhausting the pool re-fetches fresh nodes.
func TestAPILISTProviderChannelRotateAndRefresh(t *testing.T) {
	t.Parallel()

	var fetchCount atomic.Int32
	fetcher := func(_ context.Context, apiURL string) ([]FetchedNode, error) {
		if apiURL != "https://api.example.com/nodes?num=8" {
			return nil, errors.New("unexpected api_url")
		}
		fetchCount.Add(1)
		return []FetchedNode{
			{Server: "11.22.33.44", Port: 8000},
			{Server: "22.33.44.55", Port: 8000},
		}, nil
	}
	harness := newHarness(t, WithNodeFetcher(fetcher))
	ctx := context.Background()

	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name:              "bestproxy-api-main",
		Vendor:            "bestproxy-api",
		Protocol:          "http",
		APIURL:            "https://api.example.com/nodes?num=8",
		RotationMode:      RotationAPIList,
		SessionTTLSeconds: 60,
		PoolSize:          4,
		DefaultRegion:     "US",
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if provider.APIURL != "https://api.example.com/nodes?num=8" {
		t.Fatalf("APIURL = %q", provider.APIURL)
	}
	encodedProvider, err := json.Marshal(provider)
	if err != nil {
		t.Fatalf("marshal provider: %v", err)
	}
	if strings.Contains(string(encodedProvider), "api.example.com/nodes?num=8") {
		t.Fatalf("provider API response leaked the extraction URL: %s", encodedProvider)
	}
	if !provider.APIURLConfigured {
		t.Fatal("api-list provider must report a configured extraction URL")
	}
	if provider.CredentialsConfigured {
		t.Fatal("api-list provider must not report configured gateway credentials")
	}
	record, err := harness.store.GetResidentialProvider(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetResidentialProvider() error = %v", err)
	}
	if record.APIURL != "" {
		t.Fatalf("api-list URL was left in the compatibility plaintext column: %q", record.APIURL)
	}
	if strings.Contains(string(record.CredentialsEncrypted), "api.example.com") {
		t.Fatal("api-list URL was stored in cleartext")
	}
	if !provider.SupportsSticky {
		t.Fatal("api-list provider must support sticky channels")
	}

	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "api-list-1",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		PublicEndpoint: managedPublicEndpoint(),
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if channel.ActiveSessionCount != 0 {
		t.Fatalf("channel allocated %d sessions before a client request", channel.ActiveSessionCount)
	}
	if fetchCount.Load() != 0 {
		t.Fatalf("fetch calls = %d, want none at channel creation", fetchCount.Load())
	}
	channelRecord, err := harness.store.GetResidentialChannel(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	clientSession, err := harness.service.EnsureClientSessionByToken(ctx, channelRecord.RotateToken, "window-01")
	if err != nil {
		t.Fatal(err)
	}
	if fetchCount.Load() != 1 {
		t.Fatalf("fetch calls = %d, want one at first client allocation", fetchCount.Load())
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
	if canonical["server"] != "11.22.33.44" {
		t.Fatalf("allocated server = %v", canonical["server"])
	}
	if _, err := harness.service.RotateClientSessionByToken(ctx, channelRecord.RotateToken, clientSession.SessionID); err != nil {
		t.Fatal(err)
	}
	if fetchCount.Load() != 2 {
		t.Fatalf("fetch calls after client rotation = %d, want 2", fetchCount.Load())
	}
}

func TestValidateAPIURLRejectsUnsafeTargets(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"http://api.example.com/nodes",
		"https://127.0.0.1/nodes",
		"https://localhost/nodes",
		"https://192.168.1.1/nodes",
		"https://user:pass@api.example.com/nodes",
		"not a url",
	}
	for _, raw := range cases {
		if _, err := validateAPIURL(raw); err == nil {
			t.Errorf("validateAPIURL(%q) = nil error, want error", raw)
		}
	}
	got, err := validateAPIURL("https://api.example.com/nodes?num=8")
	if err != nil || !strings.Contains(got, "api.example.com") {
		t.Fatalf("validateAPIURL(valid) = %q, %v", got, err)
	}
}

func TestValidateAPIProxyURLSupportsControlPlaneProxyProtocols(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"http://127.0.0.1:7890",
		"https://proxy.example.com:8443",
		"socks5://user:pass@127.0.0.1:1080",
	} {
		if got, err := validateAPIProxyURL(raw); err != nil || got != raw {
			t.Errorf("validateAPIProxyURL(%q) = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"",
		"ftp://proxy.example.com:21",
		"http://proxy.example.com",
		"http://proxy.example.com:0",
		"http://proxy.example.com:65536",
		"http://proxy.example.com:8080/path",
		"http://proxy.example.com:8080?secret=1",
	} {
		if raw == "" {
			continue
		}
		if _, err := validateAPIProxyURL(raw); err == nil {
			t.Errorf("validateAPIProxyURL(%q) = nil error, want error", raw)
		}
	}
}

func TestProviderAPIProxyIsEncryptedWriteOnlyAndPreservedOnUpdate(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	ctx := context.Background()
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name:              "api-proxy-provider",
		Vendor:            "bestproxy",
		Protocol:          "http",
		GatewayHost:       "proxy.example.com",
		GatewayPort:       2312,
		APIProxyURL:       "http://user:secret@127.0.0.1:7890",
		Credentials:       &Credentials{Username: "account", Password: "gateway-secret"},
		UsernameTemplate:  "{user}-session-{session}",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 600,
		PoolSize:          1,
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if !provider.APIProxyConfigured {
		t.Fatal("provider should report a configured API proxy")
	}
	encoded, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "7890") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("provider JSON leaked API proxy data: %s", encoded)
	}
	updated, err := harness.service.UpdateProvider(ctx, provider.ID, UpdateProviderRequest{
		Version:           provider.Version,
		Name:              provider.Name,
		Vendor:            provider.Vendor,
		Protocol:          provider.Protocol,
		GatewayHost:       provider.GatewayHost,
		GatewayPort:       provider.GatewayPort,
		UsernameTemplate:  provider.UsernameTemplate,
		RotationMode:      provider.RotationMode,
		SessionTTLSeconds: provider.SessionTTLSeconds,
		PoolSize:          provider.PoolSize,
		Enabled:           true,
	})
	if err != nil {
		t.Fatalf("UpdateProvider() error = %v", err)
	}
	if !updated.APIProxyConfigured {
		t.Fatal("blank update should preserve the configured API proxy")
	}
}

func TestAPIListUpdateKeepsWriteOnlyURLAndRejectsCredentialModeWithoutCredentials(t *testing.T) {
	t.Parallel()

	harness := newHarness(t)
	ctx := context.Background()
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name:              "api-list-update",
		Vendor:            "bestproxy-api",
		Protocol:          "http",
		APIURL:            "https://api.example.com/nodes?app_key=secret",
		RotationMode:      RotationAPIList,
		SessionTTLSeconds: 60,
		PoolSize:          1,
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	updated, err := harness.service.UpdateProvider(ctx, provider.ID, UpdateProviderRequest{
		Version:           provider.Version,
		Name:              provider.Name,
		Vendor:            provider.Vendor,
		Protocol:          provider.Protocol,
		GatewayHost:       provider.GatewayHost,
		GatewayPort:       provider.GatewayPort,
		RotationMode:      RotationAPIList,
		SessionTTLSeconds: provider.SessionTTLSeconds,
		PoolSize:          provider.PoolSize,
		Enabled:           true,
	})
	if err != nil {
		t.Fatalf("UpdateProvider() error = %v", err)
	}
	if updated.APIURL != "https://api.example.com/nodes?app_key=secret" {
		t.Fatalf("updated APIURL = %q, want preserved secret", updated.APIURL)
	}

	_, err = harness.service.UpdateProvider(ctx, provider.ID, UpdateProviderRequest{
		Version:          updated.Version,
		Name:             updated.Name,
		Vendor:           "bestproxy",
		Protocol:         "http",
		GatewayHost:      "proxy.bestproxy.com",
		GatewayPort:      2312,
		UsernameTemplate: "{user}_session-{session}",
		RotationMode:     RotationSessionTemplate,
		PoolSize:         1,
		Enabled:          true,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("switch to credential mode error = %v, want ErrInvalid", err)
	}
}
