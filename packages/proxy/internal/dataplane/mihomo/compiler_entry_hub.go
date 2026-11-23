package mihomo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// EntryHubListenerName is the Mihomo listener the unified entry dials into.
const EntryHubListenerName = "hxe-hub"

// EntryHub is where the unified entry reaches the data plane. Port 0 turns the
// hub off and leaves the document exactly as it was before the entry existed.
type EntryHub struct {
	Port   int
	Secret string
}

// EntryNode is one compiled node as the unified entry sees it.
type EntryNode struct {
	ID             string `json:"id"`
	ProxyName      string `json:"proxy_name"`
	HubUser        string `json:"hub_user"`
	DisplayName    string `json:"display_name"`
	Protocol       string `json:"protocol"`
	Server         string `json:"server"`
	Port           int    `json:"port"`
	LifecycleState string `json:"lifecycle_state"`
}

// EntryGroup is one enabled proxy group as the unified entry sees it.
type EntryGroup struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	HubUser string `json:"hub_user"`
}

// EntryCatalog is what the last applied document offers the unified entry.
type EntryCatalog struct {
	// HubError says why the hub is off although it was asked for.
	HubError   string
	HubAddress string
	HubSecret  string
	Nodes      []EntryNode
	Groups     []EntryGroup
}

// SetEntryHub turns the hub listener on or off for later compiles.
func (c *Compiler) SetEntryHub(hub EntryHub) {
	c.entryHub = hub
}

// entryHubUser is the hub username that routes to one node or group.
func entryHubUser(kind, id string) string {
	id = strings.TrimPrefix(strings.TrimPrefix(id, "hx-node-"), "group-")
	if len(id) > 24 {
		id = id[:24]
	}
	return "hxe-" + kind + "-" + id
}

// EntryHubSubRule names the sub-rule set holding every hub route.
const EntryHubSubRule = "hxe-hub"

// compileEntryHub builds the loopback hub listener, one user per node and per
// group, and the sub-rule set that sends each user to its target. Existing
// traffic only meets one SUB-RULE line; unknown hub users hit REJECT so the
// hub never falls through to DIRECT. A hub port already used by a listener
// leaves the hub out and says why in the catalog.
func (c *Compiler) compileEntryHub(
	hub EntryHub,
	nodeRecords []store.NodeConfigRecord,
	nodeByID map[string]compiledNode,
	enabledGroups map[string]store.ProxyGroupRecord,
	endpoints []Endpoint,
) (map[string]any, *entryHubRules, *Endpoint, EntryCatalog) {
	if hub.Port <= 0 || (len(nodeByID) == 0 && len(enabledGroups) == 0) {
		return nil, nil, nil, EntryCatalog{}
	}
	if hub.Port > 65535 || len(hub.Secret) < 16 {
		return nil, nil, nil, EntryCatalog{HubError: "entry hub needs a port and a secret of at least 16 characters"}
	}
	for _, endpoint := range endpoints {
		if endpoint.Port == hub.Port {
			return nil, nil, nil, EntryCatalog{HubError: fmt.Sprintf("entry hub port %d is used by listener %q", hub.Port, endpoint.Name)}
		}
	}
	catalog := EntryCatalog{HubAddress: fmt.Sprintf("127.0.0.1:%d", hub.Port), HubSecret: hub.Secret}
	users := make([]map[string]any, 0, len(nodeByID)+len(enabledGroups))
	routes := make([]string, 0, len(nodeByID)+len(enabledGroups)+1)
	seen := make(map[string]bool, len(nodeByID)+len(enabledGroups))
	add := func(user, target string) bool {
		if seen[user] {
			return false
		}
		seen[user] = true
		users = append(users, map[string]any{"username": user, "password": hub.Secret})
		routes = append(routes, "IN-USER,"+user+","+target)
		return true
	}
	for _, record := range nodeRecords {
		compiled, ok := nodeByID[record.ID]
		if !ok {
			continue
		}
		user := entryHubUser("n", compiled.Name)
		if !add(user, compiled.Name) {
			continue
		}
		catalog.Nodes = append(catalog.Nodes, EntryNode{
			ID:             record.ID,
			ProxyName:      compiled.Name,
			HubUser:        user,
			DisplayName:    record.DisplayName,
			Protocol:       record.Protocol,
			Server:         fmt.Sprint(compiled.Config["server"]),
			Port:           intOf(compiled.Config["port"]),
			LifecycleState: record.LifecycleState,
		})
	}
	groupIDs := make([]string, 0, len(enabledGroups))
	for id := range enabledGroups {
		groupIDs = append(groupIDs, id)
	}
	sort.Strings(groupIDs)
	for _, id := range groupIDs {
		group := enabledGroups[id]
		user := entryHubUser("g", group.ID)
		if !add(user, group.Name) {
			continue
		}
		catalog.Groups = append(catalog.Groups, EntryGroup{ID: group.ID, Name: group.Name, HubUser: user})
	}
	routes = append(routes, "MATCH,REJECT")
	config := map[string]any{
		"name":   EntryHubListenerName,
		"type":   "mixed",
		"listen": "127.0.0.1",
		"port":   hub.Port,
		"users":  users,
		"udp":    false,
	}
	endpoint := &Endpoint{ID: "entry-hub", Name: EntryHubListenerName, Kind: "mixed", BindAddress: "127.0.0.1", Port: hub.Port}
	rules := &entryHubRules{
		dispatch: "SUB-RULE,(IN-NAME," + EntryHubListenerName + ")," + EntryHubSubRule,
		routes:   routes,
	}
	return config, rules, endpoint, catalog
}

// entryHubRules is the one top-level dispatch line and the sub-rule set.
type entryHubRules struct {
	dispatch string
	routes   []string
}

// prepend puts the hub dispatch ahead of rules; nil adds nothing.
func (r *entryHubRules) prepend(rules []string) []string {
	if r == nil {
		return rules
	}
	return append([]string{r.dispatch}, rules...)
}

// attach adds the hub sub-rule set to a document; nil adds nothing.
func (r *entryHubRules) attach(document map[string]any) {
	if r == nil {
		return
	}
	document["sub-rules"] = map[string]any{EntryHubSubRule: r.routes}
}

// CompileWithoutEntryHub compiles the document with the hub left out, the
// fallback when a document with the hub cannot be applied.
func (c *Compiler) CompileWithoutEntryHub(ctx context.Context) (Compiled, error) {
	return c.compile(ctx, EntryHub{})
}

// intOf reads a YAML number that may arrive as any integer or float type.
func intOf(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case uint16:
		return int(typed)
	default:
		var parsed int
		_, _ = fmt.Sscan(fmt.Sprint(value), &parsed)
		return parsed
	}
}

// applyWithoutEntryHubLocked applies the document again with the hub left out
// after the document with the hub failed, so the unified entry can never stop
// the existing listeners from updating. The catalog carries the first error.
func (m *Manager) applyWithoutEntryHubLocked(ctx context.Context, cause error) (Compiled, error) {
	compiled, err := m.compiler.CompileWithoutEntryHub(ctx)
	if err != nil {
		m.recordFailureLocked(err)
		return Compiled{}, err
	}
	if err := m.applyCompiledLocked(ctx, compiled); err != nil {
		return Compiled{}, err
	}
	m.logger.Warn("entry hub left out of the data plane", "component", "dataplane", "error", cause)
	compiled.Entry.HubError = cause.Error()
	return compiled, nil
}
