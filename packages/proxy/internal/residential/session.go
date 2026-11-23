package residential

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// SessionPoolLifetime returns the vendor lifetime represented by one rendered
// residential session. BestProxy's `life` and RapidProxy's `stime` values are
// measured in minutes; the generic provider field uses seconds as its name
// suggests.
func SessionPoolLifetime(provider Provider) time.Duration {
	if provider.SessionTTLSeconds <= 0 || provider.RotationMode == RotationPerRequest {
		return 0
	}
	ttl := provider.SessionTTLSeconds
	if strings.EqualFold(provider.Vendor, "bestproxy") && provider.RotationMode == RotationAPIList {
		// API-list links carry the authoritative BestProxy `life` value. The
		// form field is only a fallback for links without that parameter.
		if parsed, err := url.Parse(provider.APIURL); err == nil {
			if life, err := strconv.Atoi(parsed.Query().Get("life")); err == nil && life > 0 {
				ttl = life
			}
		}
	}
	unit := time.Second
	if strings.EqualFold(provider.Vendor, "bestproxy") || strings.EqualFold(provider.Vendor, "rapidproxy") {
		unit = time.Minute
	}
	return time.Duration(ttl) * unit
}

// SessionPoolRefreshAge leaves a 20% margin before the vendor lifetime. This
// keeps a pool from reaching the expiry boundary while it is idle or while a
// refresh request is waiting behind other data-plane work.
func SessionPoolRefreshAge(provider Provider) time.Duration {
	lifetime := SessionPoolLifetime(provider)
	if lifetime <= 0 {
		return 0
	}
	margins := lifetime / 5
	if margins < time.Second {
		margins = time.Second
	}
	if margins >= lifetime {
		return lifetime
	}
	return lifetime - margins
}

// Credentials are the vendor account secrets. They are stored AEAD-encrypted and
// never returned by the API.
type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Session is one pooled sticky session of a channel.
type Session struct {
	// Index is the stable slot number inside the channel pool. The channel's
	// active_session_index points at one of these.
	Index int `json:"index"`
	// ID is the sticky session identifier sent to the vendor.
	ID string `json:"id"`
	// Server and Port are the direct proxy endpoint for api-list sessions.
	// They are empty for gateway-login sessions, which dial the provider
	// gateway instead.
	Server string `json:"-"`
	Port   int    `json:"-"`
	// Username is the fully rendered gateway username.
	Username string `json:"-"`
	// Password is only populated for API-list endpoints that include their own
	// credentials. Gateway sessions use the provider credential envelope.
	Password string `json:"-"`
	// Canonical carries a complete node configuration for cf-worker sessions
	// (VLESS/Trojan over WebSocket parsed from the Cloudflare Worker panel).
	// When set, canonicalNodeConfig renders it verbatim instead of building a
	// gateway http/socks5 shape.
	Canonical map[string]any `json:"-"`
}

// newSessionID returns an opaque sticky-session identifier. It is 12 hex
// characters (6 random bytes), which stays within the 4-12 alphanumeric session
// id bound that BestProxy and similar residential gateways enforce, and hex
// survives every vendor username dialect without escaping.
func newSessionID() (string, error) {
	var buffer [6]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate residential session id: %w", err)
	}
	return hex.EncodeToString(buffer[:]), nil
}

// buildSessions renders a pool of sticky sessions for one channel.
//
// For per-request gateways there is no session to pin, so exactly one session is
// produced and the pool degenerates to a single upstream.
func buildSessions(provider Provider, credentials Credentials, region string, size int) ([]Session, error) {
	if provider.RotationMode != RotationSessionTemplate || !TemplateUsesSession(provider.UsernameTemplate) {
		username, err := Render(provider.UsernameTemplate, Variables{
			User:    credentials.Username,
			Region:  region,
			Country: region,
			TTL:     strconv.Itoa(provider.SessionTTLSeconds),
		})
		if err != nil {
			return nil, err
		}
		return []Session{{Index: 0, ID: "", Username: username}}, nil
	}
	if size < 1 {
		size = 1
	}
	sessions := make([]Session, 0, size)
	for index := 0; index < size; index++ {
		sessionID, err := newSessionID()
		if err != nil {
			return nil, err
		}
		username, err := Render(provider.UsernameTemplate, Variables{
			User:    credentials.Username,
			Session: sessionID,
			Region:  region,
			Country: region,
			TTL:     strconv.Itoa(provider.SessionTTLSeconds),
		})
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, Session{Index: index, ID: sessionID, Username: username})
	}
	return sessions, nil
}

