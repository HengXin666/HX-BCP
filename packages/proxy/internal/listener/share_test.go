package listener

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type shareTestReconciler struct{}

func (shareTestReconciler) Apply(context.Context) error { return nil }

func newShareTestService(t *testing.T) (*Service, *store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(database, box, shareTestReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	return service, database, ctx
}

func TestAdvancedShareExportSupportsThreeClientFormats(t *testing.T) {
	service, database, ctx := newShareTestService(t)
	groupID := createShareTestGroup(t, ctx, database)
	created, err := service.Create(ctx, CreateRequest{
		Name: "CF VLESS", Kind: "vless", BindAddress: "127.0.0.1", Port: 18088, ProxyGroupID: groupID,
		Auth:           &Auth{Username: "hx-user", Password: "11111111-1111-1111-1111-111111111111"},
		Transport:      Transport{Type: "ws", WSPath: "/edge"},
		PublicEndpoint: PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	export, err := service.ExportByShareToken(ctx, strings.TrimPrefix(created.SharePath, "/sub/"), "127.0.0.1:19090")
	if err != nil {
		t.Fatal(err)
	}
	if created.Transport.WSPath != WebSocketPathPrefix+"edge" {
		t.Fatalf("listener path = %q, want %q", created.Transport.WSPath, WebSocketPathPrefix+"edge")
	}
	if !strings.Contains(export.Body, "proxy.example.com:443") || !strings.Contains(export.Body, "path=%2F__hx-proxy__%2Fedge") {
		t.Fatalf("unexpected URI export: %s", export.Body)
	}
	clashBody, clashName, _, err := export.Render("clash")
	if err != nil || !strings.HasSuffix(clashName, ".yaml") {
		t.Fatalf("Render(clash) = %q, %q, %v", clashBody, clashName, err)
	}
	var clash map[string]any
	if err := yaml.Unmarshal([]byte(clashBody), &clash); err != nil || clash["proxies"] == nil {
		t.Fatalf("invalid Clash output: %v, %s", err, clashBody)
	}
	if clash["proxy-groups"] == nil || clash["rules"] == nil {
		t.Fatalf("Clash output is not a complete profile: %s", clashBody)
	}
	if binary, lookupErr := exec.LookPath("mihomo"); lookupErr == nil {
		directory := t.TempDir()
		configPath := filepath.Join(directory, "clash.yaml")
		if err := os.WriteFile(configPath, []byte(clashBody), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, "-t", "-d", directory, "-f", configPath).CombinedOutput(); err != nil {
			t.Fatalf("mihomo rejected Clash output: %v\n%s\n%s", err, output, clashBody)
		}
	}
	if parsed, err := nodeparse.Parse([]byte(clashBody)); err != nil || len(parsed.Nodes) != 1 {
		t.Fatalf("Clash output cannot be imported: %v, %+v", err, parsed)
	}
	singBoxBody, singBoxName, _, err := export.Render("sing-box")
	if err != nil || !strings.HasSuffix(singBoxName, ".json") {
		t.Fatalf("Render(sing-box) = %q, %q, %v", singBoxBody, singBoxName, err)
	}
	var singBox map[string]any
	if err := json.Unmarshal([]byte(singBoxBody), &singBox); err != nil || singBox["outbounds"] == nil {
		t.Fatalf("invalid sing-box output: %v, %s", err, singBoxBody)
	}
	if parsed, err := nodeparse.Parse([]byte(singBoxBody)); err != nil || len(parsed.Nodes) != 1 {
		t.Fatalf("sing-box output cannot be imported: %v, %+v", err, parsed)
	}
	encoded, _, _, err := export.Render("v2rayn")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !strings.HasPrefix(string(decoded), "vless://") {
		t.Fatalf("invalid v2rayN output: %v, %q", err, decoded)
	}
	if parsed, err := nodeparse.Parse(decoded); err != nil || len(parsed.Nodes) != 1 {
		t.Fatalf("v2rayN output cannot be imported: %v, %+v", err, parsed)
	}
}

func createShareTestGroup(t *testing.T, ctx context.Context, database *store.Store) string {
	t.Helper()
	record := store.ProxyGroupRecord{
		ID:               "group-share-test",
		Name:             "share-test",
		Strategy:         "manual",
		SourceSpecJSON:   `{"node_ids":[],"include_direct":true}`,
		RulePipelineJSON: "{}",
		Enabled:          true,
		EmptyBehavior:    "direct",
		Version:          1,
	}
	if _, err := database.CreateProxyGroup(ctx, record); err != nil {
		t.Fatal(err)
	}
	return record.ID
}

func TestShareExportRendersAuthenticatedURIs(t *testing.T) {
	service, database, ctx := newShareTestService(t)
	groupID := createShareTestGroup(t, ctx, database)

	created, err := service.Create(ctx, CreateRequest{
		Name:         "边缘出口",
		Kind:         "mixed",
		BindAddress:  "192.168.1.20",
		Port:         7890,
		ProxyGroupID: groupID,
		Auth:         &Auth{Username: "user", Password: "pa:ss@word"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.SharePath == "" || !strings.HasPrefix(created.SharePath, "/sub/") {
		t.Fatalf("share path missing: %+v", created)
	}
	token := strings.TrimPrefix(created.SharePath, "/sub/")

	export, err := service.ExportByShareToken(ctx, token, "192.168.1.20:19090")
	if err != nil {
		t.Fatalf("ExportByShareToken() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(export.Body), "\n")
	if len(lines) != 2 {
		t.Fatalf("mixed listener should export two URIs, got %v", lines)
	}
	// A non-loopback listener can use its bind address, and credentials must be
	// URL-escaped.
	if !strings.Contains(lines[0], "192.168.1.20:7890") {
		t.Fatalf("unexpected host in %q", lines[0])
	}
	if !strings.Contains(lines[0], "user:pa%3Ass%40word@") {
		t.Fatalf("credentials not escaped in %q", lines[0])
	}
	if !strings.HasPrefix(lines[0], "http://") || !strings.HasPrefix(lines[1], "socks5://") {
		t.Fatalf("unexpected schemes: %v", lines)
	}

	decoded, err := base64.StdEncoding.DecodeString(export.EncodeSubscription())
	if err != nil || string(decoded) != export.Body {
		t.Fatalf("EncodeSubscription() roundtrip failed: %v", err)
	}

	if _, err := service.ExportByShareToken(ctx, "0000000000000000", "host"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token error = %v, want ErrNotFound", err)
	}
}

func TestShareExportDoesNotExposeLoopbackPortThroughExternalHost(t *testing.T) {
	service, database, ctx := newShareTestService(t)
	groupID := createShareTestGroup(t, ctx, database)
	created, err := service.Create(ctx, CreateRequest{
		Name: "internal-only", Kind: "http", BindAddress: "127.0.0.1", Port: 32825, ProxyGroupID: groupID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExportByShareToken(ctx, strings.TrimPrefix(created.SharePath, "/sub/"), "proxy.example.com")
	if !errors.Is(err, ErrShareDisabled) {
		t.Fatalf("external loopback export error = %v, want ErrShareDisabled", err)
	}
}

func TestShareExportServesLoopbackListenerToLoopbackCaller(t *testing.T) {
	service, database, ctx := newShareTestService(t)
	groupID := createShareTestGroup(t, ctx, database)
	created, err := service.Create(ctx, CreateRequest{
		Name: "local-only", Kind: "http", BindAddress: "127.0.0.1", Port: 32825, ProxyGroupID: groupID,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(created.SharePath, "/sub/")
	// A local CLI or local service reaches the control plane on loopback, so the
	// listener it just created is reachable by that same machine.
	cases := map[string]string{
		"127.0.0.1:19090": "127.0.0.1",
		"localhost:19090": "localhost",
		"[::1]:19090":     "::1",
	}
	for requestHost, wantHost := range cases {
		export, err := service.ExportByShareToken(ctx, token, requestHost)
		if err != nil {
			t.Fatalf("loopback export for %q error = %v, want success", requestHost, err)
		}
		// The advertised host is whichever local name the caller used; both are
		// reachable by the machine requesting them. The port stays the listener's.
		if export.Host != wantHost || export.Port != 32825 {
			t.Fatalf("loopback export for %q = %s:%d, want %s:32825", requestHost, export.Host, export.Port, wantHost)
		}
		if !strings.Contains(export.Body, ":32825") {
			t.Fatalf("loopback export body for %q = %q", requestHost, export.Body)
		}
	}
	// An empty request host cannot prove the caller is local, so it stays refused.
	if _, err := service.ExportByShareToken(ctx, token, ""); !errors.Is(err, ErrShareDisabled) {
		t.Fatalf("empty request host error = %v, want ErrShareDisabled", err)
	}
}

func TestShareExportUsesConfiguredPublicEndpointAndRejectsLoopbackFallback(t *testing.T) {
	service, database, ctx := newShareTestService(t)
	groupID := createShareTestGroup(t, ctx, database)

	created, err := service.Create(ctx, CreateRequest{
		Name: "public-http", Kind: "http", BindAddress: "127.0.0.1", Port: 7892, ProxyGroupID: groupID,
		PublicEndpoint: PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	export, err := service.ExportByShareToken(ctx, strings.TrimPrefix(created.SharePath, "/sub/"), "127.0.0.1:19090")
	if err != nil {
		t.Fatal(err)
	}
	if export.Host != "proxy.example.com" || export.Port != 443 || !strings.HasPrefix(export.Body, "https://proxy.example.com:443") {
		t.Fatalf("configured endpoint was not used: %+v, body=%q", export, export.Body)
	}

}

func TestShareExportRejectsDisabledListenerAndRotates(t *testing.T) {
	service, database, ctx := newShareTestService(t)
	groupID := createShareTestGroup(t, ctx, database)

	created, err := service.Create(ctx, CreateRequest{
		Name:         "rotate-me",
		Kind:         "http",
		BindAddress:  "127.0.0.1",
		Port:         7891,
		ProxyGroupID: groupID,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldToken := strings.TrimPrefix(created.SharePath, "/sub/")

	rotated, err := service.RotateShareToken(ctx, created.ID)
	if err != nil {
		t.Fatalf("RotateShareToken() error = %v", err)
	}
	newToken := strings.TrimPrefix(rotated.SharePath, "/sub/")
	if newToken == oldToken || len(newToken) != 32 {
		t.Fatalf("token not rotated: old=%s new=%s", oldToken, newToken)
	}
	if _, err := service.ExportByShareToken(ctx, oldToken, "host"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token error = %v, want ErrNotFound", err)
	}

	updated, err := service.Update(ctx, created.ID, UpdateRequest{
		Version:      rotated.Version,
		Name:         created.Name,
		Kind:         created.Kind,
		BindAddress:  created.BindAddress,
		Port:         created.Port,
		ProxyGroupID: groupID,
		Enabled:      false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatal("listener should be disabled")
	}
	if _, err := service.ExportByShareToken(ctx, newToken, "host"); !errors.Is(err, ErrShareDisabled) {
		t.Fatalf("disabled listener error = %v, want ErrShareDisabled", err)
	}
}
