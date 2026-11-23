package residential

import (
	"context"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

func TestDeclaredSessionsExportAndControlUseStableCredentials(t *testing.T) {
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "residential-us",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		SessionCount:   2,
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(channel.Endpoint.SharePath, "/sub/")
	bundle, matched, err := harness.service.ExportByShareToken(ctx, token, "proxy.example.com")
	if err != nil || !matched {
		t.Fatalf("ExportByShareToken() = (%+v, %t, %v)", bundle, matched, err)
	}
	if bundle.NodeCount() != 2 || len(bundle.Exports) != 1 {
		t.Fatalf("bundle counts = nodes:%d exports:%d", bundle.NodeCount(), len(bundle.Exports))
	}
	if strings.Contains(bundle.Exports[0].Body, "acct123") ||
		strings.Contains(bundle.Exports[0].Body, "s3cret") ||
		strings.Contains(bundle.Exports[0].Body, "gate.bestproxy.com") {
		t.Fatal("ordinary subscription leaked the residential vendor endpoint")
	}
	if bundle.Exports[0].Nodes[0].Name != "residential-us-01" ||
		bundle.Exports[0].Nodes[1].Name != "residential-us-02" {
		t.Fatalf("exported node names = %+v", bundle.Exports)
	}
	firstPassword := bundle.Exports[0].Nodes[0].Auth.Password
	secondPassword := bundle.Exports[0].Nodes[1].Auth.Password
	if firstPassword == "" || firstPassword == secondPassword {
		t.Fatal("declared sessions did not receive independent credentials")
	}

	controlToken := strings.TrimPrefix(channel.ControlPath, "/ctl/")
	control, err := harness.service.ControlNodesByToken(ctx, controlToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(control.Nodes) != 2 || control.Nodes[0].ProxyURL != nil {
		t.Fatalf("control node count = %d, proxy URL present = %t", len(control.Nodes), len(control.Nodes) > 0 && control.Nodes[0].ProxyURL != nil)
	}
	if got := control.Nodes[0].Endpoints; len(got) != 1 ||
		got[0].Protocol != "vless" || got[0].Transport != "ws" || got[0].BrowserCompatible {
		t.Fatalf("control endpoints = %+v", got)
	}
	if !strings.Contains(control.Nodes[0].Endpoints[0].URI, firstPassword) {
		t.Fatalf("control endpoint does not use stable declared credential")
	}
	if control.Nodes[0].ResidentialEndpoint != nil {
		t.Fatal("session-template provider exposed its vendor gateway credential")
	}

	before, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 1, RotateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after, err := harness.store.GetResidentialClientSession(ctx, channel.ID, "s01")
	if err != nil {
		t.Fatal(err)
	}
	if before.NodeFingerprint == after.NodeFingerprint || rotated.ProxyURL != nil ||
		len(rotated.Endpoints) != 1 || rotated.Endpoints[0].URI != control.Nodes[0].Endpoints[0].URI {
		t.Fatal("rotation changed client identity or kept the old allocation")
	}
	if rotated.ResidentialEndpoint != nil {
		t.Fatal("session-template rotation exposed its vendor gateway credential")
	}
}

func TestDeclaredControlOmitsUnallocatedResidentialEndpointUntilNext(t *testing.T) {
	fetchCount := 0
	harness := newHarness(t, WithNodeFetcher(func(context.Context, string) ([]FetchedNode, error) {
		fetchCount++
		return []FetchedNode{{
			Server:   "11.22.33.44",
			Port:     8000 + fetchCount,
			Username: "node-user",
			Password: "node-secret",
		}}, nil
	}))
	ctx := context.Background()
	provider, err := harness.service.CreateProvider(ctx, CreateProviderRequest{
		Name:                  "api-list-provider",
		Vendor:                "bestproxy-api",
		Protocol:              "http",
		APIURL:                "https://api.example.com/nodes",
		RotationMode:          RotationAPIList,
		SessionTTLSeconds:     60,
		MaxConcurrentSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "residential-lazy",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		SessionCount:   1,
		IdleRelease:    60,
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	controlToken := strings.TrimPrefix(channel.ControlPath, "/ctl/")
	control, err := harness.service.ControlNodesByToken(ctx, controlToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(control.Nodes) != 1 || control.Nodes[0].ResidentialEndpoint != nil {
		t.Fatal("unallocated declared node exposed a residential endpoint")
	}
	rotated, err := harness.service.RotateDeclaredSessionByControlToken(ctx, controlToken, 1, RotateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ResidentialEndpoint == nil {
		t.Fatal("next did not return the newly allocated residential endpoint")
	}
	endpoint := rotated.ResidentialEndpoint
	if endpoint.Protocol != "http" || endpoint.Server != "11.22.33.44" ||
		endpoint.Port != 8001 || endpoint.Username == "" || endpoint.Password == "" || endpoint.TLS {
		t.Fatal("next returned an invalid residential endpoint")
	}
	shareToken := strings.TrimPrefix(channel.Endpoint.SharePath, "/sub/")
	bundle, matched, err := harness.service.ExportByShareToken(ctx, shareToken, "proxy.example.com")
	if err != nil || !matched || len(bundle.Exports) != 1 {
		t.Fatalf("ordinary subscription export failed: matched=%t exports=%d err=%v", matched, len(bundle.Exports), err)
	}
	if strings.Contains(bundle.Exports[0].Body, endpoint.Server) ||
		strings.Contains(bundle.Exports[0].Body, endpoint.Username) ||
		strings.Contains(bundle.Exports[0].Body, endpoint.Password) {
		t.Fatal("ordinary subscription leaked the extracted residential endpoint")
	}
}

func TestDeclaredControlExposesManagedWebSocketEndpoint(t *testing.T) {
	harness := newHarness(t)
	ctx := context.Background()
	provider := harness.createProvider(t)
	channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
		Name:           "residential-ws",
		ProviderID:     provider.ID,
		Mode:           ModeSticky,
		SessionCount:   1,
		PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	controlToken := strings.TrimPrefix(channel.ControlPath, "/ctl/")
	control, err := harness.service.ControlNodesByToken(ctx, controlToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(control.Nodes) != 1 {
		t.Fatalf("control node count = %d", len(control.Nodes))
	}
	node := control.Nodes[0]
	if node.ProxyURL != nil || len(node.Endpoints) != 1 {
		t.Fatalf("control node proxy URL present = %t, endpoint count = %d", node.ProxyURL != nil, len(node.Endpoints))
	}
	endpoint := node.Endpoints[0]
	if endpoint.Protocol != "vless" || endpoint.Transport != "ws" || endpoint.BrowserCompatible ||
		!strings.HasPrefix(endpoint.URI, "vless://") || !strings.Contains(endpoint.URI, "proxy.example.com:443") {
		t.Fatalf("control endpoint = %+v", endpoint)
	}
}

func TestDeclaredChannelsSupportCloudflareWebSocketProtocols(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		t.Run(protocol, func(t *testing.T) {
			harness := newHarness(t)
			ctx := context.Background()
			provider := harness.createProvider(t)
			channel, err := harness.service.CreateChannel(ctx, CreateChannelRequest{
				Name:           "residential-" + protocol,
				ProviderID:     provider.ID,
				Mode:           ModeSticky,
				Protocol:       protocol,
				SessionCount:   1,
				PublicEndpoint: listener.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			if channel.Endpoint.Kind != protocol || channel.Endpoint.Transport.Type != "ws" {
				t.Fatalf("endpoint = %+v", channel.Endpoint)
			}
			if !strings.HasPrefix(channel.ControlURL, "https://proxy.example.com/ctl/") {
				t.Fatalf("control URL = %q", channel.ControlURL)
			}

			shareToken := strings.TrimPrefix(channel.Endpoint.SharePath, "/sub/")
			bundle, matched, err := harness.service.ExportByShareToken(ctx, shareToken, "proxy.example.com")
			if err != nil || !matched || bundle.NodeCount() != 1 {
				t.Fatalf("ExportByShareToken() = (%+v, %t, %v)", bundle, matched, err)
			}
			if len(bundle.Exports) != 1 || bundle.Exports[0].Kind != protocol {
				t.Fatalf("exports = %+v", bundle.Exports)
			}

			controlToken := strings.TrimPrefix(channel.ControlPath, "/ctl/")
			control, err := harness.service.ControlNodesByToken(ctx, controlToken)
			if err != nil {
				t.Fatal(err)
			}
			if len(control.Nodes) != 1 || len(control.Nodes[0].Endpoints) != 1 {
				t.Fatalf("control node count = %d", len(control.Nodes))
			}
			endpoint := control.Nodes[0].Endpoints[0]
			if endpoint.Protocol != protocol || endpoint.Transport != "ws" || endpoint.BrowserCompatible {
				t.Fatalf("control endpoint = %+v", endpoint)
			}
		})
	}
}
