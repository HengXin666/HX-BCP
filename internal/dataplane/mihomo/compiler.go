package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/routingrules"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
	"gopkg.in/yaml.v3"
)

type Repository interface {
	ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error)
	ListListeners(context.Context) ([]store.ListenerRecord, error)
	ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error)
	ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error)
	ListResidentialClientRoutes(context.Context) ([]store.ResidentialClientRouteRecord, error)
	ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error)
	GetMetadata(context.Context, string) (string, error)
}

type Cipher interface {
	Open([]byte, []byte) ([]byte, error)
}

type Endpoint struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	BindAddress string `json:"bind_address"`
	Port        int    `json:"port"`
}

type Compiled struct {
	TUNDevice        string
	YAML             []byte
	Endpoints        []Endpoint
	ProxyCount       int
	ControllerSocket string
	// GroupMembers is group id -> fingerprints of the nodes each enabled
	// group carries in this document. It is recorded after a successful
	// apply and feeds the Retain stage of the next compile.
	GroupMembers map[string][]string
	// WithheldEntries are enabled listeners left out of this document: Docker
	// companions that cannot serve (see compiler_docker_entries.go) and
	// listeners pinned to a node the document no longer carries (see
	// compiler_pinned.go).
	WithheldEntries []WithheldEntry
}

type Compiler struct {
	repository       Repository
	cipher           Cipher
	controllerSocket string
	egressInterface  string
	now              func() time.Time
	// hostAddressPresent is replaced in tests; nil checks host interfaces.
	hostAddressPresent func(string) bool
	// pendingMembers is the membership of the last applied document when
	// recording it failed; it overrides stored history until a write succeeds.
	pendingMembers *pendingMembership
}

func NewCompiler(repository Repository, cipher Cipher) (*Compiler, error) {
	if repository == nil {
		return nil, errors.New("mihomo compiler repository is required")
	}
	if cipher == nil {
		return nil, errors.New("mihomo compiler cipher is required")
	}
	return &Compiler{repository: repository, cipher: cipher}, nil
}

func (c *Compiler) setControllerSocket(path string) {
	c.controllerSocket = strings.TrimSpace(path)
}

func (c *Compiler) setEgressInterface(name string) {
	c.egressInterface = strings.TrimSpace(name)
}

