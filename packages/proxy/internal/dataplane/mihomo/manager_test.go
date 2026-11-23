package mihomo

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	listenerservice "github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/metrics"
	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/subscription"
)

func TestCommandEnvironmentStripsLocalProxyVariables(t *testing.T) {
	// A local proxy exported into the control plane's environment must not reach
	// the managed data plane: Mihomo is a Go program and honours
	// HTTP_PROXY/ALL_PROXY for its own provider, rule-set and geo fetches, so an
	// inherited proxy silently rewrites the data plane's egress.
	for name, value := range map[string]string{
		"http_proxy":  "http://127.0.0.1:7890",
		"HTTP_PROXY":  "http://127.0.0.1:7890",
		"https_proxy": "http://127.0.0.1:7890",
		"HTTPS_PROXY": "http://127.0.0.1:7890",
		"all_proxy":   "socks5://127.0.0.1:7890",
		"ALL_PROXY":   "socks5://127.0.0.1:7890",
		"no_proxy":    "example.com",
		"NO_PROXY":    "example.com",
		"ftp_proxy":   "http://127.0.0.1:7890",
	} {
		t.Setenv(name, value)
	}
	t.Setenv("HX_KEEP_ME", "1")

	environment := (&Manager{maxProcs: 3}).commandEnvironment()

	for _, value := range environment {
		name, _, _ := strings.Cut(value, "=")
		if _, isProxy := proxyEnvironmentNames[strings.ToLower(name)]; isProxy {
			t.Fatalf("commandEnvironment() leaked %q", value)
		}
	}
	if !slices.Contains(environment, "HX_KEEP_ME=1") {
		t.Fatalf("commandEnvironment() dropped an unrelated variable: %v", environment)
	}
	if !slices.Contains(environment, "GOMAXPROCS=3") {
		t.Fatalf("commandEnvironment() lost the GOMAXPROCS pin: %v", environment)
	}
}

func TestValidateEndpointAvailabilityRejectsPortOwnedByAnotherProcess(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	manager := &Manager{}
	err = manager.validateEndpointAvailabilityLocked([]Endpoint{{BindAddress: "127.0.0.1", Port: port}})
	if err == nil {
		t.Fatal("validateEndpointAvailabilityLocked() succeeded for an occupied port")
	}
	if !strings.Contains(err.Error(), "listener") || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unexpected occupied-port error: %v", err)
	}
}

func TestValidateEndpointAvailabilityAllowsSamePortBindChange(t *testing.T) {
	// The occupied port is owned by the manager's own current config, so a
	// same-port bind-address transition (127.0.0.1 -> 0.0.0.0) must not be
	// blocked by the availability probe: the old listener is released during
	// the reload.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	manager := &Manager{
		lastCompiled: Compiled{Endpoints: []Endpoint{{BindAddress: "127.0.0.1", Port: port}}},
		process:      &process{command: &exec.Cmd{}},
	}
	err = manager.validateEndpointAvailabilityLocked([]Endpoint{{BindAddress: "0.0.0.0", Port: port}})
	if err != nil {
		t.Fatalf("same-port bind change was blocked: %v", err)
	}

	// A port NOT owned by the manager is still probed and rejected when
	// another process holds it.
	manager = &Manager{process: &process{command: &exec.Cmd{}}}
	err = manager.validateEndpointAvailabilityLocked([]Endpoint{{BindAddress: "127.0.0.1", Port: port}})
	if err == nil {
		t.Fatal("validateEndpointAvailabilityLocked() succeeded for a foreign occupied port")
	}
}

