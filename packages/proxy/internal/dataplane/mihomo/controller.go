package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/metrics"
	"github.com/HengXin666/hx-pxy/internal/store"
)

var ErrNotRunning = errors.New("mihomo is not running")

type DelayResult struct {
	Delay int `json:"delay"`
}

type connectionsResponse struct {
	Connections []runtimeConnection `json:"connections"`
}

type runtimeConnection struct {
	ID       string             `json:"id"`
	Upload   int64              `json:"upload"`
	Download int64              `json:"download"`
	Chains   []string           `json:"chains"`
	Metadata connectionMetadata `json:"metadata"`
}

type connectionMetadata struct {
	SourceIP        string `json:"sourceIP"`
	SourcePort      string `json:"sourcePort"`
	DestinationIP   string `json:"destinationIP"`
	DestinationPort string `json:"destinationPort"`
	Host            string `json:"host"`
	Network         string `json:"network"`
	InboundName     string `json:"inboundName"`
	InboundUser     string `json:"inboundUser"`
	SpecialProxy    string `json:"specialProxy"`
}

func (m *Manager) TrafficSnapshot(ctx context.Context) (metrics.RuntimeSnapshot, error) {
	m.mu.Lock()
	m.refreshProcessLocked()
	if !m.isRunningLocked() || !m.status.Running {
		m.mu.Unlock()
		return metrics.RuntimeSnapshot{}, ErrNotRunning
	}
	socketPath := m.controllerSocket
	endpoints := append([]Endpoint(nil), m.lastCompiled.Endpoints...)
	m.mu.Unlock()

	nodes, err := m.compiler.repository.ListNodeConfigs(ctx, nil)
	if err != nil {
		return metrics.RuntimeSnapshot{}, fmt.Errorf("list traffic node identities: %w", err)
	}
	groups, err := m.compiler.repository.ListProxyGroups(ctx)
	if err != nil {
		return metrics.RuntimeSnapshot{}, fmt.Errorf("list traffic group identities: %w", err)
	}
	channels, err := m.compiler.repository.ListResidentialChannels(ctx)
	if err != nil {
		return metrics.RuntimeSnapshot{}, fmt.Errorf("list residential traffic identities: %w", err)
	}
	identities := make(map[string][]metrics.Resource, len(nodes)+len(groups)+len(endpoints))
	for _, record := range nodes {
		addTrafficIdentity(identities, nodeProxyName(record.Fingerprint), metrics.Resource{Type: store.TrafficResourceNode, ID: record.ID})
	}
	for _, record := range groups {
		addTrafficIdentity(identities, record.Name, metrics.Resource{Type: store.TrafficResourceProxyGroup, ID: record.ID})
	}
	for _, endpoint := range endpoints {
		addTrafficIdentity(identities, listenerConfigName(endpoint.ID), metrics.Resource{Type: store.TrafficResourceListener, ID: endpoint.ID})
	}
	for _, channel := range channels {
		resource := metrics.Resource{Type: store.TrafficResourceResidentialChannel, ID: channel.ID}
		addTrafficIdentity(identities, listenerConfigName(channel.ListenerID), resource)
		if channel.DirectListenerID != "" {
			addTrafficIdentity(identities, listenerConfigName(channel.DirectListenerID), resource)
		}
	}

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
		return metrics.RuntimeSnapshot{}, fmt.Errorf("create Mihomo connections request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		if strings.Contains(err.Error(), "dial unix") {
			return metrics.RuntimeSnapshot{}, fmt.Errorf("%w: control socket unreachable", ErrNotRunning)
		}
		return metrics.RuntimeSnapshot{}, fmt.Errorf("request Mihomo connections: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return metrics.RuntimeSnapshot{}, fmt.Errorf("Mihomo connections returned status %d", response.StatusCode)
	}
	var payload connectionsResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
	if err := decoder.Decode(&payload); err != nil {
		return metrics.RuntimeSnapshot{}, fmt.Errorf("decode Mihomo connections: %w", err)
	}
	return mapTrafficConnections(payload.Connections, identities), nil
}

func addTrafficIdentity(identities map[string][]metrics.Resource, name string, resource metrics.Resource) {
	identities[name] = append(identities[name], resource)
}

func mapTrafficConnections(connections []runtimeConnection, identities map[string][]metrics.Resource) metrics.RuntimeSnapshot {
	result := metrics.RuntimeSnapshot{Connections: make([]metrics.Connection, 0, len(connections))}
	for _, connection := range connections {
		resources := make([]metrics.Resource, 0, len(connection.Chains)+2)
		if matched, exists := identities[connection.Metadata.InboundName]; exists {
			resources = append(resources, matched...)
		}
		if matched, exists := identities[connection.Metadata.SpecialProxy]; exists {
			resources = append(resources, matched...)
		}
		for _, name := range connection.Chains {
			if matched, exists := identities[name]; exists {
				resources = append(resources, matched...)
			}
		}
		result.Connections = append(result.Connections, metrics.Connection{
			Detail: metrics.ConnectionDetail{
				SourceIP: connection.Metadata.SourceIP, SourcePort: connection.Metadata.SourcePort,
				Destination: connection.Metadata.DestinationIP, DestinationPort: connection.Metadata.DestinationPort,
				Network: connection.Metadata.Network, Inbound: connection.Metadata.InboundName,
			},
			ID: connection.ID, Upload: connection.Upload, Download: connection.Download, Resources: resources,
		})
	}
	return result
}

