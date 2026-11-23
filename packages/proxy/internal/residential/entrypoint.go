package residential

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

const (
	residentialInternalPortStart = 32000
	residentialInternalPortEnd   = 41999
)

// managedChannelListener creates the private WebSocket entry point used by new
// clients. Its port, bootstrap credential and Edge Relay path are internal.
func (s *Service) managedChannelListener(
	ctx context.Context,
	channelID string,
	protocol string,
) (ChannelListenerRequest, error) {
	records, err := s.repository.ListListeners(ctx)
	if err != nil {
		return ChannelListenerRequest{}, err
	}
	used := make(map[int]struct{}, len(records))
	for _, record := range records {
		used[record.Port] = struct{}{}
	}
	port := 0
	for candidate := residentialInternalPortStart; candidate <= residentialInternalPortEnd; candidate++ {
		if _, exists := used[candidate]; !exists {
			port = candidate
			break
		}
	}
	if port == 0 {
		return ChannelListenerRequest{}, fmt.Errorf("%w: no internal residential listener ports are available", ErrConflict)
	}
	uuid, err := newUUIDCredential()
	if err != nil {
		return ChannelListenerRequest{}, err
	}
	return ChannelListenerRequest{
		Kind:        protocol,
		BindAddress: "127.0.0.1",
		Port:        port,
		Auth:        &listener.Auth{Username: "hx-bootstrap", Password: uuid},
		Transport: listener.Transport{
			Type:   "ws",
			WSPath: listener.WebSocketPathPrefix + "residential/" + channelID,
		},
	}, nil
}

func normalizeManagedChannelProtocol(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "vless", nil
	}
	if !isResidentialWebSocketKind(value) {
		return "", fmt.Errorf("%w: protocol must be vless, vmess, or trojan", ErrInvalid)
	}
	return value, nil
}

func newUUIDCredential() (string, error) {
	raw, err := newToken()
	if err != nil {
		return "", err
	}
	return raw[:8] + "-" + raw[8:12] + "-4" + raw[13:16] + "-a" + raw[17:20] + "-" + raw[20:], nil
}

// residentialBindAddress constrains the reverse-proxied WebSocket entry point
// to loopback. That listener is an internal implementation detail reached only
// through the control plane's edge relay.
func residentialBindAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "127.0.0.1", nil
	}
	ip := net.ParseIP(value)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("%w: residential listeners must bind to a loopback address", ErrInvalid)
	}
	return ip.String(), nil
}

func isResidentialWebSocketKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "vless", "vmess", "trojan":
		return true
	default:
		return false
	}
}

func validateResidentialWebSocketEndpoint(endpoint listener.PublicEndpoint) error {
	if strings.TrimSpace(endpoint.Host) == "" {
		return nil
	}
	if endpoint.Port != 0 && endpoint.Port != 443 {
		return fmt.Errorf("%w: residential WebSocket public endpoint must use HTTPS port 443", ErrInvalid)
	}
	if !endpoint.TLS {
		return fmt.Errorf("%w: residential WebSocket public endpoint must use TLS", ErrInvalid)
	}
	return nil
}