func (c *Compiler) Compile(ctx context.Context) (Compiled, error) {
	settings, err := systemsettings.Load(ctx, c.repository)
	if err != nil {
		return Compiled{}, fmt.Errorf("load global settings: %w", err)
	}
	routeConfig, err := routingrules.Load(ctx, c.repository)
	if err != nil {
		return Compiled{}, fmt.Errorf("load routing rules: %w", err)
	}
	groups, err := c.repository.ListProxyGroups(ctx)
	if err != nil {
		return Compiled{}, err
	}
	listeners, err := c.repository.ListListeners(ctx)
	if err != nil {
		return Compiled{}, err
	}
	listeners, withheldEntries, err := c.gateDockerEntries(ctx, listeners)
	if err != nil {
		return Compiled{}, err
	}
	enabledGroups := make(map[string]store.ProxyGroupRecord)
	groupNames := make(map[string]string)
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		if _, exists := groupNames[group.Name]; exists {
			return Compiled{}, fmt.Errorf("duplicate enabled proxy group name %q", group.Name)
		}
		enabledGroups[group.ID] = group
		groupNames[group.Name] = group.ID
	}
	groupNameByID := make(map[string]string, len(enabledGroups))
	for id, record := range enabledGroups {
		groupNameByID[id] = record.Name
	}
	nodeRecords, err := c.repository.ListNodeConfigs(ctx, nil)
	if err != nil {
		return Compiled{}, err
	}
	candidates, err := c.repository.ListGroupNodeCandidates(ctx)
	if err != nil {
		return Compiled{}, err
	}
	incumbents, err := c.loadIncumbents(ctx)
	if err != nil {
		return Compiled{}, err
	}
	members := newMemberResolver(candidates, incumbents, c.clock())
	members.addFingerprints(nodeRecords)
	// A listener only reaches compile with a dead pin when the node went away
	// after the listener was created; that row is withheld rather than failing
	// the whole document. See compiler_pinned.go.
	listeners, withheldPinned := withholdPinnedListeners(listeners, nodeRecords)
	withheldEntries = append(withheldEntries, withheldPinned...)
	withheldIDs := make(map[string]bool, len(withheldPinned))
	for _, entry := range withheldPinned {
		withheldIDs[entry.ListenerID] = true
	}
	clientRoutes, err := c.repository.ListResidentialClientRoutes(ctx)
	if err != nil {
		return Compiled{}, err
	}
	clientRoutes = routesForCompile(clientRoutes, withheldIDs)
	nodeByID := make(map[string]compiledNode, len(nodeRecords))
	nodeNameByID := make(map[string]string, len(nodeRecords))
	proxies := make([]map[string]any, 0, len(nodeRecords))
	for _, record := range nodeRecords {
		proxyName := nodeProxyName(record.Fingerprint)
		config, err := c.decryptNode(record, proxyName, groupNameByID)
		if err != nil {
			return Compiled{}, err
		}
		nodeByID[record.ID] = compiledNode{Name: proxyName, Config: config}
		nodeNameByID[record.ID] = proxyName
		proxies = append(proxies, config)
	}
	if err := validateResidentialDialerChains(groups, nodeRecords, nodeByID, members, groupNameByID); err != nil {
		return Compiled{}, err
	}
	// Backstop for rows that never went through the service validator: a chain
	// that cannot be honoured must fail here, not silently dial out directly.
	if err := validateGroupDialerChains(groups); err != nil {
		return Compiled{}, err
	}
	sort.Slice(proxies, func(left, right int) bool {
		return fmt.Sprint(proxies[left]["name"]) < fmt.Sprint(proxies[right]["name"])
	})

	derived := newDerivedProxySet()
	proxyGroups := make([]map[string]any, 0, len(enabledGroups))
	for _, record := range sortGroupsByDependency(groups) {
		if !record.Enabled {
			continue
		}
		compiledGroup, err := compileGroup(record, nodeByID, members, groupNameByID, derived)
		if err != nil {
			return Compiled{}, err
		}
		proxyGroups = append(proxyGroups, compiledGroup)
	}
	// Every derived proxy must reach the document or each dialer-proxy dangles,
	// which Mihomo rejects outright.
	proxies = derived.merged(proxies)

	listenerConfigs := make([]map[string]any, 0, len(listeners))
	endpoints := make([]Endpoint, 0, len(listeners))
	residentialFallbackRules := make([]string, 0)
	sharedInboundRules := make([]sharedUserRule, 0)
	// Dedicated listeners normally set `proxy`, which never consults rules.
	// A YAML default removes that binding and, for default:proxy only, records
	// an IN-NAME catch-all so unmatched traffic still reaches the same group.
	dedicatedFallbacks := make([]string, 0)
	usedEndpoints := make(map[string]string)
	usedEdgeRoutes := make(map[string]string)
	routesByListener := make(map[string][]store.ResidentialClientRouteRecord)
	for _, route := range clientRoutes {
		if !route.ChannelEnabled {
			continue
		}
		// A channel may publish the same logical sessions on a reverse-proxied
		// WebSocket entry point and on a directly reachable TCP one. Both
		// listeners authenticate the same users and reach the same exit.
		routesByListener[route.ListenerID] = append(routesByListener[route.ListenerID], route)
		if route.DirectListenerID != "" {
			routesByListener[route.DirectListenerID] = append(routesByListener[route.DirectListenerID], route)
		}
	}
	// Directly bound listeners compile first so their port and edge-route
	// uniqueness is validated before the aggregate listeners are merged in.
	for _, record := range listeners {
		if !record.Enabled || listener.SharedInboundOwnerOf(record) != "" {
			continue
		}
		// A listener that pins a node still names a group on its row (the column is
		// NOT NULL and the aggregate family uses a group as its anchor), but it must
		// not dereference that group: the anchor may be empty or disabled, and a
		// pinned listener's traffic never reaches it. Only an unpinned listener
		// needs a live group to exist.
		group, exists := enabledGroups[record.ProxyGroupID]
		if !exists && strings.TrimSpace(record.NodeID) == "" {
			return Compiled{}, fmt.Errorf("listener %q references a missing or disabled proxy group", record.Name)
		}
		egressName := group.Name
		// A pin always resolves here: withholdPinnedListeners already removed
		// every listener whose node this document does not carry, and such a
		// listener is withheld rather than repointed at its group.
		if nodeID := strings.TrimSpace(record.NodeID); nodeID != "" {
			egressName = nodeNameByID[nodeID]
		}
		endpointKey := fmt.Sprintf("%s:%d", record.BindAddress, record.Port)
		if other, exists := usedEndpoints[endpointKey]; exists {
			return Compiled{}, fmt.Errorf("listeners %q and %q use the same endpoint %s", other, record.Name, endpointKey)
		}
		usedEndpoints[endpointKey] = record.Name
		if isWebSocketListener(record.Kind) {
			routeKey, err := edgeRouteKey(record)
			if err != nil {
				return Compiled{}, err
			}
			if other, exists := usedEdgeRoutes[routeKey]; exists {
				return Compiled{}, fmt.Errorf("listeners %q and %q use the same public WebSocket route", other, record.Name)
			}
			usedEdgeRoutes[routeKey] = record.Name
		}
		clientRoutesForListener := routesByListener[record.ID]
		config, err := c.compileListener(record, egressName, clientRoutesForListener)
		if err != nil {
			return Compiled{}, err
		}
		// A YAML default removes `proxy` only for groups named by applies_to,
		// and only when that listener is not pinned to a node. Empty default
		// is a legacy document and must keep the binding even if applies_to
		// was stored beside it.
		_, inScope := scopeSet(routeConfig.AppliesTo)[record.ProxyGroupID]
		if routeConfig.Default != "" && len(clientRoutesForListener) == 0 && strings.TrimSpace(record.NodeID) == "" && inScope {
			delete(config, "proxy")
			if routeConfig.Default == routingrules.DefaultProxy {
				dedicatedFallbacks = append(dedicatedFallbacks, "IN-NAME,"+listenerConfigName(record.ID)+","+egressName)
			}
		}
		if len(clientRoutesForListener) > 0 {
			residentialFallbackRules = append(
				residentialFallbackRules,
				"IN-NAME,"+listenerConfigName(record.ID)+","+egressName,
			)
		}
		listenerConfigs = append(listenerConfigs, config)
		endpoints = append(endpoints, Endpoint{
			ID:          record.ID,
			Name:        record.Name,
			Kind:        record.Kind,
			BindAddress: record.BindAddress,
			Port:        record.Port,
		})
	}
	// One client, many services: the aggregate listeners carry every managed
	// service behind two ports. Members are selected by their service-scoped
	// username, so each service keeps its own credential, protocol and group.
	sharedListeners, sharedRules, err := c.compileSharedInbounds(settings, listeners, enabledGroups, nodeNameByID)
	if err != nil {
		return Compiled{}, err
	}
	sharedInboundRules = append(sharedInboundRules, sharedRules...)
	for _, shared := range sharedListeners {
		// Every protocol of one aggregate family deliberately shares one bind
		// address and port: Mihomo demultiplexes the protocols on that single
		// socket, which is what lets one client URL carry all of them. Only a
		// collision with a listener from a *different* family is an error.
		owner := listener.SharedInboundOwnerOf(shared.record)
		endpointKey := fmt.Sprintf("%s:%d", shared.record.BindAddress, shared.record.Port)
		if other, exists := usedEndpoints[endpointKey]; exists && other != owner {
			return Compiled{}, fmt.Errorf("listeners %q and %q use the same endpoint %s", other, shared.record.Name, endpointKey)
		}
		usedEndpoints[endpointKey] = owner
		if shared.edgeKey != "" {
			if other, exists := usedEdgeRoutes[shared.edgeKey]; exists {
				return Compiled{}, fmt.Errorf("listeners %q and %q use the same public WebSocket route", other, shared.record.Name)
			}
			usedEdgeRoutes[shared.edgeKey] = shared.record.Name
		}
		listenerConfigs = append(listenerConfigs, shared.config)
		endpoints = append(endpoints, Endpoint{
			ID:          shared.record.ID,
			Name:        shared.record.Name,
			Kind:        shared.record.Kind,
			BindAddress: shared.record.BindAddress,
			Port:        shared.record.Port,
		})
	}
	if err := validateNoListenerLoop(proxies, listenerConfigs); err != nil {
		return Compiled{}, err
	}

	compiledRules, err := compileResidentialClientRules(clientRoutes, nodeByID)
	if err != nil {
		return Compiled{}, err
	}
	siteRules := routingrules.Compile(routeConfig, groups, listeners, listenerConfigName)
	if routeConfig.Default != "" && !slices.Contains(routingrules.SupportedDefaults(), routeConfig.Default) {
		return Compiled{}, fmt.Errorf("routing default %q is unsupported", routeConfig.Default)
	}
	pinnedUsers, groupedUsers := splitSharedUsers(sharedInboundRules)
	if routeConfig.Default == "" {
		// Legacy documents: usernames win before site rules, and the
		// residential listener fallback stays behind those site rules.
		compiledRules = append(compiledRules, sharedUserTexts(sharedInboundRules)...)
		compiledRules = append(compiledRules, siteRules...)
		compiledRules = append(compiledRules, residentialFallbackRules...)
	} else {
		// Residential fail-closed rules, node pins, and every group the
		// document does not name match before site rules. Only applies_to
		// groups follow the site rules, and only while default is proxy.
		// direct and reject drop that catch-all. An empty applies_to therefore
		// leaves every shared username where a legacy document puts it.
		outside, inside := sharedUsersByScope(groupedUsers, scopeSet(routeConfig.AppliesTo))
		compiledRules = append(compiledRules, residentialFallbackRules...)
		compiledRules = append(compiledRules, sharedUserTexts(pinnedUsers)...)
		compiledRules = append(compiledRules, sharedUserTexts(outside)...)
		compiledRules = append(compiledRules, siteRules...)
		if routeConfig.Default == routingrules.DefaultProxy {
			compiledRules = append(compiledRules, sharedUserTexts(inside)...)
		}
		compiledRules = append(compiledRules, dedicatedFallbacks...)
	}
	tun, tunRule, err := compileTUN(settings, groups)
	if err != nil {
		return Compiled{}, err
	}
	if tunRule != "" {
		compiledRules = append(compiledRules, tunRule)
	}
	if routeConfig.Default == routingrules.DefaultReject && len(routeConfig.AppliesTo) > 0 {
		// The groups in applies_to had their catch-all removed above, so this
		// is what their unmatched traffic hits. Everyone else already matched.
		compiledRules = append(compiledRules, "NETWORK,tcp,REJECT", "NETWORK,udp,REJECT")
	}
	// Not an inbound's proxy group. That group is a listener `proxy` field
	// or an IN-NAME/IN-USER rule above.
	// See .agents/notes/implemented/feature/2026-10-18-yaml-routing-intent.md
	compiledRules = append(compiledRules, "MATCH,DIRECT")
	document := map[string]any{
		"tun":       tun,
		"mode":      "rule",
		"log-level": settings.Performance.LogLevel,
		"ipv6":      settings.DNS.IPv6,
		"allow-lan": false,
		"dns": map[string]any{
			"enable":             settings.DNS.Enabled,
			"ipv6":               settings.DNS.IPv6,
			"enhanced-mode":      settings.DNS.EnhancedMode,
			"default-nameserver": settings.DNS.DefaultNameserver,
			"nameserver":         settings.DNS.Nameserver,
			"fallback":           settings.DNS.Fallback,
		},
		"tcp-concurrent":      settings.Performance.TCPConcurrent,
		"unified-delay":       settings.Performance.UnifiedDelay,
		"keep-alive-idle":     settings.Performance.KeepAliveIdle,
		"keep-alive-interval": settings.Performance.KeepAliveInterval,
		"find-process-mode":   settings.Performance.FindProcessMode,
		"proxies":             proxies,
		"proxy-groups":        proxyGroups,
		"listeners":           listenerConfigs,
		"rules":               compiledRules,
	}
	if c.controllerSocket != "" {
		document["external-controller-unix"] = c.controllerSocket
	}
	if c.egressInterface != "" {
		document["interface-name"] = c.egressInterface
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return Compiled{}, fmt.Errorf("encode mihomo configuration: %w", err)
	}
	return Compiled{
		TUNDevice: func() string {
			if tunRule != "" {
				return managedTUNDevice
			}
			return ""
		}(),
		YAML:             encoded,
		Endpoints:        endpoints,
		ProxyCount:       len(proxies),
		ControllerSocket: c.controllerSocket,
		GroupMembers:     members.memberships(groupNameByID),
		WithheldEntries:  withheldEntries,
	}, nil
}

