// Package dockeraccess answers "how does a container use this proxy service?"
// and, when the honest answer is "it cannot yet", proposes and applies the
// smallest change that makes it possible.
//
// The previous copy action only validated the service it was handed and then
// reported a dead end for every loopback, WebSocket or credential-less service:
// the operator learned the rule but got no path forward, although the control
// plane already knows everything needed to build one. This package keeps the
// safety rules (no new 0.0.0.0 bind, authentication on anything non-loopback,
// WebSocket protocols stay behind the reverse proxy) and instead offers a
// companion "Docker entry": an authenticated Mixed listener with the same
// egress as the source service, placed either on the existing shared Mixed
// entry (no new port) or on one Docker bridge gateway address. The change is
// shown as a preview and only applied on explicit confirmation.
//
// See .agents/notes/implemented/feature/2026-10-15-docker-access-entry.md.
package dockeraccess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

// State is the stable, machine-readable outcome of an assessment. The UI
// branches on it and on Reason; it never parses a message string.
type State string

const (
	// StateReady: the entry in Assessment.Entry can be dialed from a container.
	StateReady State = "ready"
	// StateNeedsEntry: not directly dialable; Plan describes the fix.
	StateNeedsEntry State = "needs_entry"
	// StateClientLanding: a residential channel; containers use its Mihomo
	// subscription / control URL, which are already actionable.
	StateClientLanding State = "client_landing"
	// StateBlocked: nothing can be done from here; Reason says what the
	// operator has to change first (enable the service, create a bridge).
	StateBlocked State = "blocked"
)

// Reason codes. They are part of the API contract.
const (
	ReasonDirect          = "direct"
	ReasonCompanion       = "companion_entry"
	ReasonServiceDisabled = "service_disabled"
	ReasonResidential     = "residential_channel"
	ReasonWebSocket       = "websocket_protocol"
	ReasonLoopbackBind    = "loopback_bind"
	ReasonMissingAuth     = "missing_auth"
	ReasonNoShareExport   = "no_share_export"
	ReasonNoAddress       = "no_reachable_address"
	ReasonNoBridge        = "no_docker_bridge"
	// ReasonBridgeScanFailed: the host's interfaces could not be read at all,
	// so "no bridge" is unknown, not observed. See
	// .agents/notes/implemented/bug-fix/2026-10-17-control-plane-must-enumerate-interfaces.md.
	ReasonBridgeScanFailed = "bridge_scan_failed"
	ReasonNoFreePort       = "no_free_port"
	ReasonSharedDisabled   = "shared_entry_disabled"
	ReasonEntryDisabled    = "entry_disabled"
	ReasonEntryUnreachable = "entry_unreachable"
	// ReasonEntryPending: the entry is valid and its bridge is up, but the
	// running data plane does not serve it yet (it was withheld while the
	// bridge was missing). The data plane re-applies on its own shortly.
	ReasonEntryPending      = "entry_pending"
	PlacementShared         = "shared"
	PlacementBridge         = "bridge"
	companionNameSuffix     = " · Docker"
	maxListenerNameRunes    = 128
	sharedStandardOwnerName = listener.SharedInboundStandardOwner
)

// SharedEntry is the shared Mixed carrier as configured in global settings.
type SharedEntry struct {
	Enabled bool
	Bind    string
	Port    int
}

// Input is everything Assess needs. It is a plain value so the decision logic
// is unit-testable without a database, a network or a data plane.
type Input struct {
	Source      listener.Listener
	Residential bool
	Shared      SharedEntry
	Bridges     []Bridge
	// BridgeScanFailed is true when the host's interfaces could not be read
	// (for example a sandbox without AF_NETLINK). An empty Bridges list then
	// means "unknown", never "this host has no Docker bridge".
	BridgeScanFailed bool
	// LAN lists non-bridge host addresses, used only to name a dial target for
	// a service that already binds a wildcard address.
	LAN []string
	// Gateway is the bridge address the caller asked for; empty or unknown
	// selects the first bridge.
	Gateway string
	// Entry is the companion listener already linked to Source, if any.
	Entry *listener.Listener
	// EntryWithheld is true when the running data plane leaves Entry out.
	EntryWithheld bool
	// AllocatedPort is the free port the service found for a bridge
	// placement. Zero means none was found.
	AllocatedPort int
	// TakenNames are listener names already in use; the companion name must
	// not collide because listeners.name is UNIQUE.
	TakenNames map[string]struct{}
}

