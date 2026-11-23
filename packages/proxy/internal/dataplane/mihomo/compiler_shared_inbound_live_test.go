package mihomo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// TestCompileSharedInboundPassesMihomoValidation feeds the compiled shared
// inbound configuration to a real Mihomo binary. A unit test can only prove the
// YAML has the shape we intended; only Mihomo can prove it accepts the
// aggregate listeners (users, ws-path, protocol demux on one port).
//
// It is skipped when no Mihomo binary is on PATH, mirroring the rest of the
// data-plane test suite.
func TestCompileSharedInboundPassesMihomoValidation(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	const marker = "11111111-1111-1111-1111-111111111111"
	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, 17890),
			aggregateListener("agg-vless", "vless", listener.SharedInboundWebSocketOwner, 17891),
			aggregateListener("agg-trojan", "trojan", listener.SharedInboundWebSocketOwner, 17891),
			memberListener(t, "member-a", "service-a", "mixed", "group-a", "svc-group-a", "pass-a", listener.SharedInboundStandardOwner, 17890),
			memberListener(t, "member-b", "service-b", "mixed", "group-b", "svc-group-b", "pass-b", listener.SharedInboundStandardOwner, 17890),
			memberListener(t, "member-vless", "service-vless", "vless", "group-a", "svc-group-a-vless", marker, listener.SharedInboundWebSocketOwner, 17891),
			memberListener(t, "member-trojan", "service-trojan", "trojan", "group-b", "svc-group-b-trojan", "trojan-pass", listener.SharedInboundWebSocketOwner, 17891),
		},
	}
	compiler, err := NewCompiler(repository, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(configPath, compiled.YAML, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-t", "-d", directory, "-f", configPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Mihomo rejected the shared inbound config: %v\n%s\n---config---\n%s", err, output, compiled.YAML)
	}
}
