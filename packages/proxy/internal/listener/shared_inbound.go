package listener

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func parseIPAddress(value string) net.IP { return net.ParseIP(strings.TrimSpace(value)) }

// Shared-inbound ownership markers.
//
// A shared inbound is a *membership* concept, not a separate record: every
// proxy service keeps its own listener row (its own name, credential, protocol
// and proxy group), but rows marked here reuse one bind address and port
// instead of each reserving a dedicated one. Mihomo then separates the members
// by username through IN-USER rules, so a single client process can carry
// every protocol and every group through one URL.
const (
	// SharedInboundStandardOwner marks HTTP / SOCKS5 / Mixed members that share
	// the aggregate Mixed port.
	SharedInboundStandardOwner = "standard"
	// SharedInboundWebSocketOwner marks VLESS / VMess / Trojan members that
	// share one aggregate WebSocket port per protocol.
	SharedInboundWebSocketOwner = "websocket"

	// sharedInboundRouteName is the single reserved edge route shared by every
	// WebSocket service carried by an aggregate listener.
	sharedInboundRouteName = "shared"
	// sharedAggregateNamePrefix identifies the single aggregate row of a family.
	sharedAggregateNamePrefix = "hx-shared-inbound-"
)

// SharedInboundOwnerOf reports the aggregate family a record belongs to, or ""
// for a dedicated per-service listener.
func SharedInboundOwnerOf(record store.ListenerRecord) string {
	return strings.TrimSpace(record.SharedInbound)
}

// DefaultSharedInboundOwnerForKind reports the aggregate family a listener of
// this protocol joins when its caller does not name one.
//
// Omitting the marker used to mean "reserve a dedicated port", and that default
// was the root cause of "one managed service, one open port": every caller that
// did not know about shared inbounds — the raw management API, a script, a new
// feature — quietly produced another bound port. The control plane promises a
// single entry point, so the default is now membership, and the HTTP / SOCKS /
// Mixed family is the one every service can be carried by.
//
// WebSocket protocols are deliberately excluded, and that exclusion is a
// measurement rather than a preference. The only advanced-kind listener this
// build creates is a residential channel entry point, which is addressed by its
// own channel-scoped WebSocket path and carries per-session IN-USER routes that
// the aggregate listener cannot express. A listener creation request carries
// nothing that reliably distinguishes that entry point from an ordinary VLESS
// service (same kind, same loopback bind, same username/password auth), so
// defaulting advanced kinds into the family would silently strip session
// routing from the next residential channel. They keep their own port unless a
// caller marks them explicitly, which the management API still allows.
func DefaultSharedInboundOwnerForKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "http", "socks", "mixed":
		return SharedInboundStandardOwner
	default:
		return ""
	}
}

// SharedInboundCarrierKind is the Mihomo listener type that carries a member of
// a shared-inbound family.
//
// The standard family has exactly one carrier: Mihomo's Mixed listener speaks
// HTTP proxy and SOCKS5 on a single socket, which is the contract documented for
// the 7890 entry point ("http / socks / mixed"). An HTTP or SOCKS5 service
// therefore gets no listener of its own kind — it is carried by the Mixed
// listener and selected by username.
//
// Deriving the carrier from the family rather than from the member's row is what
// makes the family convergent. Keyed by member kind instead, the first HTTP
// member would create an aggregate row of kind "http" that publishes no
// listener, a later Mixed member would add a second aggregate row for the same
// port, and the compiler — which can only carry a Mixed standard listener —
// would silently drop the HTTP service from the data plane.
//
// The WebSocket family differs: each protocol is a genuinely distinct Mihomo
// listener type, so it keeps one carrier per protocol even though they share a
// port and a ws-path.
func SharedInboundCarrierKind(owner, kind string) string {
	switch strings.ToLower(strings.TrimSpace(owner)) {
	case SharedInboundStandardOwner:
		return "mixed"
	case SharedInboundWebSocketOwner:
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return ""
	}
}

// SharedInboundCarrierKey identifies the published listener a member belongs to.
// Members that share a carrier share a key, which is what makes the standard
// family collapse to one aggregate listener.
func SharedInboundCarrierKey(owner, kind string) string {
	return strings.ToLower(strings.TrimSpace(owner)) + "|" + SharedInboundCarrierKind(owner, kind)
}

