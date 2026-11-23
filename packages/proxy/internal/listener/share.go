package listener

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
	"gopkg.in/yaml.v3"
)

// ErrShareDisabled marks share exports rejected because the listener is
// disabled; the API maps it onto 404 to avoid leaking listener existence.
var ErrShareDisabled = errors.New("listener share link is disabled")

// ShareNode is one published proxy inside a subscription. A plain listener
// exports exactly one; a residential channel exports one per declared session,
// all sharing the same endpoint but carrying independent credentials.
type ShareNode struct {
	// Name is the node name a client displays. It must stay stable across
	// rotations so a subscription does not have to be re-fetched.
	Name string
	Auth *Auth
}

// ShareExport is the rendered subscription payload for one listener.
type ShareExport struct {
	// Body is the plain URI list, one proxy URI per line.
	Body string
	// FileName is a suggested download name.
	FileName string
	Name     string
	Kind     string
	Host     string
	Port     int
	// Nodes is the published node list, in stable order.
	Nodes     []ShareNode
	Transport Transport
	Endpoint  PublicEndpoint
}

// Auth reports the first node's credentials. It preserves the pre-0.2.0
// single-node accessor used by callers that only ever publish one proxy.
func (export ShareExport) Auth() *Auth {
	if len(export.Nodes) == 0 {
		return nil
	}
	return export.Nodes[0].Auth
}

func newShareToken() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate listener share token: %w", err)
	}
	return hex.EncodeToString(buffer[:]), nil
}

// ExportByShareToken renders the subscription body for the enabled listener
// owning the token. requestHost is the host (optionally host:port) the client
// used to reach the control plane. An explicit public endpoint takes priority
// over the request host when the data-plane listener is bound to loopback.
func (s *Service) ExportByShareToken(ctx context.Context, token, requestHost string) (ShareExport, error) {
	token = strings.TrimSpace(token)
	if len(token) < 16 || len(token) > 64 {
		return ShareExport{}, ErrNotFound
	}
	record, err := s.repository.GetListenerByShareToken(ctx, token)
	if err != nil {
		return ShareExport{}, mapStoreError(err)
	}
	return s.exportRecord(record, requestHost, nil)
}

// ExportByID renders an enabled listener for its token-scoped client subscription.
func (s *Service) ExportByID(ctx context.Context, id, requestHost string) (ShareExport, error) {
	record, err := s.repository.GetListener(ctx, strings.TrimSpace(id))
	if err != nil {
		return ShareExport{}, mapStoreError(err)
	}
	return s.exportRecord(record, requestHost, nil)
}

// ExportWithNodes renders an enabled listener with caller-supplied credentials.
// Residential channels use it to publish stable declared sessions without
// exposing the listener's provisioning credential.
func (s *Service) ExportWithNodes(
	ctx context.Context,
	id, requestHost, name string,
	nodes []ShareNode,
) (ShareExport, error) {
	record, err := s.repository.GetListener(ctx, strings.TrimSpace(id))
	if err != nil {
		return ShareExport{}, mapStoreError(err)
	}
	if strings.TrimSpace(name) != "" {
		record.Name = strings.TrimSpace(name)
	}
	return s.exportRecord(record, requestHost, nodes)
}