// canonicalNodeConfig renders one session as a canonical node configuration.
//
// The shape matches what internal/nodeparse produces for subscription nodes so
// the existing Mihomo compiler path converts it without special cases. Upstream
// residential gateways are plain HTTP/SOCKS5 proxies with credentials, so no new
// protocol support is required.
func canonicalNodeConfig(provider Provider, session Session, password, displayName string) map[string]any {
	if len(session.Canonical) > 0 {
		// cf-worker sessions carry a complete VLESS/Trojan WebSocket config
		// fetched from the Cloudflare Worker panel. The Mihomo compiler already
		// understands this canonical shape (query/ws/tls/sni/path), so the
		// control plane only adds the node name and the optional dialer proxy.
		config := cloneCanonical(session.Canonical)
		config["name"] = displayName
		if upstreamGroupID := strings.TrimSpace(provider.UpstreamProxyGroupID); upstreamGroupID != "" {
			config[store.ResidentialDialerProxyGroupIDKey] = upstreamGroupID
		}
		return config
	}
	protocol := provider.Protocol
	server := provider.GatewayHost
	port := provider.GatewayPort
	if session.Server != "" {
		server = session.Server
		port = session.Port
	}
	sessionPassword := password
	if session.Password != "" {
		sessionPassword = session.Password
	}
	config := map[string]any{
		"name":   displayName,
		"type":   protocol,
		"server": server,
		"port":   port,
	}
	if session.Username != "" {
		config["username"] = session.Username
		config["password"] = sessionPassword
	}
	if provider.RotationMode == RotationHXCFWsPxy && session.ID != "" {
		config[store.ResidentialWsPxySessionIDKey] = session.ID
	}
	if protocol == "https" {
		config["type"] = "http"
		config["tls"] = true
	}
	if protocol == "socks5" {
		// Residential gateways commonly proxy UDP as well; Mihomo ignores the
		// hint when the gateway refuses it.
		config["udp"] = true
	}
	if upstreamGroupID := strings.TrimSpace(provider.UpstreamProxyGroupID); upstreamGroupID != "" {
		// This stable id is encrypted with the node and resolved to the current
		// group name only while compiling Mihomo YAML.
		config[store.ResidentialDialerProxyGroupIDKey] = upstreamGroupID
	}
	return config
}

// sessionFingerprint derives the stable node fingerprint for a session.
//
// The channel id participates so two channels of the same provider never share a
// node row, and the display name is excluded so renaming a channel does not
// orphan traffic history. Credentials are hashed, never stored in the clear.
func sessionFingerprint(channelID string, provider Provider, session Session) (string, error) {
	if len(session.Canonical) > 0 {
		// The canonical config identifies the concrete panel endpoint: server,
		// port, credential, and the WebSocket path are all part of it. Hashing
		// the canonical (minus display name) makes every fresh panel refresh
		// produce a new fingerprint, so the data plane actually switches.
		fingerprintInput := make(map[string]any, len(session.Canonical)+1)
		fingerprintInput["channel"] = channelID
		for key, value := range session.Canonical {
			if key != "name" {
				fingerprintInput[key] = value
			}
		}
		encoded, err := json.Marshal(fingerprintInput)
		if err != nil {
			return "", fmt.Errorf("encode cf-worker session fingerprint: %w", err)
		}
		digest := sha256.Sum256(encoded)
		return hex.EncodeToString(digest[:]), nil
	}
	server := provider.GatewayHost
	port := provider.GatewayPort
	if session.Server != "" {
		server = session.Server
		port = session.Port
	}
	payload := map[string]any{
		"channel":  channelID,
		"type":     provider.Protocol,
		"server":   server,
		"port":     port,
		"username": session.Username,
	}
	if provider.RotationMode == RotationHXCFWsPxy && session.ID != "" {
		// Pin the fingerprint to the WsPxy session id, not the CONNECT port.
		// rotate keeps the same port, so hashing host:port would leave the
		// data-plane node looking unchanged after a colo switch.
		payload["wspxy_session"] = session.ID
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode residential session fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// sessionDisplayName builds a human-readable, stably sortable node name. The
// zero-padded index keeps the pool ordering identical between the database sort
// and the rotation index.
func sessionDisplayName(channelName, region string, session Session) string {
	label := channelName
	if region != "" {
		label += " " + region
	}
	return fmt.Sprintf("%s #%02d", label, session.Index+1)
}
