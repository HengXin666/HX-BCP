package entry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HengXin666/hx-pxy/internal/residential"
	"github.com/HengXin666/hx-pxy/internal/slot"
)

// ErrNoRoute reports a selector nothing can serve right now.
var ErrNoRoute = errors.New("no route for the selector")

// ErrPoolDenied reports a pool the tenant may not use.
var ErrPoolDenied = errors.New("pool not allowed for the tenant")

// ErrQuotaExceeded reports a tenant over its residential quota.
var ErrQuotaExceeded = errors.New("residential quota exceeded")

// RouteInfo says where one connection went, for logs and accounting.
type RouteInfo struct {
	Pool    string
	Kind    string
	Target  string
	NodeID  string
	Group   string
	Gateway string
	Slot    string
}

// GatewaySource lists the residential providers the entry can dial.
type GatewaySource interface {
	EntryGateways(context.Context) ([]residential.EntryGateway, error)
}

// Router turns a selector and a target into a dialed upstream connection.
type Router struct {
	snapshots   *snapshotHolder
	nodeSlots   *slot.Table
	gatewaySlot *slot.Table
	gateways    GatewaySource
	rotation    atomic.Uint64
	timeout     time.Duration
	direct      func(ctx context.Context, network, address string) (net.Conn, error)
	// egress dials leave the host: the direct pool and residential gateways.
	egress func(ctx context.Context, network, address string) (net.Conn, error)
	// allowLocal lets the direct pool reach local addresses, tests only.
	allowLocal bool

	gatewayMutex  sync.Mutex
	gatewayCache  []residential.EntryGateway
	gatewayLoaded time.Time
}

// NewRouter builds a router over a snapshot holder and a gateway source.
func NewRouter(snapshots *snapshotHolder, gateways GatewaySource) *Router {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return &Router{
		snapshots:   snapshots,
		nodeSlots:   slot.NewTable(nil),
		gatewaySlot: slot.NewTable(nil),
		gateways:    gateways,
		timeout:     20 * time.Second,
		direct:      dialer.DialContext,
		egress:      dialer.DialContext,
	}
}

// Dial opens a tunnel to target for one selector.
func (r *Router) Dial(ctx context.Context, tenant string, selector Selector, target string) (net.Conn, RouteInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	info := RouteInfo{Pool: selector.Pool, Target: target, Slot: selector.Slot}
	snapshot := r.snapshots.load()
	if selector.Group != "" && selector.Pool != PoolResidential {
		group, ok := snapshot.Group(selector.Group)
		if !ok {
			return nil, info, fmt.Errorf("%w: proxy group %q", ErrNoRoute, selector.Group)
		}
		info.Kind, info.Group = "group", group.Name
		conn, err := r.dialHub(ctx, snapshot, group.HubUser, target)
		return conn, info, err
	}
	switch selector.Pool {
	case PoolDirect:
		info.Kind = "direct"
		address, err := r.directTarget(ctx, target)
		if err != nil {
			return nil, info, err
		}
		conn, err := r.egress(ctx, "tcp", address)
		return conn, info, err
	case PoolResidential:
		return r.dialGateway(ctx, snapshot, tenant, selector, target, info)
	default:
		return r.dialNode(ctx, snapshot, tenant, selector, target, info)
	}
}

// directTarget resolves a direct target once and refuses anything but public
// unicast addresses, so a tenant with the direct pool cannot reach the control
// plane, the hub, this host or its networks through the entry.
func (r *Router) directTarget(ctx context.Context, target string) (string, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return "", fmt.Errorf("%w: cannot resolve %s", ErrNoRoute, host)
	}
	for _, address := range addresses {
		if !r.allowLocal && !publicAddress(address.Unmap()) {
			return "", fmt.Errorf("%w: %s resolves to a non-public address", ErrPoolDenied, host)
		}
	}
	return net.JoinHostPort(addresses[0].Unmap().String(), port), nil
}

