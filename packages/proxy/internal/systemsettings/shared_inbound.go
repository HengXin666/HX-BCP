package systemsettings

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// SharedInboundMode selects how managed services are exposed to clients.
//
// "per_service" is the historical behaviour: every proxy service owns its own
// bound port, so N services cost N listeners and N open ports.
//
// "shared" keeps one ManagedPort for the HTTP / SOCKS5 / Mixed family and one
// ManagedWSPort for the VLESS / VMess / Trojan WebSocket family. Every service
// is distinguished by its username and is routed by an IN-USER rule, so a
// single client process can carry every protocol and every group through two
// URLs instead of one client per port.
type SharedInboundMode string

const (
	SharedInboundPerService SharedInboundMode = "per_service"
	SharedInboundShared     SharedInboundMode = "shared"
)

// Default shared-inbound ports. 7890/7891 are the conventional Mihomo mixed /
// SOCKS ports; the WebSocket port is loopback-only and normally reached
// through the edge reverse proxy.
const (
	DefaultSharedMixedPort = 7890
	DefaultSharedWSPort    = 7891
)

// SharedInboundSettings configures the one-client entry points.
type SharedInboundSettings struct {
	Mode                SharedInboundMode `json:"mode"`
	MixedBindAddress    string            `json:"mixed_bind_address,omitempty"`
	MixedPort           int               `json:"mixed_port,omitempty"`
	WSPort              int               `json:"ws_port,omitempty"`
	MixedPublicHost     string            `json:"mixed_public_host,omitempty"`
	MixedPublicPort     int               `json:"mixed_public_port,omitempty"`
	MixedPublicTLS      bool              `json:"mixed_public_tls,omitempty"`
	WSPublicHost        string            `json:"ws_public_host,omitempty"`
	WSPublicPort        int               `json:"ws_public_port,omitempty"`
	IncludeInPerService bool              `json:"include_in_per_service,omitempty"`
}

// DefaultSharedInbound returns the shared-mode defaults used when no settings
// have been stored yet. An existing installation keeps its dedicated ports:
// Load() marks a stored document that predates this section as per_service.
func DefaultSharedInbound() SharedInboundSettings {
	return SharedInboundSettings{
		Mode:             SharedInboundShared,
		MixedBindAddress: "0.0.0.0",
		MixedPort:        DefaultSharedMixedPort,
		WSPort:           DefaultSharedWSPort,
	}
}

// Enabled reports whether the shared entry points should be compiled.
func (s SharedInboundSettings) Enabled() bool {
	return s.Mode == SharedInboundShared
}

// AppliesToStandard reports whether a standard (HTTP / SOCKS / Mixed) service
// is carried by the shared Mixed listener.
func (s SharedInboundSettings) AppliesToStandard() bool {
	return s.Enabled()
}

// AppliesToWebSocket reports whether a WebSocket protocol service is carried
// by the shared VLESS / VMess / Trojan listeners.
func (s SharedInboundSettings) AppliesToWebSocket() bool {
	return s.Enabled() || s.IncludeInPerService
}

// normalizeSharedInbound fills unset values from the defaults and canonicalizes
// the bind address.
func normalizeSharedInbound(settings *SharedInboundSettings) {
	defaults := DefaultSharedInbound()
	if strings.TrimSpace(settings.MixedBindAddress) == "" {
		settings.MixedBindAddress = defaults.MixedBindAddress
	}
	settings.MixedBindAddress = strings.TrimSpace(settings.MixedBindAddress)
	if settings.MixedPort == 0 {
		settings.MixedPort = defaults.MixedPort
	}
	if settings.WSPort == 0 {
		settings.WSPort = defaults.WSPort
	}
	settings.MixedPublicHost = strings.ToLower(strings.TrimSpace(settings.MixedPublicHost))
	settings.WSPublicHost = strings.ToLower(strings.TrimSpace(settings.WSPublicHost))
	settings.Mode = SharedInboundMode(strings.ToLower(strings.TrimSpace(string(settings.Mode))))
}

