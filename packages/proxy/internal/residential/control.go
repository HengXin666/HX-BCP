package residential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type ControlNode struct {
	Index       int               `json:"index"`
	NodeName    string            `json:"node_name"`
	Endpoints   []ControlEndpoint `json:"endpoints"`
	ProxyURL    *string           `json:"proxy_url"`
	Hint        string            `json:"hint,omitempty"`
	ExitIP      string            `json:"exit_ip,omitempty"`
	CountryCode string            `json:"country_code,omitempty"`
	RouteMode   string            `json:"route_mode"`
	// AllocVersion is the monotonic allocation counter of this node. It is
	// bumped on every rotation, route switch and (re)allocation; consumers pass
	// it back as expected_alloc_version to detect concurrent rotation.
	AllocVersion int `json:"alloc_version"`
	// Lease is the exclusive-ownership state of this node window. LeaseID is
	// only present in claim/heartbeat responses; the node pool list never
	// renders another consumer's capability token.
	Lease *ControlLease `json:"lease,omitempty"`
	// ResidentialEndpoint is the vendor endpoint assigned to this logical
	// node. It is only rendered by the control-token API; subscription and
	// administrator views use separate DTOs which cannot carry this secret.
	ResidentialEndpoint *ControlResidentialEndpoint `json:"residential_endpoint,omitempty"`
}

// ControlEndpoint describes one standard client-facing entry point for a
// declared node. Consumers can either use a browser-compatible URI directly or
// hand an advanced URI to a local data plane such as Mihomo or sing-box.
type ControlEndpoint struct {
	Protocol          string `json:"protocol"`
	Transport         string `json:"transport"`
	URI               string `json:"uri"`
	BrowserCompatible bool   `json:"browser_compatible"`
}

// ControlResidentialEndpoint is the minimal upstream configuration a trusted
// automation client needs to build its own local data plane. Password is
// intentionally available only behind the channel's high-privilege control
// token and must never be copied into logs or ordinary subscriptions.
type ControlResidentialEndpoint struct {
	Protocol string `json:"protocol"`
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	TLS      bool   `json:"tls"`
}

type ControlNodeList struct {
	Channel string        `json:"channel"`
	Nodes   []ControlNode `json:"nodes"`
}

func (s *Service) ControlNodesByToken(ctx context.Context, token string) (ControlNodeList, error) {
	channel, err := s.controlChannel(ctx, token)
	if err != nil {
		return ControlNodeList{}, err
	}
	residentialEndpoints, err := s.controlResidentialEndpoints(ctx, channel)
	if err != nil {
		return ControlNodeList{}, err
	}
	nodes := make([]ControlNode, 0, channel.SessionCount)
	for index := 1; index <= channel.SessionCount; index++ {
		node, err := s.controlNodeViewWithResidentialEndpoints(ctx, channel, index, residentialEndpoints)
		if err != nil {
			return ControlNodeList{}, err
		}
		if err := s.repository.TouchResidentialClientSession(
			ctx,
			channel.ID,
			declaredSessionID(index),
			s.now().UTC(),
		); err != nil {
			return ControlNodeList{}, err
		}
		nodes = append(nodes, node)
	}
	return ControlNodeList{Channel: channel.Name, Nodes: nodes}, nil
}

func (s *Service) RotateDeclaredSession(
	ctx context.Context,
	channelID string,
	index int,
) (ChannelSession, error) {
	channel, err := s.repository.GetResidentialChannel(ctx, channelID)
	if err != nil {
		return ChannelSession{}, mapStoreError(err)
	}
	if err := validateDeclaredIndex(channel, index); err != nil {
		return ChannelSession{}, err
	}
	if _, err := s.RotateClientSessionByToken(ctx, channel.RotateToken, declaredSessionID(index)); err != nil {
		return ChannelSession{}, err
	}
	record, err := s.repository.GetResidentialClientSession(ctx, channel.ID, declaredSessionID(index))
	if err != nil {
		return ChannelSession{}, mapStoreError(err)
	}
	return declaredSessionView(channel, record, index), nil
}

func (s *Service) RotateDeclaredSessionByControlToken(
	ctx context.Context,
	token string,
	index int,
	options RotateOptions,
) (ControlNode, error) {
	channel, err := s.controlChannel(ctx, token)
	if err != nil {
		return ControlNode{}, err
	}
	if err := validateDeclaredIndex(channel, index); err != nil {
		return ControlNode{}, err
	}
	if _, err := s.rotateClientSessionByToken(ctx, channel.RotateToken, declaredSessionID(index), options); err != nil {
		return ControlNode{}, err
	}
	return s.controlNodeView(ctx, channel, index)
}