func validateNoListenerLoop(proxies, listeners []map[string]any) error {
	for _, proxy := range proxies {
		proxyPort, ok := integerValue(proxy["port"])
		if !ok {
			continue
		}
		proxyHost := strings.TrimSpace(fmt.Sprint(proxy["server"]))
		for _, listener := range listeners {
			listenerPort, ok := integerValue(listener["port"])
			if !ok || proxyPort != listenerPort {
				continue
			}
			listenerHost := strings.TrimSpace(fmt.Sprint(listener["listen"]))
			if hostsCanReferToSameLocalEndpoint(proxyHost, listenerHost) {
				return fmt.Errorf("proxy %q points to managed listener %s:%d", proxy["name"], listenerHost, listenerPort)
			}
		}
	}
	return nil
}

func integerValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	default:
		parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
		return parsed, err == nil
	}
}

func hostsCanReferToSameLocalEndpoint(proxyHost, listenerHost string) bool {
	proxyHost = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(proxyHost)), ".")
	listenerHost = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(listenerHost)), ".")
	if proxyHost == "localhost" {
		return listenerHost == "localhost" || listenerHost == "" || isLoopbackOrUnspecified(listenerHost)
	}
	proxyIP := net.ParseIP(proxyHost)
	listenerIP := net.ParseIP(listenerHost)
	if proxyIP != nil && proxyIP.IsLoopback() {
		return listenerHost == "localhost" || listenerHost == "" || listenerIP != nil && (listenerIP.IsLoopback() || listenerIP.IsUnspecified())
	}
	return proxyHost != "" && proxyHost == listenerHost
}