// IsSharedInboundMemberKind reports whether a listener of this protocol is
// carried by the given aggregate family. HTTP, SOCKS5 and Mixed are all carried
// by the standard Mixed listener.
func IsSharedInboundMemberKind(owner, kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch strings.ToLower(strings.TrimSpace(owner)) {
	case SharedInboundStandardOwner:
		return kind == "http" || kind == "socks" || kind == "mixed"
	case SharedInboundWebSocketOwner:
		return kind == "vless" || kind == "vmess" || kind == "trojan"
	default:
		return false
	}
}

// SharedInboundMemberKinds lists every protocol a family carries, in a stable
// order. It is used by validation messages and by tests that need to pin the
// family contract.
func SharedInboundMemberKinds(owner string) []string {
	switch strings.ToLower(strings.TrimSpace(owner)) {
	case SharedInboundStandardOwner:
		return []string{"http", "socks", "mixed"}
	case SharedInboundWebSocketOwner:
		return []string{"vless", "vmess", "trojan"}
	default:
		return nil
	}
}

// IsSharedInboundOwner reports whether an owner marker is one of the two
// aggregate families.
func IsSharedInboundOwner(owner string) bool {
	switch strings.TrimSpace(owner) {
	case SharedInboundStandardOwner, SharedInboundWebSocketOwner:
		return true
	default:
		return false
	}
}

// SharedInboundRoutePath is the single reserved edge route shared by every
// WebSocket service carried by an aggregate listener, so one URL works for
// every protocol.
func SharedInboundRoutePath() string {
	return WebSocketPathPrefix + sharedInboundRouteName
}

// SharedInboundSpec is the resolved shared-inbound configuration handed to the
// listener service. It mirrors systemsettings.SharedInboundSettings without
// creating an import cycle; the application layer maps one onto the other.
type SharedInboundSpec struct {
	// Enabled publishes standard services through the aggregate Mixed listener.
	Enabled bool
	// IncludeWebSocket publishes WebSocket services through the aggregate
	// VLESS / VMess / Trojan listeners.
	IncludeWebSocket bool
	MixedBindAddress string
	MixedPort        int
	WSPort           int
	// Client-facing endpoints. The shared WebSocket port is loopback-only, so
	// a public host (normally the reverse proxy) is required for a usable
	// subscription.
	MixedPublicHost string
	MixedPublicPort int
	MixedPublicTLS  bool
	WSPublicHost    string
	WSPublicPort    int
}

// SharedEndpointProvider resolves the current shared-inbound configuration at
// export time. The listener service keeps it optional so packages that only
// need per-service listeners (tests) can skip it.
type SharedEndpointProvider func() (SharedInboundSpec, bool)

// ResolveSharedEndpoint maps a member listener onto the client-facing endpoint
// of its aggregate family. It returns ok=false when the family is enabled but
// has no reachable endpoint, which callers surface as a disabled share link so
// an internal Mihomo port never leaks into a subscription.
func ResolveSharedEndpoint(spec SharedInboundSpec, record store.ListenerRecord, requestHost string) (PublicEndpoint, string, int, bool) {
	owner := SharedInboundOwnerOf(record)
	switch owner {
	case SharedInboundStandardOwner:
		host := strings.TrimSpace(spec.MixedPublicHost)
		port := spec.MixedPublicPort
		endpoint := PublicEndpoint{Host: host, Port: port, TLS: spec.MixedPublicTLS}
		if host == "" {
			// Reuse the per-service rule: only an explicitly public bind
			// address may be advertised, otherwise fall back to the request
			// host (the admin reaches the control plane by name).
			if ip := parseIPAddress(spec.MixedBindAddress); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
				return PublicEndpoint{Port: spec.MixedPort}, ip.String(), spec.MixedPort, true
			}
			if requestHost == "" {
				return PublicEndpoint{}, "", 0, false
			}
			fallback := exportHost(spec.MixedBindAddress, requestHost)
			if fallback == "" {
				return PublicEndpoint{}, "", 0, false
			}
			return PublicEndpoint{Port: spec.MixedPort}, fallback, spec.MixedPort, true
		}
		if port == 0 {
			port = 443
		}
		return endpoint, host, port, true
	case SharedInboundWebSocketOwner:
		host := strings.TrimSpace(spec.WSPublicHost)
		if host == "" {
			return PublicEndpoint{}, "", 0, false
		}
		port := spec.WSPublicPort
		if port == 0 {
			port = 443
		}
		return PublicEndpoint{Host: host, Port: port, TLS: true}, host, port, true
	default:
		return PublicEndpoint{}, "", 0, false
	}
}

