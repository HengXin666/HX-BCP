package mihomo

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/metrics"
	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/subscription"
)

func TestMapTrafficConnectionsAttributesKnownResources(t *testing.T) {
	identities := map[string][]metrics.Resource{
		"hx-in-1": {
			{Type: store.TrafficResourceListener, ID: "listener-1"},
			{Type: store.TrafficResourceResidentialChannel, ID: "channel-1"},
		},
		"group":     {{Type: store.TrafficResourceProxyGroup, ID: "group-1"}},
		"hx-node-1": {{Type: store.TrafficResourceNode, ID: "node-1"}},
	}
	snapshot := mapTrafficConnections([]runtimeConnection{{
		ID: "connection-1", Upload: 10, Download: 20,
		Metadata: connectionMetadata{InboundName: "hx-in-1", SpecialProxy: "group"},
		Chains:   []string{"hx-node-1", "group", "DIRECT"},
	}}, identities)
	if len(snapshot.Connections) != 1 || len(snapshot.Connections[0].Resources) != 5 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestCloseConnectionsByInboundUserDoesNotTouchOtherSessions(t *testing.T) {
	t.Parallel()
	socketPath := filepath.Join(t.TempDir(), "mihomo.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu      sync.Mutex
		deleted []string
	)
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/connections":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"connections":[
				{"id":"connection-a","metadata":{"inboundName":"hx-in-channel-a","inboundUser":"hx-session-a"}},
				{"id":"connection-b","metadata":{"inboundName":"hx-in-channel-a","inboundUser":"hx-session-b"}},
				{"id":"connection-c","metadata":{"inboundName":"hx-in-channel-b","inboundUser":"hx-session-a"}}
            ]}`))
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/connections/"):
			mu.Lock()
			deleted = append(deleted, strings.TrimPrefix(request.URL.Path, "/connections/"))
			mu.Unlock()
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})

	manager := &Manager{
		externalProcess:  true,
		controllerSocket: socketPath,
		status:           Status{Running: true},
	}
	if err := manager.CloseConnectionsByInboundUser(context.Background(), "listener-channel-a", "hx-session-a"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deleted) != 1 || deleted[0] != "connection-a" {
		t.Fatalf("deleted connections = %v, want only connection-a", deleted)
	}
}

func TestManagerTestsProxyThroughUnixController(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo is not available")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(20 * time.Millisecond)
		writer.Header().Set("Content-Length", "2")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("OK"))
	}))
	defer origin.Close()
	socksAddress, closeSOCKS := startSOCKS5Server(t)
	defer closeSOCKS()

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
	subscriptions, err := subscription.NewService(
		database,
		box,
		subscription.WithRefresh(subscription.NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		subscription.WithParser(nodeparse.Parse),
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := subscriptions.Create(ctx, subscription.CreateRequest{
		Name:         "local-socks-proxy",
		SourceType:   subscription.SourceInline,
		SourceConfig: subscription.SourceConfig{Inline: "socks5://" + socksAddress + "#local-socks-proxy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subscriptions.Refresh(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	nodes, err := database.ListNodes(ctx, store.NodeFilter{Limit: 10})
	if err != nil || len(nodes) != 1 {
		t.Fatalf("ListNodes() = (%+v, %v)", nodes, err)
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
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Close(closeContext); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	if err := manager.Apply(ctx); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	status := manager.Status()
	if !status.Running || status.ProxyCount != 1 || status.ListenerCount != 0 {
		t.Fatalf("unexpected status: %+v", status)
	}
	latency, err := manager.TestProxy(ctx, nodes[0].Fingerprint, origin.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("TestProxy() error = %v", err)
	}
	if latency <= 0 {
		t.Fatalf("latency = %d", latency)
	}
}

func startSOCKS5Server(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5Connection(t, ctx, connection)
		}
	}()
	return listener.Addr().String(), func() {
		cancel()
		_ = listener.Close()
	}
}

func handleSOCKS5Connection(t *testing.T, ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	header := make([]byte, 2)
	if _, err := io.ReadFull(connection, header); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(connection, methods); err != nil {
		return
	}
	if _, err := connection.Write([]byte{5, 0}); err != nil {
		return
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(connection, request); err != nil || request[0] != 5 || request[1] != 1 {
		return
	}
	host, err := readSOCKSAddress(connection, request[3])
	if err != nil {
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(connection, portBytes); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBytes)
	targetAddress := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	target, err := (&net.Dialer{}).DialContext(ctx, "tcp", targetAddress)
	if err != nil {
		_, _ = connection.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	if _, err := connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = connection.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(target, connection)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(connection, target)
		done <- struct{}{}
	}()
	<-done
}

func readSOCKSAddress(reader io.Reader, addressType byte) (string, error) {
	switch addressType {
	case 1:
		buffer := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return "", err
		}
		return net.IP(buffer).String(), nil
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return "", err
		}
		buffer := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return "", err
		}
		return string(buffer), nil
	case 4:
		buffer := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return "", err
		}
		return net.IP(buffer).String(), nil
	default:
		return "", fmt.Errorf("unsupported SOCKS address type %d", addressType)
	}
}