func TestManagerRunsDirectMixedListener(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := NewCompiler(database, box)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(
		compiler,
		binary,
		filepath.Join(directory, "runtime", "active.yaml"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithEgressInterface("lo"),
		WithProcessMaxProcs(2),
		WithLogRotation(1<<20, 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Close(closeContext); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	groupService, err := proxygroup.NewService(database, manager)
	if err != nil {
		t.Fatal(err)
	}
	listenerService, err := listenerservice.NewService(database, box, manager)
	if err != nil {
		t.Fatal(err)
	}
	group, err := groupService.Create(ctx, proxygroup.CreateRequest{
		Name:     "direct-test",
		Strategy: "manual",
		SourceSpec: proxygroup.SourceSpec{
			IncludeDirect: true,
		},
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	port := reservePort(t)
	createdListener, err := listenerService.Create(ctx, listenerservice.CreateRequest{
		Name:         "direct-mixed",
		Kind:         "mixed",
		BindAddress:  "127.0.0.1",
		Port:         port,
		ProxyGroupID: group.ID,
	})
	if err != nil {
		t.Fatalf("create listener: %v", err)
	}
	if createdListener.Port != port {
		t.Fatalf("listener port = %d, want %d", createdListener.Port, port)
	}
	status := manager.Status()
	if !status.Available || !status.Running || status.ListenerCount != 1 {
		t.Fatalf("unexpected manager status: %+v", status)
	}
	initialPID := status.PID
	if initialPID <= 0 {
		t.Fatalf("managed Mihomo PID = %d, want a running process", initialPID)
	}
	if err := manager.Apply(ctx); err != nil {
		t.Fatalf("hot reload unchanged config: %v", err)
	}
	if reloadedPID := manager.Status().PID; reloadedPID != initialPID {
		t.Fatalf("hot reload changed managed Mihomo PID from %d to %d", initialPID, reloadedPID)
	}
	if status.EgressInterface != "lo" || status.MaxProcs != 2 || status.LogMaxBytes != 1<<20 {
		t.Fatalf("unexpected manager safeguards: %+v", status)
	}
	activeConfig, err := os.ReadFile(filepath.Join(directory, "runtime", "active.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(activeConfig), "interface-name: lo") {
		t.Fatalf("compiled config does not bind the egress interface:\n%s", activeConfig)
	}

	releaseOrigin := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOrigin) }) }
	defer release()
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-releaseOrigin
		_, _ = writer.Write([]byte("proxied-through-mihomo"))
	}))
	defer origin.Close()
	proxyURL, err := url.Parse("http://127.0.0.1:" + portString(port))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("request through Mihomo listener: %v", err)
	}
	defer response.Body.Close()
	var traffic metrics.RuntimeSnapshot
	foundListener := false
	foundGroup := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (!foundListener || !foundGroup) {
		traffic, err = manager.TrafficSnapshot(ctx)
		if err != nil {
			t.Fatalf("TrafficSnapshot() error = %v", err)
		}
		for _, connection := range traffic.Connections {
			for _, resource := range connection.Resources {
				foundListener = foundListener || (resource.Type == store.TrafficResourceListener && resource.ID == createdListener.ID)
				foundGroup = foundGroup || (resource.Type == store.TrafficResourceProxyGroup && resource.ID == group.ID)
			}
		}
		if !foundListener || !foundGroup {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !foundListener || !foundGroup {
		t.Fatalf("live connection attribution missing: listener=%t group=%t snapshot=%+v", foundListener, foundGroup, traffic)
	}
	release()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied-through-mihomo" {
		t.Fatalf("response body = %q", body)
	}
}

func TestManagerRunsAuthenticatedHTTPSConnect(t *testing.T) {
	manager, groupService, listenerService := newRuntimeTestServices(t)
	ctx := context.Background()
	group, err := groupService.Create(ctx, proxygroup.CreateRequest{
		Name: "direct-connect", Strategy: "manual",
		SourceSpec: proxygroup.SourceSpec{IncludeDirect: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	port := reservePort(t)
	_, err = listenerService.Create(ctx, listenerservice.CreateRequest{
		Name: "authenticated-connect", Kind: "mixed", BindAddress: "127.0.0.1", Port: port, ProxyGroupID: group.ID,
		Auth: &listenerservice.Auth{Username: "proxy-user", Password: "proxy-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !manager.Status().Running {
		t.Fatal("Mihomo is not running")
	}

	origin := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("https-through-connect"))
	}))
	defer origin.Close()
	proxyURL, err := url.Parse("http://proxy-user:proxy-password@" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test server certificate.
		},
	}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("HTTPS request through authenticated CONNECT listener: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "https-through-connect" {
		t.Fatalf("response body = %q", body)
	}
}

