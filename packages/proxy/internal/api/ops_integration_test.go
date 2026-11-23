package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/auth"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/terminal"
)

// stubOpsTerminal is a TerminalService that answers Execute with canned
// command output, so the ops endpoints can be exercised end-to-end without a
// real root PTY helper.
type stubOpsTerminal struct {
	commands map[string]terminal.ExecResult
}

func (s *stubOpsTerminal) Enabled() bool { return true }

func (s *stubOpsTerminal) Status() terminal.Status {
	return terminal.Status{Enabled: true, Privileged: true}
}

func (s *stubOpsTerminal) Open(ctx context.Context, actor, remote string) (terminal.Session, error) {
	return nil, terminal.ErrDisabled
}

func (s *stubOpsTerminal) ReportCwd(ctx context.Context, actor, cwd string) {}

func (s *stubOpsTerminal) Execute(ctx context.Context, command string, timeout time.Duration) (terminal.ExecResult, error) {
	if result, ok := s.commands[command]; ok {
		return result, nil
	}
	return terminal.ExecResult{ExitCode: 127, Stderr: "no stub for command"}, nil
}

func (s *stubOpsTerminal) ListFiles(ctx context.Context, path string) ([]terminal.FileEntry, error) {
	return nil, nil
}

func (s *stubOpsTerminal) StatFile(ctx context.Context, path string) (terminal.FileEntry, error) {
	return terminal.FileEntry{}, nil
}

func (s *stubOpsTerminal) DownloadFile(ctx context.Context, path string, writer io.Writer) (int64, error) {
	return 0, nil
}

func (s *stubOpsTerminal) UploadFile(ctx context.Context, dir, name string, reader io.Reader, size int64) error {
	return nil
}

func (s *stubOpsTerminal) Mkdir(ctx context.Context, path string) error { return nil }

func (s *stubOpsTerminal) RemoveFile(ctx context.Context, path string) error { return nil }

func newOpsTestServer(t *testing.T, terminalService TerminalService) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "ops-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenPath := filepath.Join(root, "admin-setup-token")
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	authService, err := auth.NewService(database, tokenPath, logger, box)
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.EnsureSetupToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	setupToken, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := authService.Setup(context.Background(), strings.TrimSpace(string(setupToken)), "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(&stubBundleService{}, logger, WithAuth(authService), WithTerminal(terminalService))
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	key, err := authService.CreateAPIKey(context.Background(), "ops-test")
	if err != nil {
		t.Fatal(err)
	}
	return testServer, key.Key
}

func TestOpsDiskEndpoint(t *testing.T) {
	terminalService := &stubOpsTerminal{commands: map[string]terminal.ExecResult{
		"df -B1 -P": {
			ExitCode: 0,
			Stdout: "Filesystem     1024-blocks        Used Available Capacity Mounted on\n" +
				"/dev/vda1     10068836864  9000000000 1068836864      89% /root/data\n" +
				"tmpfs            1994344         168   1994176       1% /dev\n",
		},
	}}
	testServer, key := newOpsTestServer(t, terminalService)
	response, err := testServer.Client().Get(testServer.URL + "/api/v1/system/disk")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request should be 401, got %d", response.StatusCode)
	}
	response.Body.Close()

	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/system/disk", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err = testServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var body struct {
		Filesystems []DiskUsage `json:"filesystems"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Filesystems) != 1 {
		t.Fatalf("expected 1 filesystem (tmpfs filtered), got %d", len(body.Filesystems))
	}
	if body.Filesystems[0].MountedOn != "/root/data" {
		t.Errorf("expected /root/data, got %q", body.Filesystems[0].MountedOn)
	}
}

func TestOpsDockerEndpoint(t *testing.T) {
	terminalService := &stubOpsTerminal{commands: map[string]terminal.ExecResult{
		`docker ps -a --no-trunc --format '{{json .}}'`: {
			ExitCode: 0,
			Stdout: "{\"ID\":\"abc123def456\",\"Names\":\"web-app\",\"Image\":\"nginx:latest\",\"State\":\"running\",\"Status\":\"Up 2 hours\",\"Ports\":\"0.0.0.0:8080->80/tcp\",\"CreatedAt\":\"2026-08-23 05:00:00 +0000 UTC\"}\n" +
				"{\"ID\":\"def456abc789\",\"Names\":\"db\",\"Image\":\"postgres:16\",\"State\":\"exited\",\"Status\":\"Exited (0) 2 days ago\",\"Ports\":\"\",\"CreatedAt\":\"2026-08-21 05:00:00 +0000 UTC\"}\n",
		},
		`docker stats --no-stream --format '{{json .}}'`: {
			ExitCode: 0,
			Stdout:   "{\"Container\":\"abc123def456\",\"CPUPerc\":\"1.23%\",\"MemUsage\":\"15.3MiB / 7.76GiB\",\"MemPerc\":\"0.19%\",\"NetIO\":\"1.2kB / 3.4kB\",\"BlockIO\":\"0B / 0B\"}\n",
		},
	}}
	testServer, key := newOpsTestServer(t, terminalService)
	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/docker/containers", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := testServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var body struct {
		Containers []DockerContainer `json:"containers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(body.Containers))
	}
	if body.Containers[0].CPUPerc != "1.23%" {
		t.Errorf("running container should carry stats, got %q", body.Containers[0].CPUPerc)
	}
	if body.Containers[1].CPUPerc != "" {
		t.Errorf("exited container should carry no stats, got %q", body.Containers[1].CPUPerc)
	}
}
