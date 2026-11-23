package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/HengXin666/hx-pxy/internal/proxylog"
)

const maxLogFrameSize = 32 << 10

var (
	logRoutePattern        = regexp.MustCompile(`(?i)\busing\s+([^\[\r\n]+)\[([^\]\r\n]+)\]`)
	connectionRoutePattern = regexp.MustCompile(`\S+:\d+\s+-->\s+\S+:\d+`)
	userinfoPattern        = regexp.MustCompile(`(?i)(://)[^@/\s]+@`)
	secretFieldPattern     = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|cookie|password|passwd|token|access_token|api[_-]?key|secret)(\s*[:=]\s*)(?:(?:bearer|basic)\s+)?[^\s,;]+`)
)

type mihomoLogPayload struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

type logReader struct {
	connection    *websocket.Conn
	transport     *http.Transport
	nodeIdentity  map[string]logIdentity
	groupIdentity map[string]string
	closeOnce     sync.Once
}

type logIdentity struct {
	id   string
	name string
}

func (m *Manager) OpenLogStream(ctx context.Context) (proxylog.Reader, error) {
	m.mu.Lock()
	m.refreshProcessLocked()
	if !m.isRunningLocked() || !m.status.Running {
		m.mu.Unlock()
		return nil, ErrNotRunning
	}
	socketPath := m.controllerSocket
	m.mu.Unlock()
	nodeIdentity := make(map[string]logIdentity)
	groupIdentity := make(map[string]string)
	if m.compiler != nil {
		nodes, err := m.compiler.repository.ListNodeConfigs(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("load Mihomo log node identities: %w", err)
		}
		for _, node := range nodes {
			nodeIdentity[nodeProxyName(node.Fingerprint)] = logIdentity{id: node.ID, name: node.DisplayName}
		}
		groups, err := m.compiler.repository.ListProxyGroups(ctx)
		if err != nil {
			return nil, fmt.Errorf("load Mihomo log group identities: %w", err)
		}
		for _, group := range groups {
			groupIdentity[group.Name] = group.ID
		}
	}

	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialContext context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(dialContext, "unix", socketPath)
		},
	}
	client := &http.Client{Transport: transport}
	connection, response, err := websocket.Dial(ctx, "ws://unix/logs?level=debug", &websocket.DialOptions{
		HTTPClient: client,
	})
	if err != nil {
		transport.CloseIdleConnections()
		if response != nil {
			return nil, fmt.Errorf("open Mihomo log stream: controller returned status %d: %w", response.StatusCode, err)
		}
		return nil, fmt.Errorf("open Mihomo log stream: %w", err)
	}
	connection.SetReadLimit(maxLogFrameSize)
	return &logReader{
		connection:    connection,
		transport:     transport,
		nodeIdentity:  nodeIdentity,
		groupIdentity: groupIdentity,
	}, nil
}

func (r *logReader) Next(ctx context.Context) (proxylog.Event, error) {
	for {
		messageType, payload, err := r.connection.Read(ctx)
		if err != nil {
			return proxylog.Event{}, err
		}
		if messageType != websocket.MessageText {
			continue
		}
		var raw mihomoLogPayload
		if err := json.Unmarshal(payload, &raw); err != nil {
			return proxylog.Event{}, fmt.Errorf("decode Mihomo log event: %w", err)
		}
		message := strings.TrimSpace(raw.Payload)
		if message == "" {
			continue
		}
		group, runtimeNode := parseLogRoute(message)
		node := runtimeNode
		nodeID := ""
		if identity, exists := r.nodeIdentity[runtimeNode]; exists {
			nodeID = identity.id
			node = identity.name
		}
		return proxylog.Event{
			Timestamp:    time.Now().UTC(),
			Level:        normalizeLogLevel(raw.Type),
			Message:      redactLogMessage(message),
			ProxyGroupID: r.groupIdentity[group],
			ProxyGroup:   group,
			NodeID:       nodeID,
			Node:         node,
		}, nil
	}
}

func (r *logReader) Close() error {
	var closeErr error
	r.closeOnce.Do(func() {
		closeErr = r.connection.CloseNow()
		r.transport.CloseIdleConnections()
	})
	return closeErr
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error":
		return "error"
	case "warning", "warn":
		return "warning"
	case "info":
		return "info"
	default:
		return "debug"
	}
}

func parseLogRoute(message string) (string, string) {
	matches := logRoutePattern.FindStringSubmatch(message)
	if len(matches) != 3 {
		return "", ""
	}
	return strings.TrimSpace(matches[1]), strings.TrimSpace(matches[2])
}

func redactLogMessage(message string) string {
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	message = connectionRoutePattern.ReplaceAllString(message, "[source] --> [destination]")
	message = userinfoPattern.ReplaceAllString(message, `${1}[redacted]@`)
	message = secretFieldPattern.ReplaceAllString(message, `${1}${2}[redacted]`)
	message = strings.TrimSpace(message)
	if len(message) > 2048 {
		message = message[:2048]
	}
	return message
}

var _ proxylog.Reader = (*logReader)(nil)