func (s *Service) exportRecord(
	record store.ListenerRecord,
	requestHost string,
	nodes []ShareNode,
) (ShareExport, error) {
	if !record.Enabled {
		return ShareExport{}, ErrShareDisabled
	}
	if nodes == nil {
		var auth *Auth
		if record.AuthMode == "userpass" && len(record.AuthConfigEncrypted) > 0 {
			plaintext, err := s.cipher.Open(record.AuthConfigEncrypted, associatedData(record.ID))
			if err != nil {
				return ShareExport{}, fmt.Errorf("decrypt listener %q auth: %w", record.Name, err)
			}
			decoded := Auth{}
			if err := json.Unmarshal(plaintext, &decoded); err != nil {
				return ShareExport{}, fmt.Errorf("decode listener %q auth: %w", record.Name, err)
			}
			auth = &decoded
		}
		nodes = []ShareNode{{Name: record.Name, Auth: auth}}
	}
	if len(nodes) == 0 {
		return ShareExport{}, ErrShareDisabled
	}
	var transport Transport
	var endpoint PublicEndpoint
	_ = json.Unmarshal([]byte(record.TransportJSON), &transport)
	_ = json.Unmarshal([]byte(record.PublicEndpointJSON), &endpoint)
	if isAdvancedKind(record.Kind) {
		normalizedPath, err := NormalizeWebSocketPath(transport.WSPath)
		if err != nil {
			return ShareExport{}, err
		}
		transport.WSPath = normalizedPath
	}
	// A member of a shared inbound publishes the aggregate entry point: its own
	// recorded bind address is the internal Mihomo port and must never leak
	// into a subscription.
	if owner := SharedInboundOwnerOf(record); owner != "" && s.sharedEndpoints != nil {
		spec, ok := s.sharedEndpoints()
		if !ok {
			return ShareExport{}, ErrShareDisabled
		}
		aggregateEndpoint, sharedHost, sharedPort, reachable := ResolveSharedEndpoint(spec, record, requestHost)
		if !reachable {
			return ShareExport{}, ErrShareDisabled
		}
		endpoint = aggregateEndpoint
		return NewShareExport(record.Name, record.Kind, sharedHost, sharedPort, nodes, transport, endpoint), nil
	}
	host := exportHost(record.BindAddress, requestHost)
	port := record.Port
	if endpoint.Host == "" && isLoopbackBind(record.BindAddress) && !isLoopbackHost(requestHost) {
		// A loopback-only listener is reachable by the machine that runs it and
		// nobody else, so it may only be advertised back to a caller that also
		// arrived over loopback (a local CLI or local service). Anything else —
		// including a reverse proxy that forwarded a public Host header — must
		// not turn an internal Mihomo port into an externally advertised
		// subscription.
		return ShareExport{}, ErrShareDisabled
	}
	if endpoint.Host != "" {
		host = endpoint.Host
		if endpoint.Port > 0 {
			port = endpoint.Port
		}
	}
	if isAdvancedKind(record.Kind) {
		host = endpoint.Host
		port = endpoint.Port
	}
	if port < 1 || port > 65535 {
		return ShareExport{}, fmt.Errorf("%w: listener export port must be between 1 and 65535", ErrInvalid)
	}
	return NewShareExport(record.Name, record.Kind, host, port, nodes, transport, endpoint), nil
}

// NewShareExport renders the URI body for a node list. Every renderer walks the
// same list, so a residential channel and a plain listener cannot drift into
// two different subscription formats.
func NewShareExport(
	name, kind, host string,
	port int,
	nodes []ShareNode,
	transport Transport,
	endpoint PublicEndpoint,
) ShareExport {
	uris := make([]string, 0, len(nodes))
	for _, node := range nodes {
		uris = append(uris, shareURIs(kind, node.Name, host, port, node.Auth, transport, endpoint)...)
	}
	return ShareExport{
		Body:     strings.Join(uris, "\n") + "\n",
		FileName: sanitizeFileName(name) + ".txt",
		Name:     name, Kind: kind, Host: host, Port: port,
		Nodes: nodes, Transport: transport, Endpoint: endpoint,
	}
}

func isLoopbackBind(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	return ip != nil && ip.IsLoopback()
}