func (m *Manager) TestProxy(
	ctx context.Context,
	fingerprint string,
	testURL string,
	timeout time.Duration,
) (int, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return 0, errors.New("node fingerprint is required")
	}
	testURL = strings.TrimSpace(testURL)
	parsedURL, err := url.Parse(testURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return 0, errors.New("test URL must use HTTP or HTTPS")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}

	return m.proxyDelay(ctx, nodeProxyName(fingerprint), testURL, timeout)
}

// proxyDelay asks the data plane to measure one proxy's latency against a URL.
func (m *Manager) proxyDelay(
	ctx context.Context,
	proxyName string,
	testURL string,
	timeout time.Duration,
) (int, error) {
	m.mu.Lock()
	m.refreshProcessLocked()
	if !m.isRunningLocked() || !m.status.Running {
		m.mu.Unlock()
		return 0, ErrNotRunning
	}
	socketPath := m.controllerSocket
	m.mu.Unlock()

	client, transport := controllerClient(socketPath, timeout+time.Second)
	defer transport.CloseIdleConnections()
	requestURL := "http://unix/proxies/" + url.PathEscape(proxyName) + "/delay?" + url.Values{
		"timeout":  {strconv.FormatInt(timeout.Milliseconds(), 10)},
		"url":      {testURL},
		"expected": {"200-299"},
	}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create Mihomo delay request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		if strings.Contains(err.Error(), "dial unix") {
			return 0, fmt.Errorf("%w: control socket unreachable", ErrNotRunning)
		}
		return 0, fmt.Errorf("Mihomo proxy delay request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		var payload struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &payload)
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = strings.TrimSpace(string(body))
		}
		if len(message) > 256 {
			message = message[:256]
		}
		if message == "" {
			return 0, fmt.Errorf("Mihomo proxy delay returned status %d", response.StatusCode)
		}
		return 0, fmt.Errorf("Mihomo proxy delay returned status %d: %s", response.StatusCode, message)
	}
	var result DelayResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode Mihomo proxy delay: %w", err)
	}
	if result.Delay <= 0 {
		return 0, errors.New("Mihomo returned an invalid proxy delay")
	}
	return result.Delay, nil
}

// controllerClient builds an HTTP client bound to the Mihomo Unix control
// socket. Callers must close idle connections on the returned transport.
func controllerClient(socketPath string, timeout time.Duration) (*http.Client, *http.Transport) {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialContext context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(dialContext, "unix", socketPath)
		},
	}
	return &http.Client{Transport: transport, Timeout: timeout}, transport
}

// GroupSelection is a group's live member list and which member is selected.
//
// The stored group record holds a *spec* (subscriptions + filters), so its
// membership is a compile-time result. This reads what the running data plane
// actually has, which is what a caller rotating egress needs.
type GroupSelection struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Selectable is true only for a Selector: url-test re-picks by latency and
	// load-balance rotates, so a manual choice there would be silently undone.
	Selectable bool     `json:"selectable"`
	Now        string   `json:"now"`
	Members    []string `json:"members"`
}

// ReadGroup reads one group's live members and current selection. Read-only.
func (m *Manager) ReadGroup(ctx context.Context, groupName string) (GroupSelection, error) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return GroupSelection{}, errors.New("proxy group name is required")
	}
	m.mu.Lock()
	m.refreshProcessLocked()
	if !m.isRunningLocked() || !m.status.Running {
		m.mu.Unlock()
		return GroupSelection{}, ErrNotRunning
	}
	socketPath := m.controllerSocket
	m.mu.Unlock()

	client, transport := controllerClient(socketPath, 5*time.Second)
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		"http://unix/proxies/"+url.PathEscape(groupName), nil,
	)
	if err != nil {
		return GroupSelection{}, fmt.Errorf("create Mihomo group read request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		if strings.Contains(err.Error(), "dial unix") {
			return GroupSelection{}, fmt.Errorf("%w: control socket unreachable", ErrNotRunning)
		}
		return GroupSelection{}, fmt.Errorf("Mihomo group read failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return GroupSelection{}, fmt.Errorf(
			"Mihomo group read returned status %d: %s",
			response.StatusCode, controllerMessage(response.Body),
		)
	}
	var raw struct {
		Type string   `json:"type"`
		Now  string   `json:"now"`
		All  []string `json:"all"`
	}
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		return GroupSelection{}, fmt.Errorf("decode Mihomo group: %w", err)
	}
	out := GroupSelection{Name: groupName, Type: raw.Type, Now: raw.Now}
	for _, name := range raw.All {
		if strings.EqualFold(name, "REJECT") || strings.EqualFold(name, "DIRECT") {
			continue
		}
		out.Members = append(out.Members, name)
	}
	out.Selectable = strings.EqualFold(raw.Type, "Selector")
	return out, nil
}

