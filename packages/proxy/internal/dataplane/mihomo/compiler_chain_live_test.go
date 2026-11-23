package mihomo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// TestCompileChainedConfigPassesMihomoValidation feeds a chained configuration to
// a real Mihomo binary. A unit test can only prove the document has the shape we
// intended; only Mihomo can prove it accepts the derived proxies.
//
// This matters more than usual here: Mihomo REFUSES dialer-proxy on a group and
// merely logs an error before ignoring it, so the failure mode this test guards
// against is a config that "validates" while every chain silently dials direct.
// It is skipped when no Mihomo binary is on PATH, like the rest of this suite.
func TestCompileChainedConfigPassesMihomoValidation(t *testing.T) {
	nodeA := chainNodeRecord(t, "node-a", "aaaaaaaaaaaaaaaa0000")
	nodeB := chainNodeRecord(t, "node-b", "bbbbbbbbbbbbbbbb0000")
	repository := chainRepository{
		groups: []store.ProxyGroupRecord{
			{ID: "g1", Name: "hop1", Strategy: "manual", Enabled: true, SourceSpecJSON: `{"node_ids":["node-a"]}`},
			{ID: "g2", Name: "hop2", Strategy: "manual", Enabled: true,
				SourceSpecJSON: `{"node_ids":["node-b"]}`, DialerProxyGroupID: "g1"},
		},
		nodes:      []store.NodeConfigRecord{nodeA, nodeB},
		candidates: []store.GroupNodeCandidate{{NodeConfigRecord: nodeA}, {NodeConfigRecord: nodeB}},
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
	// HX_CHAIN_DUMP lets a host-side run validate the real compiler output with a
	// native Mihomo binary: the container images this suite usually runs in are
	// musl-based and cannot execute the glibc-built release binary.
	if dump := os.Getenv("HX_CHAIN_DUMP"); dump != "" {
		// World-readable because this export exists precisely so a user outside
		// the build container can validate it; it holds no credentials.
		if err := os.WriteFile(dump, compiled.YAML, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Only now require the binary: the compile above must happen even where Mihomo
	// cannot run, so HX_CHAIN_DUMP can export the real document for host-side
	// validation in a musl container.
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	command := exec.Command(binary, "-t", "-d", directory, "-f", configPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Mihomo rejected the chained config: %v\n%s\n---config---\n%s", err, output, compiled.YAML)
	}
	// Mihomo exits 0 even for the group-level dialer error it logs, so an exit
	// code alone is not proof. Assert the specific complaint is absent too.
	if text := string(output); strings.Contains(text, "dialer-proxy configuration is not allowed") {
		t.Fatalf("Mihomo rejected a group-level dialer-proxy:\n%s", text)
	}
}
