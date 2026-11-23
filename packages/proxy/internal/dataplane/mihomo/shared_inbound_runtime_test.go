package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// TestSharedInboundRoutesOnePortPerService is the end-to-end proof for the
// "one client, many groups" model: a real Mihomo process serves a SINGLE Mixed
// port, and two services authenticated with different usernames reach two
// different proxy groups on that same socket.
//
// group-a is the server's DIRECT exit, group-b is fail-closed (REJECT), so the
// same request succeeds through one credential and fails through the other. If
// the IN-USER routing were broken, both requests would take the same path and
// the test would fail either way.
func TestSharedInboundRoutesOnePortPerService(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()

	port := reservePort(t)
	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{
			{ID: "group-a", Name: "group-a", Enabled: true, Strategy: "manual", SourceSpecJSON: `{"include_direct":true}`},
			{ID: "group-b", Name: "group-b", Enabled: true, Strategy: "manual", SourceSpecJSON: "{}", EmptyBehavior: "fail-closed"},
		},
		listeners: []store.ListenerRecord{
			aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, port),
			memberListener(t, "member-a", "service-a", "mixed", "group-a", "svc-group-a", "pass-a", listener.SharedInboundStandardOwner, port),
			memberListener(t, "member-b", "service-b", "mixed", "group-b", "svc-group-b", "pass-b", listener.SharedInboundStandardOwner, port),
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

	// The listening socket can accept a connection before the rule engine has
	// finished loading; a 502 in that window is a startup artifact, not a
	// routing failure. Retry until the direct service answers, exactly as a
	// client would.
	direct := waitForStatus(t, port, "svc-group-a", "pass-a", origin.URL, http.StatusNoContent, 10*time.Second)
	if direct != http.StatusNoContent {
		t.Fatalf("service-a through the shared port = %d, want %d", direct, http.StatusNoContent)
	}
	rejected := proxiedStatus(t, port, "svc-group-b", "pass-b", origin.URL)
	if rejected == http.StatusNoContent {
		t.Fatalf("service-b reached the direct exit; IN-USER routing did not separate the services")
	}
	// A wrong password must not be accepted: the aggregate listener is the only
	// public surface and it authenticates every member.
	if status := proxiedStatus(t, port, "svc-group-a", "wrong", origin.URL); status == http.StatusNoContent {
		t.Fatal("an invalid password reached the direct exit")
	}
}

// waitForStatus polls until the request reaches the expected status or the
// deadline expires, then returns the last observed status. Mihomo accepts
// connections slightly before it finishes loading its rules, so a configuration
// test must allow that window instead of asserting on the first response.
func waitForStatus(t *testing.T, port int, username, password, target string, expected int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := 0
	for {
		last = proxiedStatus(t, port, username, password, target)
		if last == expected || time.Now().After(deadline) {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func proxiedStatus(t *testing.T, port int, username, password, target string) int {
	t.Helper()
	proxyURL, err := url.Parse(fmt.Sprintf("http://%s:%s@127.0.0.1:%d", username, password, port))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Timeout:   8 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true},
	}
	response, err := client.Get(target)
	if err != nil {
		// A rejected request surfaces as a proxy error; that is a valid outcome
		// for the fail-closed group.
		return 0
	}
	defer response.Body.Close()
	return response.StatusCode
}

func waitForPort(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("shared inbound port %d did not open", port)
}

var _ = json.Marshal