// dialNode picks a subscription node, sticky through the slot table when the
// selector names a session, rotating per connection otherwise.
func (r *Router) dialNode(ctx context.Context, snapshot *Snapshot, tenant string, selector Selector, target string, info RouteInfo) (net.Conn, RouteInfo, error) {
	info.Kind = "node"
	catalog := snapshotCatalog{snapshot: snapshot, rotation: r.rotation.Add(1)}
	request := slotRequest(tenant, selector)
	var nodeID string
	if selector.Sticky() {
		binding, err := r.nodeSlots.Resolve(catalog, request)
		if err != nil {
			return nil, info, fmt.Errorf("%w: %v", ErrNoRoute, err)
		}
		nodeID = binding.NodeID
	} else {
		node, err := catalog.Pick(request, slot.Binding{})
		if err != nil {
			return nil, info, fmt.Errorf("%w: %v", ErrNoRoute, err)
		}
		nodeID = node.ID
	}
	candidate, ok := snapshot.Candidate(nodeID)
	if !ok {
		// A hold-policy slot may still point at a node the snapshot dropped.
		return nil, info, fmt.Errorf("%w: node %s is no longer compiled", ErrNoRoute, nodeID)
	}
	info.NodeID = nodeID
	conn, err := r.dialHub(ctx, snapshot, candidate.HubUser, target)
	return conn, info, err
}

// dialGateway renders a vendor username and dials the residential gateway,
// through the provider's upstream group when it has one.
func (r *Router) dialGateway(ctx context.Context, snapshot *Snapshot, tenant string, selector Selector, target string, info RouteInfo) (net.Conn, RouteInfo, error) {
	info.Kind = "gateway"
	gateways, err := r.loadGateways(ctx)
	if err != nil {
		return nil, info, err
	}
	var gateway residential.EntryGateway
	found := false
	for _, candidate := range gateways {
		if selector.Group == "" || strings.EqualFold(candidate.Name, selector.Group) || candidate.ID == selector.Group {
			gateway, found = candidate, true
			break
		}
	}
	if !found {
		return nil, info, fmt.Errorf("%w: no residential provider matches", ErrNoRoute)
	}
	info.Gateway = gateway.Name
	ttl := gateway.TTLSeconds
	if selector.TTL > 0 {
		ttl = selector.TTL * 60
	}
	session := ""
	if gateway.Sticky() {
		if selector.Sticky() {
			request := slotRequest(tenant, selector)
			// The vendor keeps a session for ttl from its first use, so the
			// binding ends then too instead of renewing past it.
			request.MaxAge = time.Duration(ttl) * time.Second
			binding, err := r.gatewaySlot.Resolve(gatewayCatalog{id: gateway.ID}, request)
			if err != nil {
				return nil, info, err
			}
			session = sessionID(tenant, selector.Slot, gateway.ID, binding.BoundAt)
		} else {
			session = randomSessionID()
		}
	}
	username, err := gateway.RenderUsername(session, selector.Country, selector.City, ttl)
	if err != nil {
		return nil, info, err
	}
	address := net.JoinHostPort(gateway.Host, strconv.Itoa(gateway.Port))
	var conn net.Conn
	if gateway.UpstreamGroupID != "" {
		group, ok := snapshot.groupByID(gateway.UpstreamGroupID)
		if !ok {
			return nil, info, fmt.Errorf("%w: upstream group of provider %q is not compiled", ErrNoRoute, gateway.Name)
		}
		conn, err = r.dialHub(ctx, snapshot, group.HubUser, address)
	} else {
		conn, err = r.egress(ctx, "tcp", address)
	}
	if err != nil {
		return nil, info, err
	}
	conn, err = handshakeWithDeadline(ctx, conn, func(raw net.Conn) (net.Conn, error) {
		switch gateway.Protocol {
		case "socks5":
			return raw, socksClientConnect(raw, username, gateway.Password, target)
		case "https":
			secured := tls.Client(raw, &tls.Config{ServerName: gateway.Host, MinVersion: tls.VersionTLS12})
			return httpClientConnect(secured, username, gateway.Password, target)
		default:
			return httpClientConnect(raw, username, gateway.Password, target)
		}
	})
	return conn, info, err
}

// dialHub reaches a node or group through the loopback Mihomo hub.
func (r *Router) dialHub(ctx context.Context, snapshot *Snapshot, user, target string) (net.Conn, error) {
	if snapshot.HubAddress == "" {
		return nil, fmt.Errorf("%w: the data plane has not published the entry hub yet", ErrNoRoute)
	}
	conn, err := r.direct(ctx, "tcp", snapshot.HubAddress)
	if err != nil {
		return nil, fmt.Errorf("dial entry hub: %w", err)
	}
	return handshakeWithDeadline(ctx, conn, func(raw net.Conn) (net.Conn, error) {
		return raw, socksClientConnect(raw, user, snapshot.HubSecret, target)
	})
}

