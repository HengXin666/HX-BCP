package mihomo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

// compiledSharedListener is one aggregate Mihomo listener plus the record that
// identifies it, so the caller can keep the endpoint and edge-route uniqueness
// checks working unchanged.
type compiledSharedListener struct {
	record store.ListenerRecord
	config map[string]any
	// edgeKey is the public WebSocket route (host + path) this listener serves,
	// or "" when it has no public host configured. It comes from the global
	// settings rather than the row, because the endpoint of a shared WebSocket
	// entry point is deployment-wide.
	edgeKey string
}

// sharedMember is one proxy service inside an aggregate listener. Each member
// keeps its own credential, protocol and proxy group; only the bind address and
// port are shared.
type sharedMember struct {
	username string
	// action is the routing rule's target: a Mihomo proxy-group name for a
	// group-backed member, or a Mihomo proxy name for a listener pinned to one
	// node. See .agents/notes/implemented/feature/2026-09-26-listener-pins-a-node.md.
	// Mihomo resolves a rule target against proxies and proxy-groups
	// alike, so a node name is a legal target; measured on 1.19.30, naming a
	// proxy that does not exist fails the whole configuration with
	// "proxy [x] not found" rather than being ignored, which is what makes a
	// pinned node safe to emit here.
	action string
	// groupID is set for a group-backed member and empty when the listener is
	// pinned to one node. Callers use this instead of parsing the rule text,
	// because a group name may itself contain the "), " separator.
	groupID  string
	kind     string
	uuid     string
	password string
}

// compileSharedInbounds turns the aggregate listeners into Mihomo listener
// definitions.
//
// The control plane keeps one aggregate Mixed listener for every HTTP / SOCKS /
// Mixed service and one aggregate listener per WebSocket protocol for every
// VLESS / VMess / Trojan service. Membership is resolved purely by username:
// the compiler emits one IN-USER rule per member. The caller places node pins
// and groups the YAML document does not name before site rules, so one
// environment cannot take another member's egress.
func (c *Compiler) compileSharedInbounds(
	settings systemsettings.Settings,
	listeners []store.ListenerRecord,
	enabledGroups map[string]store.ProxyGroupRecord,
	nodeNameByID map[string]string,
) ([]compiledSharedListener, []sharedUserRule, error) {
	shared := settings.SharedInbound
	if !shared.Enabled() && !shared.AppliesToWebSocket() {
		return nil, nil, nil
	}

	type aggregate struct {
		record  store.ListenerRecord
		owner   string
		kind    string
		members []sharedMember
	}
	aggregates := make(map[string]*aggregate, 4)
	order := make([]string, 0, 4)
	seenMembers := make(map[string]map[string]struct{}, 4)
	aggregateRows := make(map[string]store.ListenerRecord, 4)
	for _, record := range listeners {
		if !record.Enabled || listener.SharedInboundOwnerOf(record) == "" || !listener.IsSharedInboundAggregate(record) {
			continue
		}
		// Keyed by the published listener, exactly as membership is: the whole
		// standard family shares one Mixed carrier, while each WebSocket
		// protocol keeps its own.
		owner := listener.SharedInboundOwnerOf(record)
		aggregateRows[listener.SharedInboundCarrierKey(owner, record.Kind)] = record
	}

	for _, record := range listeners {
		owner := listener.SharedInboundOwnerOf(record)
		if !record.Enabled || owner == "" || listener.IsSharedInboundAggregate(record) {
			continue
		}
		isWebSocket := isWebSocketListener(record.Kind)
		if isWebSocket {
			if !shared.AppliesToWebSocket() {
				continue
			}
		} else if !shared.Enabled() {
			continue
		}
		// The standard family is multiplexed by Mihomo's Mixed listener, which
		// speaks HTTP proxy and SOCKS5 on the same socket, so an http or socks
		// service needs no listener of its own kind. Dropping those members (as
		// a per-protocol filter used to) removed the service from the data plane
		// while its row still claimed to be published.
		if !listener.IsSharedInboundMemberKind(owner, record.Kind) {
			continue
		}
		group, exists := enabledGroups[record.ProxyGroupID]
		if !exists && strings.TrimSpace(record.NodeID) == "" {
			// A member whose group was disabled cannot carry traffic; the
			// listener stays but publishes nothing. A pinned member does not
			// dereference its group, so it is compiled from the pin alone.
			continue
		}
		member, err := c.sharedMemberFor(record, group, nodeNameByID)
		if err != nil {
			return nil, nil, err
		}
		// Membership is keyed by the carrier that actually publishes the
		// member, not by the member's own protocol: the whole standard family
		// collapses onto one Mixed listener.
		carrierKind := listener.SharedInboundCarrierKind(owner, record.Kind)
		key := listener.SharedInboundCarrierKey(owner, record.Kind)
		if seenMembers[key] == nil {
			seenMembers[key] = make(map[string]struct{})
		}
		// Two services behind one entry point must not share a username: the
		// whole membership model rests on the username selecting exactly one
		// group.
		if _, duplicate := seenMembers[key][member.username]; duplicate {
			return nil, nil, fmt.Errorf("two shared-inbound services for the %s family use the same username %q", owner, member.username)
		}
		seenMembers[key][member.username] = struct{}{}
		entry, exists := aggregates[key]
		if !exists {
			// Prefer the aggregate row so the endpoint and edge-route checks
			// run against the listener that actually binds the port.
			// The synthesised fallback must describe the family's real
			// endpoint, not the member's stored row: members share the carrier,
			// so a member row still carrying an old dedicated port would
			// otherwise be validated and published as if it were the entry
			// point.
			carrier := record
			carrier.Kind = carrierKind
			carrier.BindAddress, carrier.Port = sharedCarrierEndpoint(shared, owner)
			if row, ok := aggregateRows[listener.SharedInboundCarrierKey(owner, record.Kind)]; ok {
				carrier = row
			}
			entry = &aggregate{record: carrier, owner: owner, kind: carrierKind}
			aggregates[key] = entry
			order = append(order, key)
		}
		entry.members = append(entry.members, member)
	}
	sort.Strings(order)

	compiled := make([]compiledSharedListener, 0, len(order))
	rules := make([]sharedUserRule, 0, len(order))
	for _, key := range order {
		entry := aggregates[key]
		sort.Slice(entry.members, func(left, right int) bool {
			return entry.members[left].username < entry.members[right].username
		})
		config, memberRules, err := c.compileSharedListenerConfig(entry.record, entry.owner, entry.kind, entry.members)
		if err != nil {
			return nil, nil, err
		}
		if config == nil {
			continue
		}
		entryCompiled := compiledSharedListener{record: entry.record, config: config}
		if isWebSocketListener(entry.kind) {
			if host := strings.ToLower(strings.TrimSpace(shared.WSPublicHost)); host != "" {
				path, err := listener.NormalizeWebSocketPath(listener.SharedInboundRoutePath())
				if err != nil {
					return nil, nil, err
				}
				entryCompiled.edgeKey = host + "|" + path
			}
		}
		compiled = append(compiled, entryCompiled)
		rules = append(rules, memberRules...)
	}
	return compiled, rules, nil
}