// SharedInboundMember identifies one aggregate family and where it binds. The
// per-family membership itself is read from the persisted listener rows.
type SharedInboundMember struct {
	Owner       string
	Kind        string
	BindAddress string
	Port        int
}

// EnsureSharedInbounds converges the aggregate listener rows onto the current
// persisted membership and returns every aggregate the control plane manages.
//
// The persisted rows are the desired state: every listener marked with a
// shared-inbound owner and a non-aggregate name is a member, and every family
// with at least one member needs exactly one aggregate row. The call is
// therefore idempotent and safe after any mutation — creating a service adds a
// family, deleting the last member removes it, and changing the configured port
// repairs the existing aggregate in place.
func (s *Service) EnsureSharedInbounds(
	ctx context.Context,
	spec SharedInboundSpec,
	_ []store.ProxyGroupRecord,
	_ []SharedInboundMember,
) ([]Listener, error) {
	// A member without credentials cannot be carried by a username-routed entry
	// point, and the compiler rejects the whole configuration when it meets one,
	// so repair the family before converging it. Rows written through the
	// listener service already carry credentials; this covers rows rewritten
	// directly (the versioned migration that folds dedicated ports into the
	// family), where the store cannot mint them because encryption needs the
	// master key.
	if _, err := s.BackfillMemberCredentials(ctx); err != nil {
		return nil, err
	}
	records, err := s.repository.ListListeners(ctx)
	if err != nil {
		return nil, err
	}
	aggregates := make(map[string]store.ListenerRecord, 4)
	staleAggregates := make([]store.ListenerRecord, 0, 2)
	families := make(map[string]SharedInboundMember, 4)
	familyOrder := make([]string, 0, 4)
	for _, record := range records {
		owner := SharedInboundOwnerOf(record)
		if owner == "" {
			continue
		}
		if !IsSharedInboundMemberKind(owner, record.Kind) {
			// A row marked with a family that cannot carry its protocol is a
			// stored inconsistency (an old per-protocol aggregate, or a row
			// written by an older build). Refusing to guess keeps the aggregate
			// from publishing a port nobody routes on.
			return nil, fmt.Errorf(
				"%w: listener %q is a %s member of the %s shared inbound, which carries %s",
				ErrInvalid, record.Name, record.Kind, owner, strings.Join(SharedInboundMemberKinds(owner), " / "),
			)
		}
		// Every member of a family converges on the family's single carrier, so
		// the first member decides the family and later ones only add a username.
		carrierKind := SharedInboundCarrierKind(owner, record.Kind)
		key := SharedInboundCarrierKey(owner, record.Kind)
		if isAggregateRecord(record) {
			if existing, ok := aggregates[key]; ok {
				carrierKind := SharedInboundCarrierKind(owner, record.Kind)
				// A family keeps exactly one aggregate row. An install that ran
				// the older per-protocol layout can hold several for the same
				// family; keep the family's carrier and retire the rest so a
				// stale row cannot keep a port reserved.
				keep, drop := existing, record
				if record.Kind == carrierKind {
					keep, drop = record, existing
				}
				aggregates[key] = keep
				staleAggregates = append(staleAggregates, drop)
				continue
			}
			aggregates[key] = record
			continue
		}
		if _, duplicate := families[key]; duplicate {
			continue
		}
		bindAddress, port := aggregateEndpoint(spec, owner, carrierKind)
		member := SharedInboundMember{Owner: owner, Kind: carrierKind, BindAddress: bindAddress, Port: port}
		if err := validateSharedMember(member, spec); err != nil {
			return nil, err
		}
		families[key] = member
		familyOrder = append(familyOrder, key)
	}
	sort.Strings(familyOrder)

	anchor, err := s.sharedInboundAnchorGroup(ctx)
	if err != nil {
		return nil, err
	}

	changed := false
	// An aggregate without members would keep the port bound forever, so it is
	// removed as soon as the last member leaves the family.
	for key, record := range aggregates {
		if _, wanted := families[key]; wanted {
			continue
		}
		if err := s.repository.DeleteListener(ctx, record.ID, record.Version); err != nil && err != store.ErrNotFound {
			return nil, mapStoreError(err)
		}
		delete(aggregates, key)
		changed = true
	}
	for _, record := range staleAggregates {
		if err := s.repository.DeleteListener(ctx, record.ID, record.Version); err != nil && err != store.ErrNotFound {
			return nil, mapStoreError(err)
		}
		changed = true
	}

	for _, key := range familyOrder {
		member := families[key]
		record, exists := aggregates[key]
		if !exists {
			if _, err := s.createSharedAggregate(ctx, member, anchor); err != nil {
				return nil, err
			}
			changed = true
			continue
		}
		if record.BindAddress == member.BindAddress && record.Port == member.Port && record.Enabled &&
			record.ProxyGroupID == anchor && record.Kind == member.Kind && record.Name == sharedAggregateName(member.Owner, member.Kind) {
			continue
		}
		record.BindAddress = member.BindAddress
		record.Port = member.Port
		record.ProxyGroupID = anchor
		record.Kind = member.Kind
		record.Name = sharedAggregateName(member.Owner, member.Kind)
		record.Enabled = true
		record.UpdatedAt = s.now().UTC()
		if _, err := s.repository.UpdateListener(ctx, record, record.Version); err != nil {
			return nil, mapStoreError(err)
		}
		changed = true
	}
	// Publish the aggregate rows in the same call: a caller that just created
	// or removed the last member of a family must not have to apply twice, and
	// a settings change that only moves a port must not leave the data plane
	// binding the old one. The apply is content-hash deduplicated upstream, so
	// a no-op convergence is cheap.
	if changed && s.reconciler != nil {
		if err := s.reconciler.Apply(ctx); err != nil {
			return nil, fmt.Errorf("%w: publish shared inbound: %v", ErrApplyFailed, err)
		}
	}
	return s.aggregateListeners(ctx)
}