func (s *Service) SwitchDeclaredSessionRouteByControlToken(
	ctx context.Context,
	token string,
	index int,
	routeMode string,
	options RotateOptions,
) (ControlNode, error) {
	channel, err := s.controlChannel(ctx, token)
	if err != nil {
		return ControlNode{}, err
	}
	if err := validateDeclaredIndex(channel, index); err != nil {
		return ControlNode{}, err
	}
	if _, err := s.switchClientSessionRouteByToken(
		ctx,
		channel.RotateToken,
		declaredSessionID(index),
		routeMode,
		options,
	); err != nil {
		return ControlNode{}, err
	}
	return s.controlNodeView(ctx, channel, index)
}

func (s *Service) RotateChannelShareToken(ctx context.Context, channelID string) (Channel, error) {
	channel, err := s.repository.GetResidentialChannel(ctx, channelID)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	if _, err := s.listeners.RotateShareToken(ctx, channel.ListenerID); err != nil {
		return Channel{}, err
	}
	return s.GetChannel(ctx, channel.ID)
}

func (s *Service) RotateChannelControlToken(ctx context.Context, channelID string) (Channel, error) {
	channel, err := s.repository.GetResidentialChannel(ctx, channelID)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	token, err := newToken()
	if err != nil {
		return Channel{}, err
	}
	if _, err := s.repository.RotateResidentialChannelControlToken(ctx, channel.ID, token); err != nil {
		return Channel{}, mapStoreError(err)
	}
	return s.GetChannel(ctx, channel.ID)
}

func (s *Service) controlChannel(ctx context.Context, token string) (store.ResidentialChannelRecord, error) {
	token = strings.TrimSpace(token)
	if len(token) < 16 || len(token) > 128 {
		return store.ResidentialChannelRecord{}, ErrNotFound
	}
	channel, err := s.repository.GetResidentialChannelByControlToken(ctx, token)
	if err != nil {
		return store.ResidentialChannelRecord{}, mapStoreError(err)
	}
	if !channel.Enabled || channel.Mode != ModeSticky || channel.SessionCount < 1 {
		return store.ResidentialChannelRecord{}, ErrNotFound
	}
	return channel, nil
}

func validateDeclaredIndex(channel store.ResidentialChannelRecord, index int) error {
	if !channel.Enabled || channel.Mode != ModeSticky || index < 1 || index > channel.SessionCount {
		return ErrNotFound
	}
	return nil
}

func (s *Service) controlNodeView(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
	index int,
) (ControlNode, error) {
	residentialEndpoints, err := s.controlResidentialEndpoints(ctx, channel)
	if err != nil {
		return ControlNode{}, err
	}
	return s.controlNodeViewWithResidentialEndpoints(ctx, channel, index, residentialEndpoints)
}

func (s *Service) controlNodeViewWithResidentialEndpoints(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
	index int,
	residentialEndpoints map[string]ControlResidentialEndpoint,
) (ControlNode, error) {
	if err := validateDeclaredIndex(channel, index); err != nil {
		return ControlNode{}, err
	}
	session, err := s.repository.GetResidentialClientSession(ctx, channel.ID, declaredSessionID(index))
	if err != nil {
		return ControlNode{}, mapStoreError(err)
	}
	node := ControlNode{
		Index:        index,
		NodeName:     DeclaredNodeName(channel.Name, index),
		Endpoints:    []ControlEndpoint{},
		CountryCode:  session.CountryCode,
		RouteMode:    session.RouteMode,
		AllocVersion: session.AllocVersion,
	}
	if leaseActive(session, s.now()) {
		node.Lease = &ControlLease{
			Holder:    session.LeaseHolder,
			ExpiresAt: session.LeaseExpiresAt,
		}
	}
	password, err := s.cipher.Open(
		session.AuthPasswordEncrypted,
		clientSessionAssociatedData(channel.ID, session.SessionID),
	)
	if err != nil {
		return ControlNode{}, fmt.Errorf("decrypt residential control node credential: %w", err)
	}
	node.Endpoints, err = s.controlNodeEndpoints(ctx, channel, index, session.AuthUsername, string(password))
	if err != nil {
		return ControlNode{}, err
	}
	if session.RouteMode == ClientRouteResidential && session.NodeFingerprint != "" {
		if endpoint, exists := residentialEndpoints[session.NodeFingerprint]; exists {
			node.ResidentialEndpoint = &endpoint
		} else if residentialEndpoints != nil {
			return ControlNode{}, errors.New("resolve residential control endpoint: assigned node is missing")
		}
	}
	for _, endpoint := range node.Endpoints {
		if endpoint.BrowserCompatible {
			proxyURL := browserProxyURL(endpoint.URI)
			node.ProxyURL = &proxyURL
			break
		}
	}
	if node.ProxyURL == nil {
		node.Hint = "no browser-compatible endpoint is configured; use an advanced endpoint through a local data plane"
	}
	return node, nil
}