// SelectProxy points a `select` proxy group at one of its members.
//
// This is the mechanism that makes residential exit-IP rotation cheap: it is a
// single control-socket call against the running data plane, so it neither
// recompiles the configuration nor reloads Mihomo, and connections belonging to
// other groups are untouched.
func (m *Manager) SelectProxy(ctx context.Context, groupName, proxyName string) error {
	groupName = strings.TrimSpace(groupName)
	proxyName = strings.TrimSpace(proxyName)
	if groupName == "" || proxyName == "" {
		return errors.New("proxy group and member names are required")
	}

	m.mu.Lock()
	m.refreshProcessLocked()
	if !m.isRunningLocked() || !m.status.Running {
		m.mu.Unlock()
		return ErrNotRunning
	}
	socketPath := m.controllerSocket
	m.mu.Unlock()

	payload, err := json.Marshal(map[string]string{"name": proxyName})
	if err != nil {
		return fmt.Errorf("encode Mihomo proxy selection: %w", err)
	}
	client, transport := controllerClient(socketPath, 5*time.Second)
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		"http://unix/proxies/"+url.PathEscape(groupName),
		bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("create Mihomo proxy selection request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if strings.Contains(err.Error(), "dial unix") {
			return fmt.Errorf("%w: control socket unreachable", ErrNotRunning)
		}
		return fmt.Errorf("Mihomo proxy selection failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf(
		"Mihomo proxy selection returned status %d: %s",
		response.StatusCode,
		controllerMessage(response.Body),
	)
}

// CloseConnectionsByInboundUser drains only the tunnels authenticated as one
// logical residential client. This makes a route switch take effect for future
// bytes without disturbing other sessions sharing the listener port.
func (m *Manager) CloseConnectionsByInboundUser(ctx context.Context, listenerID, username string) error {
	listenerID = strings.TrimSpace(listenerID)
	username = strings.TrimSpace(username)
	if listenerID == "" || username == "" {
		return errors.New("listener id and inbound username are required")
	}
	inboundName := listenerConfigName(listenerID)
	m.mu.Lock()
	m.refreshProcessLocked()
	if !m.isRunningLocked() || !m.status.Running {
		m.mu.Unlock()
		return ErrNotRunning
	}
	socketPath := m.controllerSocket
	m.mu.Unlock()

	client, transport := controllerClient(socketPath, 5*time.Second)
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/connections", nil)
	if err != nil {
		return fmt.Errorf("create Mihomo connections request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("list Mihomo connections: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Mihomo connections returned status %d", response.StatusCode)
	}
	var payload connectionsResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&payload); err != nil {
		return fmt.Errorf("decode Mihomo connections: %w", err)
	}
	for _, connection := range payload.Connections {
		if connection.Metadata.InboundName != inboundName || connection.Metadata.InboundUser != username {
			continue
		}
		closeRequest, err := http.NewRequestWithContext(
			ctx,
			http.MethodDelete,
			"http://unix/connections/"+url.PathEscape(connection.ID),
			nil,
		)
		if err != nil {
			return fmt.Errorf("create Mihomo connection close request: %w", err)
		}
		closeResponse, err := client.Do(closeRequest)
		if err != nil {
			return fmt.Errorf("close Mihomo connection: %w", err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(closeResponse.Body, 1024))
		closeResponse.Body.Close()
		if closeResponse.StatusCode != http.StatusNoContent && closeResponse.StatusCode != http.StatusOK {
			return fmt.Errorf("Mihomo connection close returned status %d", closeResponse.StatusCode)
		}
	}
	return nil
}

// CheckProxyReachable confirms that one proxy can reach the public internet.
//
// The Mihomo delay endpoint reports latency only, not the response body, so this
// verifies egress without revealing the exit address. Reporting the residential
// exit IP itself is done by the control-plane provider probe, which is a
// deliberate one-shot diagnostic rather than part of any traffic path.
func (m *Manager) CheckProxyReachable(ctx context.Context, proxyName string) (int, error) {
	proxyName = strings.TrimSpace(proxyName)
	if proxyName == "" {
		return 0, errors.New("proxy name is required")
	}
	return m.proxyDelay(ctx, proxyName, exitIPProbeURL, 10*time.Second)
}

// exitIPProbeURL is the reachability target used when confirming that a pooled
// residential session can egress at all.
const exitIPProbeURL = "http://cp.cloudflare.com/generate_204"

// controllerMessage extracts a short error message from a controller response.
func controllerMessage(body io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(body, 1024))
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &payload)
	message := strings.TrimSpace(payload.Message)
	if message == "" {
		message = strings.TrimSpace(string(raw))
	}
	if len(message) > 256 {
		message = message[:256]
	}
	return message
}