// BackfillMemberCredentials mints a credential for every standard-family member
// that has none, so a family written by an older build (or by the migration
// that folds dedicated rows into the family) can actually be compiled.
//
// Without it the compiler refuses the entire configuration with
//
//	listener %q has no credentials but the shared inbound routes members by username
//
// which does not merely leave those services unreachable: Manager.Apply records
// a failure instead of publishing, so one credential-less member takes the
// whole data plane down. Measured migrating this installation, 58 of the 60
// rows folded into the family had no credential at all.
//
// It is exported because the migration is not the only writer of family
// membership: anything that rewrites rows directly has to repair them before
// the data plane is asked to compile.
func (s *Service) BackfillMemberCredentials(ctx context.Context) (int, error) {
	records, err := s.repository.ListListeners(ctx)
	if err != nil {
		return 0, err
	}
	minted := 0
	for _, record := range records {
		owner := SharedInboundOwnerOf(record)
		if owner != SharedInboundStandardOwner || IsSharedInboundAggregate(record) {
			continue
		}
		if record.AuthMode == "userpass" && len(record.AuthConfigEncrypted) > 0 {
			continue
		}
		auth, err := newMemberAuth()
		if err != nil {
			return minted, err
		}
		encoded, err := json.Marshal(auth)
		if err != nil {
			return minted, fmt.Errorf("encode generated member credential: %w", err)
		}
		sealed, err := s.cipher.Seal(encoded, associatedData(record.ID))
		if err != nil {
			return minted, fmt.Errorf("encrypt generated member credential: %w", err)
		}
		record.AuthMode = "userpass"
		record.AuthConfigEncrypted = sealed
		record.UpdatedAt = s.now().UTC()
		if _, err := s.repository.UpdateListener(ctx, record, record.Version); err != nil {
			return minted, mapStoreError(err)
		}
		minted++
	}
	return minted, nil
}

// newMemberAuth mints the credential a migrated member needs. The username is
// service-scoped and unique, because the whole membership model rests on the
// username selecting exactly one proxy group.
func newMemberAuth() (Auth, error) {
	id, err := newID()
	if err != nil {
		return Auth{}, err
	}
	token, err := newShareToken()
	if err != nil {
		return Auth{}, err
	}
	// newID returns "listener-<hex>"; only the random part is kept so the
	// published username stays short and does not advertise a row id.
	return Auth{Username: "svc-" + strings.TrimPrefix(id, "listener-"), Password: token}, nil
}

// SharedInboundAggregates returns the aggregate listeners currently published.
func (s *Service) SharedInboundAggregates(ctx context.Context) ([]Listener, error) {
	return s.aggregateListeners(ctx)
}