func isLoopbackOrUnspecified(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

type compiledNode struct {
	Name   string
	Config map[string]any
}

func decodeSourceSpec(value string) (proxygroup.SourceSpec, error) {
	var spec proxygroup.SourceSpec
	if err := json.Unmarshal([]byte(value), &spec); err != nil {
		return proxygroup.SourceSpec{}, err
	}
	return spec, nil
}

// sortGroupsByDependency orders groups so every referenced group is emitted
// before the groups referencing it, keeping the compiled YAML stable and
// friendly to strict readers. Cycles cannot occur because the service rejects
// them at write time; unknown references keep their original position.
func sortGroupsByDependency(groups []store.ProxyGroupRecord) []store.ProxyGroupRecord {
	index := make(map[string]int, len(groups))
	for position, group := range groups {
		index[group.ID] = position
	}
	visited := make([]bool, len(groups))
	ordered := make([]store.ProxyGroupRecord, 0, len(groups))
	var visit func(position int)
	visit = func(position int) {
		if visited[position] {
			return
		}
		visited[position] = true
		spec, err := decodeSourceSpec(groups[position].SourceSpecJSON)
		var referenced []string
		if err == nil {
			referenced = spec.GroupIDs
		}
		// A dialer is a dependency too: emit the group it dials through first.
		if dialer := strings.TrimSpace(groups[position].DialerProxyGroupID); dialer != "" {
			referenced = append(referenced, dialer)
		}
		for _, referencedID := range referenced {
			if referencedPosition, exists := index[referencedID]; exists {
				visit(referencedPosition)
			}
		}
		ordered = append(ordered, groups[position])
	}
	for position := range groups {
		visit(position)
	}
	return ordered
}

func compileGroup(
	record store.ProxyGroupRecord,
	nodes map[string]compiledNode,
	resolver *memberResolver,
	groupNameByID map[string]string,
	derived *derivedProxySet,
) (map[string]any, error) {
	spec, err := decodeSourceSpec(record.SourceSpecJSON)
	if err != nil {
		return nil, fmt.Errorf("decode group %q source spec: %w", record.Name, err)
	}
	// A group chain is expressed per member: this group's members are emitted as
	// derived proxies that dial out through the dialer group's name.
	dialerName := ""
	if dialerID := strings.TrimSpace(record.DialerProxyGroupID); dialerID != "" {
		name, exists := groupNameByID[dialerID]
		if !exists {
			// validateGroupDialerChains already rejected this; refuse rather
			// than let the chain quietly become a direct connection.
			return nil, fmt.Errorf("proxy group %q dials through a group that is missing or disabled", record.Name)
		}
		dialerName = name
	}
	resolvedNodeIDs, err := resolver.nodeIDs(record)
	if err != nil {
		return nil, err
	}
	members := make([]string, 0, len(spec.GroupIDs)+len(resolvedNodeIDs)+1)
	seen := make(map[string]struct{}, len(spec.GroupIDs)+len(resolvedNodeIDs)+1)
	for _, groupID := range spec.GroupIDs {
		name, exists := groupNameByID[groupID]
		if !exists {
			// The referenced group was disabled after this spec was saved;
			// drop the member instead of failing the whole compilation.
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		members = append(members, name)
	}
	for _, nodeID := range resolvedNodeIDs {
		node, exists := nodes[nodeID]
		if !exists {
			continue
		}
		memberName := node.Name
		if dialerName != "" {
			derivedName, err := derived.derive(node, record.ID, record.Name, dialerName)
			if err != nil {
				return nil, err
			}
			memberName = derivedName
		}
		if _, duplicate := seen[memberName]; duplicate {
			continue
		}
		seen[memberName] = struct{}{}
		members = append(members, memberName)
	}
	if spec.IncludeDirect {
		members = append(members, "DIRECT")
	}
	if len(members) == 0 {
		if record.EmptyBehavior == "direct" {
			members = append(members, "DIRECT")
		} else {
			members = append(members, "REJECT")
		}
	}
	group := map[string]any{
		"name":    record.Name,
		"proxies": members,
	}
	switch record.Strategy {
	case "manual":
		group["type"] = "select"
	case "url-test", "fallback":
		group["type"] = record.Strategy
		group["url"] = spec.TestURL
		group["interval"] = spec.IntervalSeconds
		group["lazy"] = true
	case "round-robin", "consistent-hashing", "sticky-sessions":
		group["type"] = "load-balance"
		group["strategy"] = record.Strategy
		group["url"] = spec.TestURL
		group["interval"] = spec.IntervalSeconds
		group["lazy"] = true
	default:
		return nil, fmt.Errorf("proxy group %q uses unsupported strategy %q", record.Name, record.Strategy)
	}
	return group, nil
}

func (c *Compiler) compileListener(
	record store.ListenerRecord,
	groupName string,
	clientRoutes []store.ResidentialClientRouteRecord,
) (map[string]any, error) {
	config := map[string]any{
		"name":   listenerConfigName(record.ID),
		"type":   record.Kind,
		"listen": record.BindAddress,
		"port":   record.Port,
		"users":  []any{},
	}
	if len(clientRoutes) == 0 {
		config["proxy"] = groupName
	}
	if record.Kind == "http" || record.Kind == "socks" || record.Kind == "mixed" {
		config["udp"] = true
	}
	users := make([]map[string]any, 0, len(clientRoutes)+1)
	seenUsers := make(map[string]struct{}, len(clientRoutes)+1)
	if record.AuthMode == "userpass" {
		plaintext, err := c.cipher.Open(record.AuthConfigEncrypted, []byte("listener:"+record.ID))
		if err != nil {
			return nil, fmt.Errorf("decrypt listener %q auth: %w", record.Name, err)
		}
		var auth listener.Auth
		if err := json.Unmarshal(plaintext, &auth); err != nil {
			return nil, fmt.Errorf("decode listener %q auth: %w", record.Name, err)
		}
		if auth.Username == "" || auth.Password == "" {
			return nil, fmt.Errorf("listener %q has incomplete authentication", record.Name)
		}
		user := map[string]any{"username": auth.Username}
		switch record.Kind {
		case "vless":
			user["uuid"] = auth.Password
		case "vmess":
			user["uuid"] = auth.Password
			user["alterId"] = 0
		default:
			user["password"] = auth.Password
		}
		users = append(users, user)
		seenUsers[auth.Username] = struct{}{}
	}
	for _, route := range clientRoutes {
		if _, duplicate := seenUsers[route.AuthUsername]; duplicate {
			return nil, fmt.Errorf("listener %q has duplicate residential client username", record.Name)
		}
		password, err := c.cipher.Open(
			route.AuthPasswordEncrypted,
			[]byte("residential-client-session:"+route.ChannelID+":"+route.SessionID),
		)
		if err != nil {
			return nil, fmt.Errorf("decrypt listener %q residential client auth: %w", record.Name, err)
		}
		if route.AuthUsername == "" || len(password) == 0 {
			return nil, fmt.Errorf("listener %q has incomplete residential client authentication", record.Name)
		}
		user := map[string]any{"username": route.AuthUsername}
		if record.Kind == "vless" || record.Kind == "vmess" {
			user["uuid"] = string(password)
		} else {
			user["password"] = string(password)
		}
		users = append(users, user)
		seenUsers[route.AuthUsername] = struct{}{}
	}
	config["users"] = users
	if isWebSocketListener(record.Kind) {
		var transport listener.Transport
		if err := json.Unmarshal([]byte(record.TransportJSON), &transport); err != nil {
			return nil, fmt.Errorf("decode listener %q transport: %w", record.Name, err)
		}
		if transport.Type != "ws" || transport.WSPath == "" {
			return nil, fmt.Errorf("listener %q has invalid WebSocket transport", record.Name)
		}
		normalizedPath, err := listener.NormalizeWebSocketPath(transport.WSPath)
		if err != nil {
			return nil, fmt.Errorf("listener %q has invalid WebSocket path: %w", record.Name, err)
		}
		config["ws-path"] = normalizedPath
		if record.Kind == "vless" || record.Kind == "trojan" {
			config["allow-insecure"] = true
		}
	}
	return config, nil
}

func isWebSocketListener(kind string) bool {
	return kind == "vless" || kind == "vmess" || kind == "trojan"
}

func edgeRouteKey(record store.ListenerRecord) (string, error) {
	var transport listener.Transport
	if err := json.Unmarshal([]byte(record.TransportJSON), &transport); err != nil {
		return "", fmt.Errorf("decode listener %q transport: %w", record.Name, err)
	}
	path, err := listener.NormalizeWebSocketPath(transport.WSPath)
	if err != nil {
		return "", fmt.Errorf("listener %q has invalid WebSocket path: %w", record.Name, err)
	}
	var endpoint listener.PublicEndpoint
	if err := json.Unmarshal([]byte(record.PublicEndpointJSON), &endpoint); err != nil {
		return "", fmt.Errorf("decode listener %q public endpoint: %w", record.Name, err)
	}
	host := strings.ToLower(strings.TrimSpace(endpoint.Host))
	if host == "" {
		return "", fmt.Errorf("listener %q has no public WebSocket host", record.Name)
	}
	return host + "\x00" + path, nil
}

func nodeProxyName(fingerprint string) string {
	fingerprint = strings.TrimSpace(fingerprint)
	if len(fingerprint) > 16 {
		fingerprint = fingerprint[:16]
	}
	return "hx-node-" + fingerprint
}

func listenerConfigName(id string) string {
	id = strings.TrimPrefix(id, "listener-")
	if len(id) > 16 {
		id = id[:16]
	}
	return "hx-in-" + id
}