// Endpoint is where a container dials and with which URI schemes.
type Endpoint struct {
	ListenerID string   `json:"listener_id"`
	Kind       string   `json:"kind"`
	SharePath  string   `json:"share_path"`
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Schemes    []string `json:"schemes"`
	Companion  bool     `json:"companion"`
}

// Plan is the preview of the change Apply would make.
type Plan struct {
	ID          string `json:"id"`
	Placement   string `json:"placement"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	BindAddress string `json:"bind_address"`
	Port        int    `json:"port"`
	DialHost    string `json:"dial_host"`
	Gateway     string `json:"gateway"`
	// NewPort is true when the plan opens a port that is not open today.
	NewPort bool `json:"new_port"`
}

// Assessment is the answer for one service.
type Assessment struct {
	ListenerID string    `json:"listener_id"`
	State      State     `json:"state"`
	Reason     string    `json:"reason"`
	Gateway    string    `json:"gateway,omitempty"`
	Bridges    []Bridge  `json:"bridges"`
	Entry      *Endpoint `json:"entry,omitempty"`
	Plan       *Plan     `json:"plan,omitempty"`
	// Revocable is true when a companion entry exists and can be removed.
	Revocable bool `json:"revocable"`
	// ClientLanding is true when a Mihomo-subscription path also exists
	// (WebSocket protocols), so the UI can offer it as the alternative.
	ClientLanding bool `json:"client_landing"`
}

// Assess decides how a container reaches Source.
func Assess(in Input) Assessment {
	bridges := append([]Bridge(nil), in.Bridges...)
	if bridges == nil {
		bridges = []Bridge{}
	}
	result := Assessment{ListenerID: in.Source.ID, Bridges: bridges, Gateway: selectGateway(bridges, in.Gateway)}
	if in.Residential {
		result.State, result.Reason = StateClientLanding, ReasonResidential
		return result
	}
	if !in.Source.Enabled {
		// A companion of a disabled source is not compiled (the data plane
		// gates it), but it must stay revocable from here.
		result.State, result.Reason, result.Revocable = StateBlocked, ReasonServiceDisabled, in.Entry != nil
		return result
	}
	result.ClientLanding = isWebSocketKind(in.Source.Kind)
	if in.Entry != nil {
		result.Revocable = true
		if !in.Entry.Enabled {
			result.State, result.Reason = StateBlocked, ReasonEntryDisabled
			return result
		}
		endpoint, _ := direct(*in.Entry, in.Shared, result.Gateway, in.LAN)
		bridgeMissing := !bridgeStillPresent(*in.Entry, in.Bridges)
		switch {
		case endpoint == nil:
			// The companion was valid when created; a changed shared bind can
			// strand it. Revoke and recreate fixes it.
			result.State, result.Reason = StateBlocked, ReasonEntryUnreachable
			return result
		case bridgeMissing && in.BridgeScanFailed && len(in.Bridges) == 0:
			// Its bind address may still be there: the host link table could
			// not be read at all. Never report a bridge as gone without having
			// looked. See ReasonBridgeScanFailed.
			result.State, result.Reason = StateBlocked, ReasonBridgeScanFailed
			return result
		case bridgeMissing:
			result.State, result.Reason = StateBlocked, ReasonEntryUnreachable
			return result
		}
		if in.EntryWithheld {
			// Never hand out a URI the data plane does not serve yet.
			result.State, result.Reason = StateBlocked, ReasonEntryPending
			return result
		}
		endpoint.Companion = true
		result.State, result.Reason, result.Entry = StateReady, ReasonCompanion, endpoint
		return result
	}
	endpoint, reason := direct(in.Source, in.Shared, result.Gateway, in.LAN)
	if endpoint != nil {
		result.State, result.Reason, result.Entry = StateReady, ReasonDirect, endpoint
		return result
	}
	result.Reason = reason
	plan := planEntry(in, result.Gateway)
	if plan == nil {
		if in.BridgeScanFailed && len(in.Bridges) == 0 {
			// "This host has no Docker bridge" is a claim about the host, and
			// it may only be made after reading the host.
			result.State, result.Reason = StateBlocked, ReasonBridgeScanFailed
			return result
		}
		result.State, result.Reason = StateBlocked, ReasonNoBridge
		if result.Gateway != "" {
			result.Reason = ReasonNoFreePort
		}
		return result
	}
	result.State, result.Plan = StateNeedsEntry, plan
	return result
}

// direct reports whether a container can dial item as-is.
func direct(item listener.Listener, shared SharedEntry, gateway string, lan []string) (*Endpoint, string) {
	if isWebSocketKind(item.Kind) {
		return nil, ReasonWebSocket
	}
	bind, port := item.BindAddress, item.Port
	if item.SharedInbound == sharedStandardOwnerName {
		// A member of a shared entry that is switched off is not compiled at
		// all; its stored bind/port must not be offered as dialable.
		if !shared.Enabled {
			return nil, ReasonSharedDisabled
		}
		bind, port = shared.Bind, shared.Port
	}
	ip := net.ParseIP(strings.TrimSpace(bind))
	if ip == nil || ip.IsLoopback() {
		return nil, ReasonLoopbackBind
	}
	if !item.AuthConfigured {
		return nil, ReasonMissingAuth
	}
	if item.SharePath == "" {
		return nil, ReasonNoShareExport
	}
	host := ip.String()
	if ip.IsUnspecified() {
		host = gateway
		if host == "" && len(lan) > 0 {
			host = lan[0]
		}
		if host == "" {
			return nil, ReasonNoAddress
		}
	}
	return &Endpoint{
		ListenerID: item.ID, Kind: item.Kind, SharePath: item.SharePath,
		Host: host, Port: port, Schemes: schemesFor(item.Kind),
	}, ""
}

// planEntry places a companion Mixed entry. The shared Mixed carrier is
// preferred when it is already reachable (no new port at all); otherwise the
// companion binds exactly one bridge gateway address — never a wildcard.
func planEntry(in Input, gateway string) *Plan {
	name := uniqueName(companionName(in.Source.Name), in.TakenNames)
	carrier := net.ParseIP(strings.TrimSpace(in.Shared.Bind))
	if in.Shared.Enabled && carrier != nil && !carrier.IsLoopback() && in.Shared.Port > 0 {
		dial := carrier.String()
		if carrier.IsUnspecified() {
			dial = gateway
			if dial == "" && len(in.LAN) > 0 {
				dial = in.LAN[0]
			}
		}
		if dial != "" {
			return withID(in.Source, &Plan{
				Placement: PlacementShared, Name: name, Kind: "mixed",
				BindAddress: carrier.String(), Port: in.Shared.Port, DialHost: dial, Gateway: gateway,
			})
		}
	}
	if gateway == "" || in.AllocatedPort == 0 {
		return nil
	}
	return withID(in.Source, &Plan{
		Placement: PlacementBridge, Name: name, Kind: "mixed",
		BindAddress: gateway, Port: in.AllocatedPort, DialHost: gateway, Gateway: gateway, NewPort: true,
	})
}

// withID stamps a plan with a digest of what it would change, so Apply can
// refuse a confirmation made against a preview that no longer holds.
func withID(source listener.Listener, plan *Plan) *Plan {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%s|%s|%d|%s",
		source.ID, source.Version, plan.Placement, plan.Name, plan.BindAddress, plan.Port, plan.DialHost)))
	plan.ID = hex.EncodeToString(digest[:8])
	return plan
}

// bridgeStillPresent reports whether a bridge-bound companion still binds an
// address the host has. A shared-placed companion is judged by direct().
func bridgeStillPresent(entry listener.Listener, bridges []Bridge) bool {
	if entry.SharedInbound != "" {
		return true
	}
	for _, bridge := range bridges {
		if bridge.Address == entry.BindAddress {
			return true
		}
	}
	return false
}

func selectGateway(bridges []Bridge, requested string) string {
	requested = strings.TrimSpace(requested)
	for _, bridge := range bridges {
		if bridge.Address == requested {
			return requested
		}
	}
	if len(bridges) > 0 {
		return bridges[0].Address
	}
	return ""
}

func companionName(source string) string {
	base := []rune(strings.TrimSpace(source))
	limit := maxListenerNameRunes - len([]rune(companionNameSuffix))
	if len(base) > limit {
		base = base[:limit]
	}
	return string(base) + companionNameSuffix
}

func uniqueName(name string, taken map[string]struct{}) string {
	if _, used := taken[name]; !used {
		return name
	}
	for index := 2; ; index++ {
		suffix := fmt.Sprintf(" %d", index)
		runes := []rune(name)
		if len(runes)+len([]rune(suffix)) > maxListenerNameRunes {
			runes = runes[:maxListenerNameRunes-len([]rune(suffix))]
		}
		candidate := string(runes) + suffix
		if _, used := taken[candidate]; !used {
			return candidate
		}
	}
}

func schemesFor(kind string) []string {
	switch kind {
	case "http":
		return []string{"http"}
	case "socks":
		return []string{"socks5h"}
	default:
		return []string{"http", "socks5h"}
	}
}

func isWebSocketKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "vless", "vmess", "trojan":
		return true
	default:
		return false
	}
}