func (s *Service) aggregateListeners(ctx context.Context) ([]Listener, error) {
	records, err := s.repository.ListListeners(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Listener, 0, 4)
	for _, record := range records {
		if isAggregateRecord(record) {
			result = append(result, fromRecord(record))
		}
	}
	return result, nil
}

// IsSharedInboundAggregate distinguishes the single aggregate row of a family
// from its members. Aggregate rows carry the deterministic managed name and
// never hold a credential of their own, so the compiler must not treat them as
// members when it collects usernames.
func IsSharedInboundAggregate(record store.ListenerRecord) bool {
	return SharedInboundOwnerOf(record) != "" && strings.HasPrefix(record.Name, sharedAggregateNamePrefix)
}

func isAggregateRecord(record store.ListenerRecord) bool {
	return IsSharedInboundAggregate(record)
}

func aggregateEndpoint(spec SharedInboundSpec, owner, kind string) (string, int) {
	if owner == SharedInboundStandardOwner && !isAdvancedKind(kind) {
		return spec.MixedBindAddress, spec.MixedPort
	}
	return "127.0.0.1", spec.WSPort
}

func (s *Service) createSharedAggregate(ctx context.Context, member SharedInboundMember, anchorGroupID string) (Listener, error) {
	id, err := newID()
	if err != nil {
		return Listener{}, err
	}
	transportJSON, endpointJSON, err := sharedAggregateEndpointConfig(member.Kind)
	if err != nil {
		return Listener{}, err
	}
	shareToken, err := newShareToken()
	if err != nil {
		return Listener{}, err
	}
	now := s.now().UTC()
	record := store.ListenerRecord{
		ID:                 id,
		Name:               sharedAggregateName(member.Owner, member.Kind),
		Kind:               member.Kind,
		BindAddress:        member.BindAddress,
		Port:               member.Port,
		ProxyGroupID:       anchorGroupID,
		AuthMode:           "none",
		TransportJSON:      transportJSON,
		PublicEndpointJSON: endpointJSON,
		ShareToken:         shareToken,
		SharedInbound:      member.Owner,
		Enabled:            true,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	created, err := s.repository.CreateListener(ctx, record)
	if err != nil {
		return Listener{}, mapStoreError(err)
	}
	return fromRecord(created), nil
}

// sharedInboundAnchorGroup resolves the proxy group referenced by an aggregate
// listener row. Mihomo's listener schema requires a valid proxy group name and
// the compiler refuses the whole configuration when one is missing, but every
// member is routed by an IN-USER rule that runs before the fallback, so the
// anchor itself never carries traffic. The first enabled group (by id, for a
// stable choice) is therefore safe.
func (s *Service) sharedInboundAnchorGroup(ctx context.Context) (string, error) {
	return s.egressAnchorGroup(ctx)
}

func sharedAggregateName(owner, kind string) string {
	return sharedAggregateNamePrefix + strings.TrimSpace(owner) + "-" + strings.ToLower(strings.TrimSpace(kind))
}

func sharedAggregateEndpointConfig(kind string) (string, string, error) {
	if !isAdvancedKind(kind) {
		return "{}", "{}", nil
	}
	path, err := NormalizeWebSocketPath(SharedInboundRoutePath())
	if err != nil {
		return "", "", err
	}
	encoded, err := json.Marshal(Transport{Type: "ws", WSPath: path})
	if err != nil {
		return "", "", fmt.Errorf("encode shared inbound transport: %w", err)
	}
	return string(encoded), "{}", nil
}

func validateSharedMember(member SharedInboundMember, spec SharedInboundSpec) error {
	switch member.Owner {
	case SharedInboundStandardOwner:
		if member.Kind != SharedInboundCarrierKind(SharedInboundStandardOwner, member.Kind) {
			return fmt.Errorf("%w: the standard shared inbound is carried by the Mixed listener", ErrInvalid)
		}
		if !spec.Enabled {
			return fmt.Errorf("%w: the shared inbound is disabled", ErrInvalid)
		}
	case SharedInboundWebSocketOwner:
		if !isAdvancedKind(member.Kind) {
			return fmt.Errorf("%w: a WebSocket shared-inbound member must be vless, vmess, or trojan", ErrInvalid)
		}
		if !spec.IncludeWebSocket {
			return fmt.Errorf("%w: the shared WebSocket inbound is disabled", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown shared inbound owner %q", ErrInvalid, member.Owner)
	}
	return nil
}