// isLoopbackHost reports whether a request host (with or without a port) names
// the loopback interface. An empty host is not loopback: callers that cannot
// establish the client's host must keep the conservative refusal.
func isLoopbackHost(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = value
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// EncodeSubscription renders the conventional base64 subscription body used
// by most proxy clients.
func (export ShareExport) EncodeSubscription() string {
	return base64.StdEncoding.EncodeToString([]byte(export.Body))
}

// Render returns a client-specific subscription document. v2rayn remains the
// default because it accepts the conventional base64-wrapped URI list.
func (export ShareExport) Render(format string) (body, fileName, contentType string, err error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "v2rayn":
		return export.EncodeSubscription(), sanitizeFileName(export.Name) + ".txt", "text/plain; charset=utf-8", nil
	case "uri":
		return export.Body, sanitizeFileName(export.Name) + ".txt", "text/plain; charset=utf-8", nil
	case "clash", "mihomo":
		groupName := "HX-PROXY"
		proxies := make([]map[string]any, 0, len(export.Nodes))
		names := make([]string, 0, len(export.Nodes)+1)
		for _, node := range export.Nodes {
			if node.Name == groupName {
				groupName = "HX-PROXY-GROUP"
			}
			proxies = append(proxies, export.clashProxy(node))
			names = append(names, node.Name)
		}
		encoded, encodeErr := yaml.Marshal(map[string]any{
			"mode":      "rule",
			"log-level": "info",
			"allow-lan": false,
			"proxies":   proxies,
			"proxy-groups": []map[string]any{{
				"name": groupName, "type": "select", "proxies": append(names, "DIRECT"),
			}},
			"rules": []string{"MATCH," + groupName},
		})
		if encodeErr != nil {
			return "", "", "", fmt.Errorf("encode Clash subscription: %w", encodeErr)
		}
		return string(encoded), sanitizeFileName(export.Name) + ".yaml", "application/yaml; charset=utf-8", nil
	case "sing-box", "singbox":
		outbounds := make([]map[string]any, 0, len(export.Nodes))
		for _, node := range export.Nodes {
			outbounds = append(outbounds, export.singBoxOutbound(node))
		}
		encoded, encodeErr := json.MarshalIndent(map[string]any{"outbounds": outbounds}, "", "  ")
		if encodeErr != nil {
			return "", "", "", fmt.Errorf("encode sing-box subscription: %w", encodeErr)
		}
		return string(encoded) + "\n", sanitizeFileName(export.Name) + ".json", "application/json; charset=utf-8", nil
	default:
		return "", "", "", fmt.Errorf("%w: format must be v2rayn, clash, sing-box, or uri", ErrInvalid)
	}
}

func exportHost(bindAddress, requestHost string) string {
	ip := net.ParseIP(bindAddress)
	if ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
		return bindAddress
	}
	if host, _, err := net.SplitHostPort(requestHost); err == nil && host != "" {
		return host
	}
	if requestHost != "" {
		return requestHost
	}
	return bindAddress
}

func shareURIs(kind, name, host string, port int, auth *Auth, transport Transport, endpoint PublicEndpoint) []string {
	if kind == "vmess" {
		payload := map[string]any{
			"v": "2", "ps": name, "add": host, "port": strconv.Itoa(port),
			"id": authPassword(auth), "aid": "0", "scy": "auto", "net": "ws",
			"type": "none", "host": endpoint.Host, "path": transport.WSPath, "tls": "tls", "sni": endpoint.Host,
		}
		encoded, _ := json.Marshal(payload)
		return []string{"vmess://" + base64.RawStdEncoding.EncodeToString(encoded)}
	}
	var schemes []string
	switch kind {
	case "http":
		if endpoint.TLS {
			schemes = []string{"https"}
		} else {
			schemes = []string{"http"}
		}
	case "socks":
		schemes = []string{"socks5"}
	case "vless", "trojan":
		schemes = []string{kind}
	default: // mixed exposes both entry protocols on one port
		httpScheme := "http"
		if endpoint.TLS {
			httpScheme = "https"
		}
		schemes = []string{httpScheme, "socks5"}
	}
	uris := make([]string, 0, len(schemes))
	for _, scheme := range schemes {
		address := url.URL{
			Scheme:   scheme,
			Host:     net.JoinHostPort(host, strconv.Itoa(port)),
			Fragment: name,
		}
		if auth != nil {
			if kind == "vless" || kind == "trojan" {
				address.User = url.User(auth.Password)
			} else {
				address.User = url.UserPassword(auth.Username, auth.Password)
			}
		}
		if kind == "vless" || kind == "trojan" {
			query := url.Values{"security": {"tls"}, "type": {"ws"}, "host": {endpoint.Host}, "path": {transport.WSPath}, "sni": {endpoint.Host}}
			if kind == "vless" {
				query.Set("encryption", "none")
			}
			address.RawQuery = query.Encode()
		}
		uris = append(uris, address.String())
	}
	return uris
}

