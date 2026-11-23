package mihomo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// TestSharedInboundCarriesEveryStandardProtocolAtRuntime is the end-to-end proof
// behind the family fix: one Mixed entry point must actually proxy traffic for a
// service created as "http" or "socks", not just appear in the compiled YAML.
//
// The compiled document is not evidence on its own. Mihomo validates its own
// listener definitions at load time and silently ignores a definition it cannot
// use, so the test starts a real process and drives it the way a client does:
// through the published username and password, over a real socket.
func TestSharedInboundCarriesEveryStandardProtocolAtRuntime(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()

	port := reservePort(t)
	aggregate := aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, port)
	aggregate.BindAddress = "127.0.0.1"

	// One group per protocol so a wrong IN-USER route would land on the wrong
	// egress rather than accidentally succeeding.
	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{
			directGroup("group-http"),
			directGroup("group-socks"),
			directGroup("group-mixed"),
		},
		listeners: []store.ListenerRecord{
			aggregate,
			memberListener(t, "member-http", "service-http", "http", "group-http", "svc-http", "pass-http", listener.SharedInboundStandardOwner, port),
			memberListener(t, "member-socks", "service-socks", "socks", "group-socks", "svc-socks", "pass-socks", listener.SharedInboundStandardOwner, port),
			memberListener(t, "member-mixed", "service-mixed", "mixed", "group-mixed", "svc-mixed", "pass-mixed", listener.SharedInboundStandardOwner, port),
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
	command := exec.Command(binary, "-d", directory, "-f", configPath)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()
	waitForPort(t, port, 10*time.Second)

	for _, member := range []struct{ username, password string }{
		{"svc-http", "pass-http"},
		{"svc-socks", "pass-socks"},
		{"svc-mixed", "pass-mixed"},
	} {
		status := waitForStatus(t, port, member.username, member.password, origin.URL, http.StatusNoContent, 10*time.Second)
		if status != http.StatusNoContent {
			t.Fatalf("username %q status = %d, want %d: the service is not carried by the Mixed entry point", member.username, status, http.StatusNoContent)
		}
	}
}

// TestSharedInboundRejectsUnknownUsername proves the negative side: the entry
// point must not silently proxy for a service that is not a member, otherwise a
// passing test above could be explained by an open listener.
func TestSharedInboundRejectsUnknownUsername(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()

	port := reservePort(t)
	aggregate := aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, port)
	aggregate.BindAddress = "127.0.0.1"
	compiler, err := NewCompiler(sharedInboundRepository{
		groups: []store.ProxyGroupRecord{directGroup("group-http")},
		listeners: []store.ListenerRecord{
			aggregate,
			memberListener(t, "member-http", "service-http", "http", "group-http", "svc-http", "pass-http", listener.SharedInboundStandardOwner, port),
		},
	}, plaintextCipher{})
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
	command := exec.Command(binary, "-d", directory, "-f", configPath)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()
	waitForPort(t, port, 10*time.Second)

	// Wait for the router first, then assert the stranger is refused.
	if status := waitForStatus(t, port, "svc-http", "pass-http", origin.URL, http.StatusNoContent, 10*time.Second); status != http.StatusNoContent {
		t.Fatalf("the known member did not work, so the negative case proves nothing (status = %d)", status)
	}
	if status := proxiedStatus(t, port, "svc-stranger", "pass-stranger", origin.URL); status == http.StatusNoContent {
		t.Fatal("an unknown username was proxied by the shared entry point")
	}
}

// directGroup is a manually curated group whose only egress is DIRECT, so a
// request either reaches the origin or exposes a broken route.
func directGroup(id string) store.ProxyGroupRecord {
	group := testGroup(id)
	group.SourceSpecJSON = `{"include_direct":true}`
	return group
}
