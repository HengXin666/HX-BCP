package entry

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type liveRepository struct{ nodes []store.NodeConfigRecord }

func (r liveRepository) ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error) {
	return nil, nil
}
func (r liveRepository) ListListeners(context.Context) ([]store.ListenerRecord, error) {
	return nil, nil
}
func (r liveRepository) ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error) {
	return r.nodes, nil
}
func (r liveRepository) ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error) {
	return nil, nil
}
func (r liveRepository) ListResidentialClientRoutes(context.Context) ([]store.ResidentialClientRouteRecord, error) {
	return nil, nil
}
func (r liveRepository) ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error) {
	return nil, nil
}
func (r liveRepository) GetMetadata(context.Context, string) (string, error) {
	return "", store.ErrNotFound
}

type openCipher struct{}

func (openCipher) Open(content, _ []byte) ([]byte, error) { return content, nil }

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// TestEntryHubThroughRealMihomo compiles a document with the entry hub, runs
// the real data plane on it and tunnels through hub -> node -> target. It is
// skipped when no Mihomo binary is on PATH.
func TestEntryHubThroughRealMihomo(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	nodeUsers := make(chan string, 4)
	nodeAddress := fakeHub(t, "node-pass", nodeUsers)
	host, portText, _ := net.SplitHostPort(nodeAddress)
	var port int
	_ = json.Unmarshal([]byte(portText), &port)
	canonical, _ := json.Marshal(map[string]any{"type": "socks5", "server": host, "port": port, "username": "node-user", "password": "node-pass"})
	repository := liveRepository{nodes: []store.NodeConfigRecord{{
		ID: "n1", Fingerprint: "abcdef0123456789abcdef", DisplayName: "US 01", Protocol: "socks5",
		LifecycleState: "healthy", CanonicalConfigEncrypted: canonical,
	}}}
	compiler, err := mihomo.NewCompiler(repository, openCipher{})
	if err != nil {
		t.Fatal(err)
	}
	hubPort := freePort(t)
	compiler.SetEntryHub(mihomo.EntryHub{Port: hubPort, Secret: "hub-secret-0123456789"})
	compiled, err := compiler.Compile(context.Background())
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(configPath, compiled.YAML, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "-t", "-d", directory, "-f", configPath).CombinedOutput(); err != nil {
		t.Fatalf("Mihomo rejected the hub config: %v\n%s\n%s", err, output, compiled.YAML)
	}
	process := exec.Command(binary, "-d", directory, "-f", configPath)
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _, _ = process.Process.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", compiled.Entry.HubAddress, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mihomo never opened the hub")
		}
		time.Sleep(100 * time.Millisecond)
	}

	holder := &snapshotHolder{}
	holder.value.Store(BuildSnapshot(compiled.Entry, map[string][]store.SourceClass{"n1": {store.SourceClassSubscription}}, nil, time.Now()))
	router := NewRouter(holder, nil)
	echo := echoServer(t)
	conn, info, err := router.Dial(context.Background(), "acme", Selector{Pool: PoolSubscription, Slot: "s1", TTL: 5, OnLoss: OnLossRebind}, echo)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16)
	read, _ := conn.Read(buffer)
	if string(buffer[:read]) != "echo:hi\n" {
		t.Fatalf("tunnel answered %q", buffer[:read])
	}
	if info.NodeID != "n1" || <-nodeUsers != "node-user" {
		t.Fatalf("route = %+v", info)
	}

	raw, err := net.Dial("tcp", compiled.Entry.HubAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
	if err := socksClientConnect(raw, "hxe-n-unknown", "hub-secret-0123456789", echo); err == nil {
		t.Fatal("the hub accepted an unknown user")
	}
}