// sharedCarrierEndpoint reports where the aggregate listener of a family binds.
// The standard family is a Mixed listener reachable by a container or a LAN
// client (its bind address is configurable and defaults to 0.0.0.0); the
// WebSocket family stays on loopback because it is reached through the edge
// relay.
func sharedCarrierEndpoint(shared systemsettings.SharedInboundSettings, owner string) (string, int) {
	if owner == listener.SharedInboundWebSocketOwner {
		return "127.0.0.1", shared.WSPort
	}
	return shared.MixedBindAddress, shared.MixedPort
}

func (c *Compiler) sharedMemberFor(
	record store.ListenerRecord,
	group store.ProxyGroupRecord,
	recordedNodes map[string]string,
) (sharedMember, error) {
	if record.AuthMode != "userpass" || len(record.AuthConfigEncrypted) == 0 {
		return sharedMember{}, fmt.Errorf(
			"listener %q has no credentials but the shared inbound routes members by username; enable username/password authentication",
			record.Name,
		)
	}
	if c.cipher == nil {
		return sharedMember{}, fmt.Errorf("listener %q credentials are unavailable", record.Name)
	}
	plaintext, err := c.cipher.Open(record.AuthConfigEncrypted, []byte("listener:"+record.ID))
	if err != nil {
		return sharedMember{}, fmt.Errorf("decrypt listener %q auth: %w", record.Name, err)
	}
	var auth listener.Auth
	if err := json.Unmarshal(plaintext, &auth); err != nil {
		return sharedMember{}, fmt.Errorf("decode listener %q auth: %w", record.Name, err)
	}
	// The client authenticates with the credential the administrator set, so
	// that exact username is what Mihomo must know and what the IN-USER rule
	// must match. Deriving a synthetic name here would make every published
	// subscription unusable.
	// A pinned listener targets the node's compiled proxy name; an unpinned one
	// keeps the historical group target. Resolving the name here — rather than
	// emitting the raw id — is what keeps a rule from pointing at a name that
	// only exists in the control plane. The lookup reads the same records the
	// proxy list is built from, so the two cannot drift.
	// A member whose group is missing or disabled cannot carry traffic and is
	// dropped by the caller, so the group-backed action is always a live name.
	// A pin always resolves: the compiler withholds every listener whose node
	// the document does not carry before it gets here (compiler_pinned.go), and
	// such a listener is never repointed at its group.
	action := group.Name
	groupID := group.ID
	if nodeID := strings.TrimSpace(record.NodeID); nodeID != "" {
		action = recordedNodes[nodeID]
		groupID = ""
	}
	member := sharedMember{
		username: strings.TrimSpace(auth.Username),
		action:   action,
		groupID:  groupID,
		kind:     record.Kind,
	}
	if member.username == "" {
		return sharedMember{}, fmt.Errorf("listener %q has an empty shared-inbound username", record.Name)
	}
	switch record.Kind {
	case "vless", "vmess":
		if !listener.ValidUUID(auth.Password) {
			return sharedMember{}, fmt.Errorf("listener %q must use a UUID credential for %s", record.Name, record.Kind)
		}
		member.uuid = auth.Password
	default:
		if auth.Password == "" {
			return sharedMember{}, fmt.Errorf("listener %q has an empty shared-inbound password", record.Name)
		}
		member.password = auth.Password
	}
	return member, nil
}

