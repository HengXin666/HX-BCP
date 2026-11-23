package mihomo

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestOpenLogStreamReadsUnixControllerWebSocket(t *testing.T) {
	t.Parallel()
	socketPath := filepath.Join(t.TempDir(), "controller.sock")
	unixListener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/logs" || request.URL.Query().Get("level") != "debug" {
			http.NotFound(writer, request)
			return
		}
		connection, acceptErr := websocket.Accept(writer, request, nil)
		if acceptErr != nil {
			return
		}
		defer connection.CloseNow()
		_ = connection.Write(request.Context(), websocket.MessageText, []byte(
			`{"type":"info","payload":"[TCP] 127.0.0.1:5000 --> example.test:443 using Fast[Tokyo]"}`,
		))
	})}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(unixListener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-serveDone
	})

	manager := &Manager{
		controllerSocket: socketPath,
		process:          &process{done: make(chan error)},
		status:           Status{Running: true},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reader, err := manager.OpenLogStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	event, err := reader.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.ProxyGroup != "Fast" || event.Node != "Tokyo" || event.Level != "info" {
		t.Fatalf("event = %#v", event)
	}
	if strings.Contains(event.Message, "example.test") {
		t.Fatalf("event leaks destination: %#v", event)
	}
}

func TestParseAndRedactLogRoute(t *testing.T) {
	t.Parallel()
	message := "[TCP] 127.0.0.1:53120 --> private.example:443 match Match using Fast Pool[Tokyo 01]"
	group, node := parseLogRoute(message)
	if group != "Fast Pool" || node != "Tokyo 01" {
		t.Fatalf("route = %q, %q", group, node)
	}
	redacted := redactLogMessage(message)
	if strings.Contains(redacted, "127.0.0.1") || strings.Contains(redacted, "private.example") {
		t.Fatalf("redacted message leaks connection endpoints: %q", redacted)
	}
	if !strings.Contains(redacted, "using Fast Pool[Tokyo 01]") {
		t.Fatalf("redacted message lost routing context: %q", redacted)
	}
}

func TestRedactLogMessageRemovesCredentials(t *testing.T) {
	t.Parallel()
	message := "fetch https://admin:hunter2@example.test/path authorization=Bearer Bearer-secret token=abc123 cookie:session-value"
	redacted := redactLogMessage(message)
	for _, secret := range []string{"admin", "hunter2", "Bearer-secret", "abc123", "session-value"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted message contains %q: %q", secret, redacted)
		}
	}
}

func TestNormalizeLogLevel(t *testing.T) {
	t.Parallel()
	if got := normalizeLogLevel("WARN"); got != "warning" {
		t.Fatalf("normalizeLogLevel(WARN) = %q", got)
	}
	if got := normalizeLogLevel("unknown"); got != "debug" {
		t.Fatalf("normalizeLogLevel(unknown) = %q", got)
	}
}