func (s *Service) controlResidentialEndpoints(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
) (map[string]ControlResidentialEndpoint, error) {
	provider, err := s.repository.GetResidentialProvider(ctx, channel.ProviderID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	// Direct endpoint disclosure is intentionally limited to extraction APIs.
	// Session-template gateways derive vendor account credentials and remain on
	// the server-side data plane even for callers holding a control token.
	if provider.RotationMode != RotationAPIList {
		return nil, nil
	}
	nodes, err := s.repository.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil {
		return nil, err
	}
	endpoints := make(map[string]ControlResidentialEndpoint, len(nodes))
	for _, node := range nodes {
		plaintext, err := s.cipher.Open(
			node.CanonicalConfigEncrypted,
			[]byte("node:"+node.Fingerprint),
		)
		if err != nil {
			return nil, fmt.Errorf("decrypt residential control endpoint: %w", err)
		}
		var canonical struct {
			Protocol string `json:"type"`
			Server   string `json:"server"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
			TLS      bool   `json:"tls"`
		}
		if err := json.Unmarshal(plaintext, &canonical); err != nil {
			return nil, fmt.Errorf("decode residential control endpoint: %w", err)
		}
		canonical.Protocol = strings.ToLower(strings.TrimSpace(canonical.Protocol))
		canonical.Server = strings.TrimSpace(canonical.Server)
		if (canonical.Protocol != "http" && canonical.Protocol != "socks5") ||
			validateGatewayHost(canonical.Server) != nil ||
			canonical.Port < 1 || canonical.Port > 65535 {
			return nil, errors.New("decode residential control endpoint: invalid proxy fields")
		}
		endpoints[node.Fingerprint] = ControlResidentialEndpoint{
			Protocol: canonical.Protocol,
			Server:   canonical.Server,
			Port:     canonical.Port,
			Username: canonical.Username,
			Password: canonical.Password,
			TLS:      canonical.TLS,
		}
	}
	return endpoints, nil
}

func browserProxyURL(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	parsed.Fragment = ""
	return parsed.String()
}

func (s *Service) controlNodeEndpoints(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
	index int,
	username string,
	password string,
) ([]ControlEndpoint, error) {
	baseName := DeclaredNodeName(channel.Name, index)
	hasDirect := channel.DirectListenerID != ""
	type source struct {
		listenerID string
		name       string
	}
	sources := []source{{listenerID: channel.ListenerID, name: baseName}}
	if hasDirect {
		sources[0].name += "-ws"
		sources = append(sources, source{listenerID: channel.DirectListenerID, name: baseName + "-direct"})
	}

	endpoints := make([]ControlEndpoint, 0, len(sources)+1)
	for _, source := range sources {
		export, err := s.listeners.ExportWithNodes(ctx, source.listenerID, "", channel.Name, []listener.ShareNode{{
			Name: source.name,
			Auth: &listener.Auth{Username: username, Password: password},
		}})
		if errors.Is(err, listener.ErrShareDisabled) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("export control endpoint: %w", err)
		}
		transport := strings.TrimSpace(export.Transport.Type)
		if transport == "" {
			transport = "tcp"
		}
		for _, uri := range strings.Fields(export.Body) {
			protocol, _, _ := strings.Cut(uri, ":")
			protocol = strings.ToLower(protocol)
			endpoints = append(endpoints, ControlEndpoint{
				Protocol:          protocol,
				Transport:         transport,
				URI:               uri,
				BrowserCompatible: isBrowserProxyProtocol(protocol),
			})
		}
	}
	return endpoints, nil
}

func isBrowserProxyProtocol(protocol string) bool {
	switch protocol {
	case "http", "https", "socks5":
		return true
	default:
		return false
	}
}

func declaredSessionView(
	channel store.ResidentialChannelRecord,
	record store.ResidentialClientSessionRecord,
	index int,
) ChannelSession {
	view := ChannelSession{
		Index:         index,
		SessionID:     record.SessionID,
		NodeName:      DeclaredNodeName(channel.Name, index),
		RouteMode:     record.RouteMode,
		CountryCode:   record.CountryCode,
		Allocated:     record.NodeFingerprint != "",
		AllocatedAt:   record.AllocatedAt,
		ExpiresAt:     record.ExpiresAt,
		RotateCount:   record.RotateCount,
		LastRotatedAt: record.LastRotatedAt,
		LastUsedAt:    record.LastUsedAt,
		AllocVersion:  record.AllocVersion,
	}
	if leaseActive(record, time.Now().UTC()) {
		view.LeaseHolder = record.LeaseHolder
		view.LeaseExpiresAt = record.LeaseExpiresAt
	}
	return view
}
