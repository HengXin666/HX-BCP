package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/overview"
)

type overviewConnectionsResponse struct {
	Connections []overviewConnection `json:"connections"`
}

type overviewConnection struct {
	ID       string `json:"id"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

func (m *Manager) OverviewSnapshot(ctx context.Context) (overview.Snapshot, error) {
	m.mu.Lock()
	m.refreshProcessLocked()
	if !m.isRunningLocked() || !m.status.Running {
		m.mu.Unlock()
		return overview.Snapshot{}, ErrNotRunning
	}
	socketPath := m.controllerSocket
	m.mu.Unlock()

	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialContext context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(dialContext, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/connections", nil)
	if err != nil {
		return overview.Snapshot{}, fmt.Errorf("create Mihomo overview request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		if strings.Contains(err.Error(), "dial unix") {
			return overview.Snapshot{}, fmt.Errorf("%w: control socket unreachable", ErrNotRunning)
		}
		return overview.Snapshot{}, fmt.Errorf("request Mihomo overview: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return overview.Snapshot{}, fmt.Errorf("Mihomo connections returned status %d", response.StatusCode)
	}
	var payload overviewConnectionsResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&payload); err != nil {
		return overview.Snapshot{}, fmt.Errorf("decode Mihomo overview: %w", err)
	}
	snapshot := overview.Snapshot{Connections: make([]overview.Connection, 0, len(payload.Connections))}
	for _, connection := range payload.Connections {
		snapshot.Connections = append(snapshot.Connections, overview.Connection{
			ID: connection.ID, Upload: connection.Upload, Download: connection.Download,
		})
	}
	return snapshot, nil
}