// handshakeWithDeadline runs an upstream handshake bounded by ctx.
func handshakeWithDeadline(ctx context.Context, conn net.Conn, handshake func(net.Conn) (net.Conn, error)) (net.Conn, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	result, err := handshake(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return result, nil
}

// loadGateways caches the provider list for a short while.
func (r *Router) loadGateways(ctx context.Context) ([]residential.EntryGateway, error) {
	r.gatewayMutex.Lock()
	defer r.gatewayMutex.Unlock()
	if r.gateways == nil {
		return nil, fmt.Errorf("%w: residential providers are not available", ErrNoRoute)
	}
	if time.Since(r.gatewayLoaded) < 30*time.Second && r.gatewayCache != nil {
		return r.gatewayCache, nil
	}
	gateways, err := r.gateways.EntryGateways(ctx)
	if err != nil {
		return nil, err
	}
	r.gatewayCache, r.gatewayLoaded = gateways, time.Now()
	return gateways, nil
}

// InvalidateGateways drops the provider cache after an operator edit.
func (r *Router) InvalidateGateways() {
	r.gatewayMutex.Lock()
	r.gatewayLoaded = time.Time{}
	r.gatewayMutex.Unlock()
}

// Slots lists the live slot bindings of both tables.
func (r *Router) Slots(tenant string) map[string][]slot.Entry {
	return map[string][]slot.Entry{"nodes": r.nodeSlots.List(tenant), "residential": r.gatewaySlot.List(tenant)}
}

// ReleaseSlot drops one slot from both tables so it rebinds on next use.
func (r *Router) ReleaseSlot(tenant, name string) {
	r.nodeSlots.Release(tenant, name)
	r.gatewaySlot.Release(tenant, name)
}

// SetEgressInterface binds dials that leave the host to one interface, so a
// host TUN does not capture them; empty keeps the default route.
func (r *Router) SetEgressInterface(name string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: bindToDevice(name)}
	r.egress = dialer.DialContext
}

// ReleaseTenant drops every slot of one tenant.
func (r *Router) ReleaseTenant(tenant string) {
	r.nodeSlots.ReleaseTenant(tenant)
	r.gatewaySlot.ReleaseTenant(tenant)
}

// Sweep drops expired bindings.
func (r *Router) Sweep() {
	r.nodeSlots.Sweep()
	r.gatewaySlot.Sweep()
}

func slotRequest(tenant string, selector Selector) slot.Request {
	return slot.Request{
		Tenant:  tenant,
		Slot:    selector.Slot,
		Pool:    selector.Pool,
		Country: selector.Country,
		City:    selector.City,
		ASN:     selector.ASN,
		TTL:     time.Duration(selector.TTL) * time.Minute,
		OnLoss:  selector.OnLoss,
		Grace:   5 * time.Minute,
	}
}

// gatewayCatalog is a one-node catalog: a gateway never disappears, a slot
// only rebinds when its binding expires or is released.
type gatewayCatalog struct{ id string }

func (c gatewayCatalog) Lookup(id string, _ slot.Request) (slot.Node, bool) {
	return slot.Node{ID: id, Alive: id == c.id}, id == c.id
}

func (c gatewayCatalog) Pick(slot.Request, slot.Binding) (slot.Node, error) {
	return slot.Node{ID: c.id, Alive: true}, nil
}

// sessionID derives the vendor session of a slot binding. A new binding gives a
// new id and therefore a new exit, the same binding always gives the same id.
func sessionID(tenant, slotName, gatewayID string, boundAt time.Time) string {
	digest := sha256.Sum256([]byte(tenant + "\x00" + slotName + "\x00" + gatewayID + "\x00" + strconv.FormatInt(boundAt.UnixNano(), 10)))
	return hex.EncodeToString(digest[:6])
}

func randomSessionID() string {
	var buffer [6]byte
	_, _ = rand.Read(buffer[:])
	return hex.EncodeToString(buffer[:])
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// publicAddress reports whether a direct dial may go to address: a global
// unicast that is neither private, reserved nor one of this host's own.
func publicAddress(address netip.Addr) bool {
	if !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return !localAddress(address)
}

func localAddress(address netip.Addr) bool {
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, item := range interfaces {
		if prefix, err := netip.ParsePrefix(item.String()); err == nil && prefix.Addr().Unmap() == address {
			return true
		}
	}
	return false
}