func (export ShareExport) clashProxy(node ShareNode) map[string]any {
	proxy := map[string]any{"name": node.Name, "type": export.Kind, "server": export.Host, "port": export.Port}
	switch export.Kind {
	case "http", "socks":
		if export.Kind == "http" && export.Endpoint.TLS {
			proxy["tls"] = true
		}
		if export.Kind == "socks" {
			proxy["type"] = "socks5"
		}
		addUserPassword(proxy, node.Auth)
	case "mixed":
		proxy["type"] = "http"
		if export.Endpoint.TLS {
			proxy["tls"] = true
		}
		addUserPassword(proxy, node.Auth)
	case "vless", "vmess", "trojan":
		proxy["tls"] = true
		proxy["network"] = "ws"
		proxy["server-name"] = export.Endpoint.Host
		proxy["ws-opts"] = map[string]any{"path": export.Transport.WSPath, "headers": map[string]string{"Host": export.Endpoint.Host}}
		if export.Kind == "trojan" {
			proxy["password"] = authPassword(node.Auth)
		} else {
			proxy["uuid"] = authPassword(node.Auth)
		}
		if export.Kind == "vless" {
			proxy["udp"] = true
		}
	}
	return proxy
}

func (export ShareExport) singBoxOutbound(node ShareNode) map[string]any {
	outbound := map[string]any{"type": export.Kind, "tag": node.Name, "server": export.Host, "server_port": export.Port}
	switch export.Kind {
	case "http", "socks", "mixed":
		if export.Kind == "mixed" {
			outbound["type"] = "http"
		}
		if export.Kind == "socks" {
			outbound["type"] = "socks"
		}
		addUserPassword(outbound, node.Auth)
	case "vless", "vmess", "trojan":
		outbound["tls"] = map[string]any{"enabled": true, "server_name": export.Endpoint.Host}
		outbound["transport"] = map[string]any{"type": "ws", "path": export.Transport.WSPath, "headers": map[string]string{"Host": export.Endpoint.Host}}
		if export.Kind == "trojan" {
			outbound["password"] = authPassword(node.Auth)
		} else {
			outbound["uuid"] = authPassword(node.Auth)
		}
		if export.Kind == "vmess" {
			outbound["security"] = "auto"
			outbound["alter_id"] = 0
		}
	}
	return outbound
}

func addUserPassword(target map[string]any, auth *Auth) {
	if auth == nil {
		return
	}
	target["username"] = auth.Username
	target["password"] = auth.Password
}

func authPassword(auth *Auth) string {
	if auth == nil {
		return ""
	}
	return auth.Password
}

func sanitizeFileName(name string) string {
	var builder strings.Builder
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_':
			builder.WriteRune(character)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return "listener"
	}
	return builder.String()
}

// RotateShareToken invalidates the current share link and returns the
// listener carrying the replacement token.
func (s *Service) RotateShareToken(ctx context.Context, id string) (Listener, error) {
	token, err := newShareToken()
	if err != nil {
		return Listener{}, err
	}
	record, err := s.repository.RotateListenerShareToken(ctx, id, token)
	if err != nil {
		return Listener{}, mapStoreError(err)
	}
	return fromRecord(record), nil
}