func (c *Compiler) compileSharedListenerConfig(
	record store.ListenerRecord,
	owner, kind string,
	members []sharedMember,
) (map[string]any, []sharedUserRule, error) {
	users := make([]map[string]any, 0, len(members))
	rules := make([]sharedUserRule, 0, len(members))
	// The name is derived from the family, never from a row id: the aggregate
	// row and its members are interchangeable carriers of the same listener,
	// and an IN-NAME that moved with an arbitrary row would break the
	// administrator's per-group routing rules.
	name := sharedListenerConfigName(owner, kind)
	for _, member := range members {
		user := map[string]any{"username": member.username}
		switch kind {
		case "vless", "vmess":
			if member.uuid == "" {
				continue
			}
			user["uuid"] = member.uuid
			if kind == "vmess" {
				user["alterId"] = 0
			}
		default:
			if member.password == "" {
				continue
			}
			user["password"] = member.password
		}
		users = append(users, user)
		rules = append(rules, sharedUserRule{
			Text:    "AND,((IN-NAME," + name + "),(IN-USER," + member.username + "))," + member.action,
			GroupID: member.groupID,
		})
	}
	if len(users) == 0 {
		return nil, nil, nil
	}
	config := map[string]any{
		"name":   name,
		"type":   kind,
		"listen": record.BindAddress,
		"port":   record.Port,
		"users":  users,
	}
	if kind == "http" || kind == "socks" || kind == "mixed" {
		config["udp"] = true
	}
	if isWebSocketListener(kind) {
		var transport listener.Transport
		if err := json.Unmarshal([]byte(record.TransportJSON), &transport); err != nil {
			return nil, nil, fmt.Errorf("decode shared listener %q transport: %w", record.Name, err)
		}
		path, err := listener.NormalizeWebSocketPath(transport.WSPath)
		if err != nil {
			return nil, nil, fmt.Errorf("shared listener %q has an invalid WebSocket path: %w", record.Name, err)
		}
		config["ws-path"] = path
		if kind == "vless" || kind == "trojan" {
			config["allow-insecure"] = true
		}
	}
	return config, rules, nil
}

// SharedListenerConfigName is the Mihomo listener name of an aggregate family.
// It is derived from the family (owner + protocol) and not from a database row,
// so the name survives the aggregate row being recreated and the per-group
// IN-NAME routing rules generated by the administrator never dangle.
func SharedListenerConfigName(owner, kind string) string {
	return sharedListenerConfigName(owner, kind)
}

func sharedListenerConfigName(owner, kind string) string {
	return "hx-in-shared-" + strings.ToLower(strings.TrimSpace(owner)) + "-" + strings.ToLower(strings.TrimSpace(kind))
}
