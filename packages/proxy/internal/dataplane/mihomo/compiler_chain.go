package mihomo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// derivedProxySeparator joins a node's proxy name with the id of the group that
// chains it. Operator group names may not contain it (the proxygroup service
// rejects it), so a derived name can never collide with a plain node or group.
const derivedProxySeparator = "|via|"

// derivedProxySet collects the per-member proxies that express a group-level
// egress chain.
//
// Mihomo refuses dialer-proxy on a proxy-GROUP and only logs an error before
// ignoring it, which would silently turn every chain into a direct connection.
// The chain is therefore expressed per member node instead: a copy of the node
// carrying "dialer-proxy: <the dialer group's name>".
//
// One hop is enough at this level. The dialer value is a GROUP name, and a
// chained group resolves to its own derived members, so arbitrary depth composes
// through group references rather than through name concatenation. Verified end
// to end through three hops; see .agents/notes/implemented/architecture/
// 2026-09-16-mihomo-dialer-proxy-is-node-level.md.
type derivedProxySet struct {
	proxies []map[string]any
	names   map[string]struct{}
}

func newDerivedProxySet() *derivedProxySet {
	return &derivedProxySet{names: make(map[string]struct{})}
}

// derive records a copy of the node that egresses through dialerName and returns
// the name the chaining group must reference.
func (d *derivedProxySet) derive(node compiledNode, groupID, groupName, dialerName string) (string, error) {
	// A node can only have one egress path. The residential provider bakes a
	// dialer into a node's encrypted config; a group chain is a second one.
	// Refusing is deliberate: honouring the group silently would drop the
	// provider hop and change where that channel's traffic actually exits.
	if existing, ok := node.Config["dialer-proxy"]; ok && strings.TrimSpace(fmt.Sprint(existing)) != "" {
		return "", fmt.Errorf(
			"proxy group %q chains through %q, but node %s already dials through %q; a node cannot have two egress paths",
			groupName, dialerName, node.Name, fmt.Sprint(existing))
	}
	name := node.Name + derivedProxySeparator + groupID
	if _, exists := d.names[name]; exists {
		return name, nil
	}
	config := make(map[string]any, len(node.Config)+1)
	for key, value := range node.Config {
		config[key] = value
	}
	config["name"] = name
	config["dialer-proxy"] = dialerName
	d.proxies = append(d.proxies, config)
	d.names[name] = struct{}{}
	return name, nil
}

// merged appends the derived proxies to the base list, sorted by name so the
// compiled document stays byte-identical across recompiles regardless of group
// iteration order.
func (d *derivedProxySet) merged(base []map[string]any) []map[string]any {
	if len(d.proxies) == 0 {
		return base
	}
	merged := make([]map[string]any, 0, len(base)+len(d.proxies))
	merged = append(merged, base...)
	merged = append(merged, d.proxies...)
	sort.Slice(merged, func(left, right int) bool {
		return fmt.Sprint(merged[left]["name"]) < fmt.Sprint(merged[right]["name"])
	})
	return merged
}

// validateGroupDialerChains is the compile-time backstop for egress chaining.
//
// The proxygroup service validates writes, but group rows are also produced by
// migrations and residential materialization, and Mihomo accepts a bad dialer at
// load time (a cyclic one) or ignores it (a group-level one). A chain that
// cannot be honoured must fail the compile loudly rather than silently degrade
// to a direct connection, which is what the data plane would otherwise do.
func validateGroupDialerChains(groups []store.ProxyGroupRecord) error {
	enabled := make(map[string]store.ProxyGroupRecord, len(groups))
	for _, group := range groups {
		if group.Enabled {
			enabled[group.ID] = group
		}
	}
	dialerOf := make(map[string]string, len(enabled))
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		dialer := strings.TrimSpace(group.DialerProxyGroupID)
		if dialer == "" {
			continue
		}
		if dialer == group.ID {
			return fmt.Errorf("proxy group %q dials through itself", group.Name)
		}
		target, exists := enabled[dialer]
		if !exists {
			return fmt.Errorf("proxy group %q dials through a group that is missing or disabled", group.Name)
		}
		var spec proxygroup.SourceSpec
		if err := json.Unmarshal([]byte(group.SourceSpecJSON), &spec); err == nil && spec.IncludeDirect {
			return fmt.Errorf("proxy group %q chains through %q and also includes DIRECT, which would bypass the chain", group.Name, target.Name)
		}
		dialerOf[group.ID] = target.ID
	}
	// Membership and dialer edges form one graph: a group referenced as a member
	// by the group it dials through closes a loop that Mihomo will not catch.
	edges := make(map[string][]string, len(enabled))
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		var spec proxygroup.SourceSpec
		_ = json.Unmarshal([]byte(group.SourceSpecJSON), &spec)
		next := make([]string, 0, len(spec.GroupIDs)+1)
		for _, id := range spec.GroupIDs {
			if _, exists := enabled[id]; exists {
				next = append(next, id)
			}
		}
		if dialer := dialerOf[group.ID]; dialer != "" {
			next = append(next, dialer)
		}
		edges[group.ID] = next
	}
	const (
		visiting = 1
		done     = 2
	)
	state := make(map[string]int, len(edges))
	var walk func(id string) error
	walk = func(id string) error {
		state[id] = visiting
		for _, next := range edges[id] {
			switch state[next] {
			case visiting:
				name := groupNameOrID(enabled, next)
				return fmt.Errorf("proxy groups form an egress cycle through %q", name)
			case done:
				continue
			default:
				if err := walk(next); err != nil {
					return err
				}
			}
		}
		state[id] = done
		return nil
	}
	// Iterate the stored order, not the map, so the reported cycle is stable.
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		if state[group.ID] == 0 {
			if err := walk(group.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func groupNameOrID(enabled map[string]store.ProxyGroupRecord, id string) string {
	if record, exists := enabled[id]; exists {
		return record.Name
	}
	return id
}