// validateSharedInbound rejects configurations the compiler could not turn
// into two valid Mihomo listeners.
func validateSharedInbound(settings SharedInboundSettings) error {
	switch settings.Mode {
	case "", SharedInboundPerService, SharedInboundShared:
	default:
		return fmt.Errorf("%w: shared_inbound.mode must be %q or %q", ErrInvalid, SharedInboundPerService, SharedInboundShared)
	}
	if settings.Mode != SharedInboundShared {
		return nil
	}
	if net.ParseIP(settings.MixedBindAddress) == nil {
		return fmt.Errorf("%w: shared_inbound.mixed_bind_address must be an explicit IP", ErrInvalid)
	}
	if settings.MixedPort < 1 || settings.MixedPort > 65535 {
		return fmt.Errorf("%w: shared_inbound.mixed_port must be between 1 and 65535", ErrInvalid)
	}
	if settings.WSPort < 1 || settings.WSPort > 65535 {
		return fmt.Errorf("%w: shared_inbound.ws_port must be between 1 and 65535", ErrInvalid)
	}
	if settings.MixedPort == settings.WSPort {
		return fmt.Errorf("%w: shared_inbound.mixed_port and ws_port must differ", ErrInvalid)
	}
	if settings.MixedPublicHost != "" && !validEndpointHost(settings.MixedPublicHost) {
		return fmt.Errorf("%w: shared_inbound.mixed_public_host must be an IP address or domain name", ErrInvalid)
	}
	if settings.WSPublicHost != "" && !validEndpointHost(settings.WSPublicHost) {
		return fmt.Errorf("%w: shared_inbound.ws_public_host must be an IP address or domain name", ErrInvalid)
	}
	return nil
}

// SharedInboundMixedEndpoint is the client-facing endpoint of the shared
// standard listener. A loopback bind address is only advertised when the
// administrator configured a public host, mirroring the per-listener rule that
// an internal Mihomo port must never leak into a subscription.
func SharedInboundMixedEndpoint(settings SharedInboundSettings, requestHost string) (host string, port int, tls bool, ok bool) {
	return sharedInboundEndpoint(settings.MixedBindAddress, settings.MixedPort, settings.MixedPublicHost, settings.MixedPublicPort, settings.MixedPublicTLS, requestHost, true)
}

// SharedInboundWebSocketEndpoint is the client-facing endpoint shared by the
// VLESS / VMess / Trojan listeners.
func SharedInboundWebSocketEndpoint(settings SharedInboundSettings, requestHost string) (string, int, bool, bool) {
	return sharedInboundEndpoint("127.0.0.1", settings.WSPort, settings.WSPublicHost, settings.WSPublicPort, true, requestHost, false)
}

func sharedInboundEndpoint(
	bindAddress string,
	bindPort int,
	publicHost string,
	publicPort int,
	publicTLS bool,
	requestHost string,
	allowRequestHost bool,
) (string, int, bool, bool) {
	if publicHost != "" {
		port := publicPort
		if port == 0 {
			if publicTLS {
				port = 443
			} else {
				port = bindPort
			}
		}
		if port < 1 || port > 65535 {
			return "", 0, false, false
		}
		return publicHost, port, publicTLS, true
	}
	if ip := net.ParseIP(strings.TrimSpace(bindAddress)); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
		return ip.String(), bindPort, publicTLS, true
	}
	if !allowRequestHost {
		return "", 0, false, false
	}
	if host, _, err := net.SplitHostPort(requestHost); err == nil && host != "" {
		return host, bindPort, publicTLS, true
	}
	if requestHost != "" {
		return requestHost, bindPort, publicTLS, true
	}
	return "", 0, false, false
}

func validEndpointHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsUnspecified()
	}
	return validSharedHost(host)
}

func validSharedHost(host string) bool {
	if len(host) < 3 || len(host) > 253 || strings.ContainsAny(host, "/:@?# ") || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

// SharedInboundProtocolKinds is the ordered set of WebSocket protocol
// listeners materialized for the shared WebSocket entry point.
func SharedInboundProtocolKinds() []string {
	kinds := []string{"vless", "vmess", "trojan"}
	sort.Strings(kinds)
	return kinds
}