func TestManagerRunsSOCKS5ListenerThroughSelectedNode(t *testing.T) {
	upstreamAddress, closeUpstream := startSOCKS5Server(t)
	defer closeUpstream()
	manager, groupService, listenerService, subscriptionService := newRuntimeTestServicesWithSubscriptions(t)
	ctx := context.Background()
	subscriptionRecord, err := subscriptionService.Create(ctx, subscription.CreateRequest{
		Name: "local-upstream", SourceType: "inline",
		SourceConfig: subscription.SourceConfig{Inline: "socks5://" + upstreamAddress + "#local-upstream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subscriptionService.Refresh(ctx, subscriptionRecord.ID); err != nil {
		t.Fatal(err)
	}
	group, err := groupService.Create(ctx, proxygroup.CreateRequest{
		Name: "selected-upstream", Strategy: "manual",
		SourceSpec: proxygroup.SourceSpec{SubscriptionIDs: []string{subscriptionRecord.ID}},
		// This fixture deliberately has exactly one upstream so the dataplane
		// test can prove which node a listener selected. That is the managed
		// exemption, not an API caller wrapping a node in a group.
		AllowSingleNode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	port := reservePort(t)
	_, err = listenerService.Create(ctx, listenerservice.CreateRequest{
		Name: "socks-entry", Kind: "socks", BindAddress: "127.0.0.1", Port: port, ProxyGroupID: group.ID,
		Auth: &listenerservice.Auth{Username: "socks-user", Password: "socks-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !manager.Status().Running {
		t.Fatal("Mihomo is not running")
	}

	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("through-selected-socks-node"))
	}))
	defer origin.Close()
	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialSOCKS5(t, "127.0.0.1:"+portString(port), originURL.Host, "socks-user", "socks-password")
	defer connection.Close()
	if _, err := fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", originURL.Host); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read response through SOCKS5 listener: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "through-selected-socks-node" {
		t.Fatalf("response body = %q", body)
	}
}

func TestManagerValidatesWebSocketProtocolListeners(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := NewCompiler(database, box)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(compiler, binary, filepath.Join(directory, "runtime", "active.yaml"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Close(closeContext); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	groupService, _ := proxygroup.NewService(database, manager)
	listenerService, _ := listenerservice.NewService(database, box, manager)
	group, err := groupService.Create(ctx, proxygroup.CreateRequest{Name: "direct-vless", Strategy: "manual", SourceSpec: proxygroup.SourceSpec{IncludeDirect: true}})
	if err != nil {
		t.Fatal(err)
	}
	protocols := []struct {
		kind       string
		credential string
	}{
		{kind: "vless", credential: "11111111-1111-1111-1111-111111111111"},
		{kind: "vmess", credential: "22222222-2222-2222-2222-222222222222"},
		{kind: "trojan", credential: "strong-trojan-password"},
	}
	for index, protocol := range protocols {
		created, err := listenerService.Create(ctx, listenerservice.CreateRequest{
			Name: protocol.kind + "-ws", Kind: protocol.kind, BindAddress: "127.0.0.1", Port: reservePort(t), ProxyGroupID: group.ID,
			Auth:           &listenerservice.Auth{Username: "hx-user", Password: protocol.credential},
			Transport:      listenerservice.Transport{Type: "ws", WSPath: "/edge-" + protocol.kind},
			PublicEndpoint: listenerservice.PublicEndpoint{Host: "proxy.example.com", Port: 443, TLS: true},
		})
		if err != nil {
			t.Fatalf("create %s listener: %v", protocol.kind, err)
		}
		if !created.AuthConfigured || manager.Status().ListenerCount != index+1 {
			t.Fatalf("unexpected %s listener/status: %+v %+v", protocol.kind, created, manager.Status())
		}
	}
}

func newRuntimeTestServices(t *testing.T) (*Manager, *proxygroup.Service, *listenerservice.Service) {
	manager, groups, listeners, _ := newRuntimeTestServicesWithSubscriptions(t)
	return manager, groups, listeners
}

func newRuntimeTestServicesWithSubscriptions(t *testing.T) (*Manager, *proxygroup.Service, *listenerservice.Service, *subscription.Service) {
	t.Helper()
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	ctx := context.Background()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := NewCompiler(database, box)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(
		compiler,
		binary,
		filepath.Join(directory, "runtime", "active.yaml"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := proxygroup.NewService(database, manager)
	if err != nil {
		t.Fatal(err)
	}
	listeners, err := listenerservice.NewService(database, box, manager)
	if err != nil {
		t.Fatal(err)
	}
	subscriptions, err := subscription.NewService(
		database,
		box,
		subscription.WithRefresh(subscription.NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		subscription.WithParser(nodeparse.Parse),
		subscription.WithReconciler(manager),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Close(closeContext); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := database.Close(); err != nil {
			t.Errorf("database Close() error = %v", err)
		}
	})
	return manager, groups, listeners, subscriptions
}

func dialSOCKS5(t *testing.T, proxyAddress, targetAddress, username, password string) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp", proxyAddress, 5*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5 listener: %v", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = connection.Close()
		}
	}()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := connection.Write([]byte{5, 1, 2}); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(connection, response); err != nil || response[0] != 5 || response[1] != 2 {
		t.Fatalf("SOCKS5 method response = %v, error = %v", response, err)
	}
	if len(username) > 255 || len(password) > 255 {
		t.Fatal("test SOCKS5 credentials are too long")
	}
	authRequest := []byte{1, byte(len(username))}
	authRequest = append(authRequest, username...)
	authRequest = append(authRequest, byte(len(password)))
	authRequest = append(authRequest, password...)
	if _, err := connection.Write(authRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(connection, response); err != nil || response[1] != 0 {
		t.Fatalf("SOCKS5 authentication response = %v, error = %v", response, err)
	}
	host, portValue, err := net.SplitHostPort(targetAddress)
	if err != nil {
		t.Fatal(err)
	}
	port, err := net.LookupPort("tcp", portValue)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		request = append(request, 1)
		request = append(request, ip.To4()...)
	} else {
		if len(host) > 255 {
			t.Fatal("test target host is too long")
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err := connection.Write(request); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(connection, header); err != nil || header[0] != 5 || header[1] != 0 {
		t.Fatalf("SOCKS5 connect response = %v, error = %v", header, err)
	}
	if _, err := readSOCKSAddress(connection, header[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(connection, make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Time{})
	failed = false
	return connection
}

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func portString(port int) string {
	return fmt.Sprintf("%d", port)
}
