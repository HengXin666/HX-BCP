package listener

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

var (
	ErrNotFound    = errors.New("listener not found")
	ErrConflict    = errors.New("listener conflict")
	ErrInvalid     = errors.New("invalid listener")
	ErrApplyFailed = errors.New("listener apply failed")
	// ErrUsernameTaken reports a shared-inbound member whose username is
	// already claimed by another member of the same carrier family. The data
	// plane keys membership on the username, so this is a request-level
	// conflict rather than a data-plane failure.
	ErrUsernameTaken = errors.New("shared-inbound username already in use")
)

type Repository interface {
	CreateListener(context.Context, store.ListenerRecord) (store.ListenerRecord, error)
	GetListener(context.Context, string) (store.ListenerRecord, error)
	GetListenerByShareToken(context.Context, string) (store.ListenerRecord, error)
	ListListeners(context.Context) ([]store.ListenerRecord, error)
	UpdateListener(context.Context, store.ListenerRecord, int) (store.ListenerRecord, error)
	RotateListenerShareToken(context.Context, string, string) (store.ListenerRecord, error)
	DeleteListener(context.Context, string, int) error
	GetProxyGroup(context.Context, string) (store.ProxyGroupRecord, error)
	// GetNodeConfig resolves a listener's pinned node so create/update can
	// reject an unknown or retired node at the API boundary instead of at
	// compile time, where one bad row refuses the whole configuration.
	GetNodeConfig(context.Context, string) (store.NodeConfigRecord, error)
	// ListProxyGroups anchors a shared-inbound listener to an enabled group.
	// Every member is routed by an IN-USER rule, so the anchor is only a
	// schema-valid reference and never carries traffic of its own.
	ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error)
}

type Cipher interface {
	Seal([]byte, []byte) ([]byte, error)
	Open([]byte, []byte) ([]byte, error)
}

type Reconciler interface {
	Apply(context.Context) error
}

type Auth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Transport struct {
	Type   string `json:"type"`
	WSPath string `json:"ws_path,omitempty"`
}

// WebSocketPathPrefix reserves a dedicated namespace for public WebSocket
// proxy routes. Keeping the edge namespace separate from /api, /sub and the
// frontend prevents future control-plane routes from colliding with a proxy
// listener path.
const WebSocketPathPrefix = "/__hx-proxy__/"

type PublicEndpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	TLS  bool   `json:"tls"`
}

type Listener struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	BindAddress  string `json:"bind_address"`
	Port         int    `json:"port"`
	ProxyGroupID string `json:"proxy_group_id"`
	// NodeID pins this listener's egress to one node. When set, the listener
	// bypasses ProxyGroupID entirely: its IN-USER rule targets the node's name
	// instead of a group name. Empty means the listener is still served by the
	// group in ProxyGroupID.
	NodeID         string         `json:"node_id,omitempty"`
	AuthConfigured bool           `json:"auth_configured"`
	Transport      Transport      `json:"transport"`
	PublicEndpoint PublicEndpoint `json:"public_endpoint"`
	SharePath      string         `json:"share_path,omitempty"`
	// SharedInbound marks this listener as a member of an aggregate entry
	// point ("standard" for the Mixed family, "websocket" for the
	// VLESS/VMess/Trojan family). Empty keeps the historical dedicated port
	// behaviour. Members still own their own credential, protocol and group.
	SharedInbound string `json:"shared_inbound,omitempty"`
	// SharedInboundAggregate marks the single carrier row of an aggregate
	// family. It is a control-plane resource, not a service: it holds no
	// credential and no group of its own, so the UI must not offer it for
	// editing or deletion.
	SharedInboundAggregate bool `json:"shared_inbound_aggregate,omitempty"`
	// DockerAccessFor names the source listener when this row is a Docker
	// entry (a companion created for container access). Consumers that build
	// egress lists from listeners must skip or special-case these rows: they
	// bind a Docker bridge or ride the shared entry, never 127.0.0.1.
	DockerAccessFor string `json:"docker_access_for,omitempty"`
	// DockerAccessWithheld marks a Docker entry the running data plane does
	// not serve right now (source disabled, bridge address missing). It is
	// enabled but not dialable; consumers must not offer it as an egress.
	DockerAccessWithheld bool      `json:"docker_access_withheld,omitempty"`
	Enabled              bool      `json:"enabled"`
	Version              int       `json:"version"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type CreateRequest struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	BindAddress  string `json:"bind_address"`
	Port         int    `json:"port"`
	ProxyGroupID string `json:"proxy_group_id"`
	// NodeID is the preferred way to name the egress of a service: it pins the
	// listener to exactly one node. Leave ProxyGroupID empty when using it.
	//
	// When both are set NodeID wins, because "this service goes out through
	// that node" is strictly more specific than "that group". The group is
	// kept only because the row still has to satisfy the NOT NULL foreign key.
	NodeID         string         `json:"node_id,omitempty"`
	Auth           *Auth          `json:"auth,omitempty"`
	Transport      Transport      `json:"transport,omitempty"`
	PublicEndpoint PublicEndpoint `json:"public_endpoint,omitempty"`
	// SharedInbound marks the listener as a member of an aggregate entry point.
	SharedInbound string `json:"shared_inbound,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

type UpdateRequest struct {
	Version      int    `json:"version"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	BindAddress  string `json:"bind_address"`
	Port         int    `json:"port"`
	ProxyGroupID string `json:"proxy_group_id"`
	// NodeID pins the listener to one node; see CreateRequest.NodeID. When set
	// the listener stops using ProxyGroupID for egress, but the two must agree
	// on nothing else: a pinned listener may keep any group as its fallback
	// anchor, and one that drops the pin falls back to ProxyGroupID.
	NodeID         string         `json:"node_id,omitempty"`
	Auth           *Auth          `json:"auth,omitempty"`
	Transport      Transport      `json:"transport,omitempty"`
	PublicEndpoint PublicEndpoint `json:"public_endpoint,omitempty"`
	ClearAuth      bool           `json:"clear_auth,omitempty"`
	// SharedInbound marks the listener as a member of an aggregate entry point.
	SharedInbound string `json:"shared_inbound,omitempty"`
	Enabled       bool   `json:"enabled"`
}

type Service struct {
	repository Repository
	cipher     Cipher
	reconciler Reconciler
	now        func() time.Time
	// sharedEndpoints resolves the shared-inbound configuration at export
	// time. It is optional so packages that only use dedicated listeners can
	// skip it.
	sharedEndpoints SharedEndpointProvider
}

// SetSharedEndpointProvider installs the resolver used by subscription export
// to advertise the aggregate entry point instead of an internal port.
func (s *Service) SetSharedEndpointProvider(provider SharedEndpointProvider) {
	s.sharedEndpoints = provider
}

func NewService(repository Repository, cipher Cipher, reconciler Reconciler) (*Service, error) {
	if repository == nil {
		return nil, errors.New("listener repository is required")
	}
	if cipher == nil {
		return nil, errors.New("listener cipher is required")
	}
	if reconciler == nil {
		return nil, errors.New("listener reconciler is required")
	}
	return &Service{repository: repository, cipher: cipher, reconciler: reconciler, now: time.Now}, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (Listener, error) {
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	id, err := newID()
	if err != nil {
		return Listener{}, err
	}
	normalized, err := s.normalize(ctx, id, request.Name, request.Kind, request.BindAddress, request.Port, request.ProxyGroupID, request.NodeID, request.Auth, nil, request.Transport, request.PublicEndpoint, enabled)
	if err != nil {
		return Listener{}, err
	}
	sharedInbound, err := s.sharedInboundOwnerFor(request.SharedInbound, normalized.Kind)
	if err != nil {
		return Listener{}, err
	}
	// A standard member is selected by username, so it cannot be published
	// without one. Minting the missing credential here — instead of rejecting
	// the call — is what keeps "joined the shared entry point" and "has a usable
	// credential" the same condition for every caller, including the raw
	// management API which never had to think about credentials before.
	// A member does not own the socket, so it must not keep the port the caller
	// happened to name: the row has to describe the endpoint that is actually
	// served. This mirrors what the proxy-service path already does.
	s.convergeMemberEndpoint(&normalized, sharedInbound)
	if err := s.assureMemberCredentials(&normalized, sharedInbound); err != nil {
		return Listener{}, err
	}
	if err := s.assureUniqueMemberUsername(ctx, &normalized, sharedInbound); err != nil {
		return Listener{}, err
	}
	shareToken, err := newShareToken()
	if err != nil {
		return Listener{}, err
	}
	now := s.now().UTC()
	record := store.ListenerRecord{
		ID:                  id,
		Name:                normalized.Name,
		Kind:                normalized.Kind,
		BindAddress:         normalized.BindAddress,
		Port:                normalized.Port,
		ProxyGroupID:        normalized.ProxyGroupID,
		NodeID:              normalized.NodeID,
		AuthMode:            normalized.AuthMode,
		AuthConfigEncrypted: normalized.AuthConfigEncrypted,
		TransportJSON:       normalized.TransportJSON,
		PublicEndpointJSON:  normalized.PublicEndpointJSON,
		ShareToken:          shareToken,
		SharedInbound:       sharedInbound,
		Enabled:             normalized.Enabled,
		Version:             1,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	created, err := s.repository.CreateListener(ctx, record)
	if err != nil {
		return Listener{}, mapStoreError(err)
	}
	if err := s.reconciler.Apply(ctx); err != nil {
		// The row is already persisted, so an apply failure would otherwise
		// leave a listener that no group is using and that no caller knows the
		// id of. Callers that create a listener as part of a larger unit (the
		// proxy service, quick start) cannot clean up a record they cannot
		// name, so the create must undo itself. This mirrors Update, which
		// restores the previous record for the same reason.
		if rollbackErr := s.repository.DeleteListener(ctx, created.ID, created.Version); rollbackErr != nil {
			return fromRecord(created), fmt.Errorf("%w: %v; removing the created listener record also failed: %v", ErrApplyFailed, err, rollbackErr)
		}
		return Listener{}, fmt.Errorf("%w: %v (the created listener was removed again)", ErrApplyFailed, err)
	}
	return fromRecord(created), nil
}

func (s *Service) Get(ctx context.Context, id string) (Listener, error) {
	record, err := s.repository.GetListener(ctx, id)
	if err != nil {
		return Listener{}, mapStoreError(err)
	}
	return fromRecord(record), nil
}

func (s *Service) List(ctx context.Context) ([]Listener, error) {
	records, err := s.repository.ListListeners(ctx)
	if err != nil {
		return nil, err
	}
	listeners := make([]Listener, 0, len(records))
	for _, record := range records {
		listeners = append(listeners, fromRecord(record))
	}
	return listeners, nil
}

func (s *Service) Update(ctx context.Context, id string, request UpdateRequest) (Listener, error) {
	if request.Version < 1 {
		return Listener{}, fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	existing, err := s.repository.GetListener(ctx, id)
	if err != nil {
		return Listener{}, mapStoreError(err)
	}
	currentAuth := existing.AuthConfigEncrypted
	if request.ClearAuth {
		currentAuth = nil
	}
	normalized, err := s.normalize(ctx, id, request.Name, request.Kind, request.BindAddress, request.Port, request.ProxyGroupID, request.NodeID, request.Auth, currentAuth, request.Transport, request.PublicEndpoint, request.Enabled)
	if err != nil {
		return Listener{}, err
	}
	sharedInbound, err := s.sharedInboundOwnerFor(request.SharedInbound, normalized.Kind)
	if err != nil {
		return Listener{}, err
	}
	s.convergeMemberEndpoint(&normalized, sharedInbound)
	if err := s.assureMemberCredentials(&normalized, sharedInbound); err != nil {
		return Listener{}, err
	}
	if err := s.assureUniqueMemberUsername(ctx, &normalized, sharedInbound); err != nil {
		return Listener{}, err
	}
	// Capture the pre-update record (with a defensive copy of the auth
	// ciphertext slice) so a data plane apply failure can restore it.
	original := existing
	original.AuthConfigEncrypted = append([]byte(nil), existing.AuthConfigEncrypted...)
	original.UpdatedAt = existing.UpdatedAt

	existing.Name = normalized.Name
	existing.Kind = normalized.Kind
	existing.BindAddress = normalized.BindAddress
	existing.Port = normalized.Port
	existing.ProxyGroupID = normalized.ProxyGroupID
	existing.NodeID = normalized.NodeID
	existing.AuthMode = normalized.AuthMode
	existing.AuthConfigEncrypted = normalized.AuthConfigEncrypted
	existing.TransportJSON = normalized.TransportJSON
	existing.PublicEndpointJSON = normalized.PublicEndpointJSON
	existing.SharedInbound = sharedInbound
	existing.Enabled = normalized.Enabled
	existing.UpdatedAt = s.now().UTC()
	updated, err := s.repository.UpdateListener(ctx, existing, request.Version)
	if err != nil {
		return Listener{}, mapStoreError(err)
	}
	if err := s.reconciler.Apply(ctx); err != nil {
		// Do not leave the database ahead of the data plane: an apply failure
		// must not be followed by a retry that hits an optimistic-lock
		// conflict or by a reconcile that recompiles a config Mihomo already
		// refused. Restore the previous record so the edit is rejected
		// atomically.
		if _, rollbackErr := s.repository.UpdateListener(ctx, original, updated.Version); rollbackErr != nil {
			return fromRecord(updated), fmt.Errorf("%w: %v; restoring the previous listener record also failed: %v", ErrApplyFailed, err, rollbackErr)
		}
		return Listener{}, fmt.Errorf("%w: %v (database restored to the previous listener configuration)", ErrApplyFailed, err)
	}
	return fromRecord(updated), nil
}

func (s *Service) Delete(ctx context.Context, id string, version int) error {
	if version < 1 {
		return fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	if err := s.repository.DeleteListener(ctx, id, version); err != nil {
		return mapStoreError(err)
	}
	if err := s.reconciler.Apply(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}
	return nil
}

type normalizedListener struct {
	// selfID is the row id this normalisation produces. It is the associated
	// data of the credential envelope, so a credential minted during
	// normalisation must be sealed against the same id the record is written
	// with.
	selfID              string
	Name                string
	Kind                string
	BindAddress         string
	Port                int
	ProxyGroupID        string
	NodeID              string
	AuthMode            string
	AuthConfigEncrypted []byte
	TransportJSON       string
	PublicEndpointJSON  string
	Enabled             bool
}

func (s *Service) normalize(
	ctx context.Context,
	id string,
	name string,
	kind string,
	bindAddress string,
	port int,
	groupID string,
	nodeID string,
	auth *Auth,
	existingAuth []byte,
	transport Transport,
	publicEndpoint PublicEndpoint,
	enabled bool,
) (normalizedListener, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 128 {
		return normalizedListener{}, fmt.Errorf("%w: name must contain 1 to 128 characters", ErrInvalid)
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !supportedKind(kind) {
		return normalizedListener{}, fmt.Errorf("%w: kind must be http, socks, mixed, vless, vmess, or trojan", ErrInvalid)
	}
	bindAddress = strings.TrimSpace(bindAddress)
	if bindAddress == "" {
		bindAddress = "127.0.0.1"
	}
	ip := net.ParseIP(bindAddress)
	if ip == nil {
		return normalizedListener{}, fmt.Errorf("%w: bind_address must be an explicit IP", ErrInvalid)
	}
	if port < 1 || port > 65535 {
		return normalizedListener{}, fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalid)
	}
	// Egress resolution. A listener is addressed either by the one node it is
	// pinned to or by the group that selects for it, and the node pin wins: it
	// is the more specific statement, and it is what lets a caller reach a
	// single node without first materialising a one-member group for it.
	//
	nodeID = strings.TrimSpace(nodeID)
	groupID = strings.TrimSpace(groupID)
	if nodeID != "" {
		node, err := s.repository.GetNodeConfig(ctx, nodeID)
		if errors.Is(err, store.ErrNotFound) {
			return normalizedListener{}, fmt.Errorf("%w: node does not exist", ErrInvalid)
		}
		if err != nil {
			return normalizedListener{}, err
		}
		if node.LifecycleState == "disabled" || node.LifecycleState == "retired" {
			return normalizedListener{}, fmt.Errorf("%w: node is %s and cannot carry traffic", ErrInvalid, node.LifecycleState)
		}
		// The row still has to name a live group: proxy_group_id is NOT NULL and
		// the aggregate rows use the group as their fallback anchor. The pin is
		// what carries traffic, so a pinned listener never requires the caller to
		// name a group — it inherits the same anchor a shared-inbound row uses.
		if groupID == "" {
			anchor, err := s.egressAnchorGroup(ctx)
			if err != nil {
				return normalizedListener{}, err
			}
			groupID = anchor
		}
	}
	if groupID == "" {
		return normalizedListener{}, fmt.Errorf("%w: proxy_group_id or node_id is required", ErrInvalid)
	}
	group, err := s.repository.GetProxyGroup(ctx, groupID)
	if errors.Is(err, store.ErrNotFound) {
		return normalizedListener{}, fmt.Errorf("%w: proxy group does not exist", ErrInvalid)
	}
	if err != nil {
		return normalizedListener{}, err
	}
	if !group.Enabled {
		return normalizedListener{}, fmt.Errorf("%w: proxy group is disabled", ErrInvalid)
	}

	authMode := "none"
	authEncrypted := append([]byte(nil), existingAuth...)
	if auth != nil {
		auth.Username = strings.TrimSpace(auth.Username)
		if auth.Username == "" || auth.Password == "" {
			return normalizedListener{}, fmt.Errorf("%w: username and password must both be set", ErrInvalid)
		}
		if len(auth.Username) > 128 || len(auth.Password) > 512 {
			return normalizedListener{}, fmt.Errorf("%w: listener credentials are too long", ErrInvalid)
		}
		if (kind == "vless" || kind == "vmess") && !validUUID(auth.Password) {
			return normalizedListener{}, fmt.Errorf("%w: %s credential must be a UUID", ErrInvalid, kind)
		}
		encoded, err := json.Marshal(auth)
		if err != nil {
			return normalizedListener{}, fmt.Errorf("encode listener auth: %w", err)
		}
		authEncrypted, err = s.cipher.Seal(encoded, associatedData(id))
		if err != nil {
			return normalizedListener{}, fmt.Errorf("encrypt listener auth: %w", err)
		}
	}
	if len(authEncrypted) > 0 {
		authMode = "userpass"
	}
	advanced := isAdvancedKind(kind)
	if advanced && !ip.IsLoopback() {
		return normalizedListener{}, fmt.Errorf("%w: WebSocket protocol listeners must bind to a loopback address behind a reverse proxy", ErrInvalid)
	}
	if advanced && authMode == "none" {
		return normalizedListener{}, fmt.Errorf("%w: %s listeners require credentials", ErrInvalid, kind)
	}
	if !ip.IsLoopback() && authMode == "none" {
		return normalizedListener{}, fmt.Errorf("%w: non-loopback listeners require username/password authentication", ErrInvalid)
	}
	transportJSON, publicEndpointJSON, err := normalizeEndpointConfig(advanced, transport, publicEndpoint, port)
	if err != nil {
		return normalizedListener{}, err
	}
	return normalizedListener{
		selfID:              id,
		Name:                name,
		Kind:                kind,
		BindAddress:         ip.String(),
		Port:                port,
		ProxyGroupID:        groupID,
		NodeID:              nodeID,
		AuthMode:            authMode,
		AuthConfigEncrypted: authEncrypted,
		TransportJSON:       transportJSON,
		PublicEndpointJSON:  publicEndpointJSON,
		Enabled:             enabled,
	}, nil
}

func fromRecord(record store.ListenerRecord) Listener {
	sharePath := ""
	if record.ShareToken != "" {
		sharePath = "/sub/" + record.ShareToken
	}
	var transport Transport
	var publicEndpoint PublicEndpoint
	_ = json.Unmarshal([]byte(record.TransportJSON), &transport)
	_ = json.Unmarshal([]byte(record.PublicEndpointJSON), &publicEndpoint)
	if isAdvancedKind(record.Kind) {
		if normalizedPath, err := NormalizeWebSocketPath(transport.WSPath); err == nil {
			transport.WSPath = normalizedPath
		}
	}
	return Listener{
		ID:                     record.ID,
		Name:                   record.Name,
		Kind:                   record.Kind,
		BindAddress:            record.BindAddress,
		Port:                   record.Port,
		ProxyGroupID:           record.ProxyGroupID,
		NodeID:                 record.NodeID,
		AuthConfigured:         record.AuthMode != "none" && len(record.AuthConfigEncrypted) > 0,
		Transport:              transport,
		PublicEndpoint:         publicEndpoint,
		SharePath:              sharePath,
		SharedInbound:          record.SharedInbound,
		SharedInboundAggregate: IsSharedInboundAggregate(record),
		Enabled:                record.Enabled,
		Version:                record.Version,
		CreatedAt:              record.CreatedAt,
		UpdatedAt:              record.UpdatedAt,
	}
}

// egressAnchorGroup resolves the proxy group a listener row falls back on when
// its egress is named directly.
//
// Mihomo's listener schema carries a proxy group name and the compiler refuses a
// configuration whose listener names a missing group, so every row has to
// reference a live one even when an IN-USER rule is what actually routes its
// traffic. The first enabled group (by id, for a stable choice) is therefore
// safe as an anchor: it never carries the pinned listener's traffic, and
// requiring callers to name a group would defeat the point of pinning a node.
//
// Shared-inbound aggregate rows already rely on exactly this rule, so it lives
// here once and both paths call it.
func (s *Service) egressAnchorGroup(ctx context.Context) (string, error) {
	groups, err := s.repository.ListProxyGroups(ctx)
	if err != nil {
		return "", err
	}
	anchor := ""
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		if anchor == "" || group.ID < anchor {
			anchor = group.ID
		}
	}
	if anchor == "" {
		return "", fmt.Errorf("%w: create and enable at least one proxy group before naming a node directly", ErrInvalid)
	}
	return anchor, nil
}

// SupportedKinds lists the listener kinds the control plane can publish. It is
// the single source of truth for both the validator and the machine-readable
// capability catalog, so the list an operator reads can never drift from the
// list the validator accepts.
func SupportedKinds() []string {
	return []string{"http", "socks", "mixed", "vless", "vmess", "trojan"}
}

// SupportedTransports lists the transports a listener may use. "tcp" is a
// directly dialable port; "ws" is a proxy protocol carried over WebSocket
// behind the reverse proxy and is only valid for the advanced kinds.
func SupportedTransports() []string {
	return []string{"tcp", "ws"}
}

func supportedKind(kind string) bool {
	return slices.Contains(SupportedKinds(), kind)
}

// IsAdvancedKind reports whether a listener kind speaks a proxy protocol over
// WebSocket behind the reverse proxy instead of a directly dialable TCP port.
//
// Exported because the programmatic node listing derives "is this node
// browser-dialable" from the same predicate the export path uses; a second copy
// of this rule is exactly how the listing and the subscription would drift.
func IsAdvancedKind(kind string) bool {
	return kind == "vless" || kind == "vmess" || kind == "trojan"
}

func isAdvancedKind(kind string) bool {
	return IsAdvancedKind(kind)
}

func normalizeEndpointConfig(advanced bool, transport Transport, endpoint PublicEndpoint, listenerPort int) (string, string, error) {
	if !advanced && strings.TrimSpace(endpoint.Host) == "" {
		return "{}", "{}", nil
	}
	if !advanced {
		endpoint.Host = strings.ToLower(strings.TrimSpace(endpoint.Host))
		if !validEndpointHost(endpoint.Host) {
			return "", "", fmt.Errorf("%w: public_endpoint.host must be a public IP address or domain name", ErrInvalid)
		}
		if endpoint.Port == 0 {
			endpoint.Port = listenerPort
		}
		if endpoint.Port < 1 || endpoint.Port > 65535 {
			return "", "", fmt.Errorf("%w: public_endpoint.port must be between 1 and 65535", ErrInvalid)
		}
		encoded, err := json.Marshal(endpoint)
		if err != nil {
			return "", "", fmt.Errorf("encode listener public endpoint: %w", err)
		}
		return "{}", string(encoded), nil
	}
	transport.Type = strings.ToLower(strings.TrimSpace(transport.Type))
	if transport.Type == "" {
		transport.Type = "ws"
	}
	if transport.Type != "ws" {
		return "", "", fmt.Errorf("%w: WebSocket transport only supports ws", ErrInvalid)
	}
	normalizedPath, err := NormalizeWebSocketPath(transport.WSPath)
	if err != nil {
		return "", "", err
	}
	transport.WSPath = normalizedPath
	endpoint.Host = strings.ToLower(strings.TrimSpace(endpoint.Host))
	if !validPublicHost(endpoint.Host) {
		return "", "", fmt.Errorf("%w: public_endpoint.host must be a valid domain name", ErrInvalid)
	}
	if endpoint.Port == 0 {
		endpoint.Port = 443
	}
	if endpoint.Port < 1 || endpoint.Port > 65535 || !endpoint.TLS {
		return "", "", fmt.Errorf("%w: WebSocket public endpoint must use TLS and a valid port", ErrInvalid)
	}
	transportEncoded, err := json.Marshal(transport)
	if err != nil {
		return "", "", fmt.Errorf("encode listener transport: %w", err)
	}
	endpointEncoded, err := json.Marshal(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("encode listener public endpoint: %w", err)
	}
	return string(transportEncoded), string(endpointEncoded), nil
}

// ValidatePublicEndpoint validates the endpoint metadata used by a standard
// HTTP, SOCKS5, or Mixed listener without changing any stored state.
func ValidatePublicEndpoint(endpoint PublicEndpoint, listenerPort int) error {
	_, _, err := normalizeEndpointConfig(false, Transport{}, endpoint, listenerPort)
	return err
}

func validEndpointHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsUnspecified()
	}
	return validPublicHost(host)
}

// NormalizeWebSocketPath maps user-facing paths into the reserved edge
// namespace. Existing paths from older releases are upgraded lazily, so a
// database upgrade does not invalidate a listener before its next edit.
func NormalizeWebSocketPath(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" || !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("%w: ws_path must begin with /", ErrInvalid)
	}
	if raw == strings.TrimSuffix(WebSocketPathPrefix, "/") || raw == WebSocketPathPrefix {
		return "", fmt.Errorf("%w: ws_path must contain a route after %s", ErrInvalid, WebSocketPathPrefix)
	}
	if strings.ContainsAny(raw, "?#\\") {
		return "", fmt.Errorf("%w: ws_path must be a path without query, fragment, or backslash", ErrInvalid)
	}
	normalized := raw
	if !strings.HasPrefix(normalized, WebSocketPathPrefix) {
		normalized = WebSocketPathPrefix + strings.TrimPrefix(normalized, "/")
	}
	if len(normalized) > 256 || !validNormalizedWebSocketPath(normalized) {
		return "", fmt.Errorf("%w: ws_path must use the %s prefix and contain only safe path segments", ErrInvalid, WebSocketPathPrefix)
	}
	return normalized, nil
}

func validNormalizedWebSocketPath(value string) bool {
	if !strings.HasPrefix(value, WebSocketPathPrefix) {
		return false
	}
	suffix := strings.TrimPrefix(value, WebSocketPathPrefix)
	if suffix == "" || strings.Contains(suffix, "//") || strings.HasSuffix(suffix, "/") {
		return false
	}
	for _, segment := range strings.Split(suffix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, character := range segment {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') &&
				character != '-' && character != '_' && character != '.' && character != '~' {
				return false
			}
		}
	}
	return true
}

func validPublicHost(host string) bool {
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

// ValidUUID reports whether a value is a canonical UUID. The data-plane
// compiler validates shared-inbound credentials with the same rule the service
// enforces at write time.
func ValidUUID(value string) bool { return validUUID(value) }

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range strings.ToLower(value) {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func associatedData(id string) []byte {
	return []byte("listener:" + id)
}

// normalizeSharedInboundOwner validates the aggregate family marker so an
// unknown value can never reach the database CHECK constraint.
// assureMemberCredentials mints a credential for a listener that just joined a
// username-routed family without one.
//
// The shared inbound identifies a member by its IN-USER rule, and the compiler
// refuses the entire configuration when a member holds no credential: measured
// on this build, marking an unauthenticated service as a member produces
//
//	listener "ioa-vm-mixed" has no credentials but the shared inbound routes
//	members by username
//
// and Manager.Apply then records a failure instead of publishing, so one
// credential-less service takes the *whole* data plane down rather than only
// itself. Minting here keeps the members it is always safe to mint: the
// credential is returned to the caller in the listener's own subscription.
func (s *Service) assureMemberCredentials(normalized *normalizedListener, owner string) error {
	if owner != SharedInboundStandardOwner {
		// Only the standard family is minted automatically. An explicitly
		// marked WebSocket member still has to bring its own UUID, because the
		// addressable credential of a VLESS/VMess service is its UUID.
		return nil
	}
	if normalized.AuthMode == "userpass" && len(normalized.AuthConfigEncrypted) > 0 {
		return nil
	}
	id, err := newID()
	if err != nil {
		return err
	}
	token, err := newShareToken()
	if err != nil {
		return err
	}
	// The username is service-scoped and unique: membership rests on the
	// username selecting exactly one proxy group.
	auth := Auth{Username: "svc-" + strings.TrimPrefix(id, "listener-"), Password: token}
	encoded, err := json.Marshal(auth)
	if err != nil {
		return fmt.Errorf("encode generated member credential: %w", err)
	}
	sealed, err := s.cipher.Seal(encoded, associatedData(normalized.selfID))
	if err != nil {
		return fmt.Errorf("encrypt generated member credential: %w", err)
	}
	normalized.AuthMode = "userpass"
	normalized.AuthConfigEncrypted = sealed
	return nil
}

// assureUniqueMemberUsername rejects a member whose username another member of
// the same carrier family already claims.
//
// Membership is demultiplexed by IN-USER, so the compiler refuses the entire
// configuration when two members of one family share a username:
//
//	two shared-inbound services for the standard family use the same username "hx-user"
//
// Without this check the caller learns about the collision from an apply
// failure — after the row was written and rolled back again — and the HTTP
// status says the data plane failed although the request was the problem.
//
// The compiler keeps its own check: it is the only boundary that also covers
// rows written by migrations and by callers that bypass this service. Both
// sides must agree on what counts as a claim, so this compares exactly what the
// compiler compiles: enabled members whose group is enabled, keyed by the
// carrier that publishes them.
//
// See .agents/notes/implemented/bug-fix/2026-10-17-shared-inbound-username-unique-at-write.md.
func (s *Service) assureUniqueMemberUsername(ctx context.Context, normalized *normalizedListener, owner string) error {
	if owner == "" || !normalized.Enabled || normalized.AuthMode != "userpass" || len(normalized.AuthConfigEncrypted) == 0 {
		return nil
	}
	username, err := s.memberUsername(normalized.AuthConfigEncrypted, normalized.selfID)
	if err != nil {
		return err
	}
	records, err := s.repository.ListListeners(ctx)
	if err != nil {
		return err
	}
	groups, err := s.repository.ListProxyGroups(ctx)
	if err != nil {
		return err
	}
	enabledGroups := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if group.Enabled {
			enabledGroups[group.ID] = struct{}{}
		}
	}
	key := SharedInboundCarrierKey(owner, normalized.Kind)
	for _, record := range records {
		if record.ID == normalized.selfID || !record.Enabled {
			continue
		}
		if SharedInboundCarrierKey(SharedInboundOwnerOf(record), record.Kind) != key {
			continue
		}
		if _, ok := enabledGroups[record.ProxyGroupID]; !ok {
			continue
		}
		if record.AuthMode != "userpass" || len(record.AuthConfigEncrypted) == 0 {
			continue
		}
		other, err := s.memberUsername(record.AuthConfigEncrypted, record.ID)
		if err != nil {
			return err
		}
		if other == username {
			return fmt.Errorf(
				"%w: username %q is already used by listener %q on the %s shared inbound; choose a different username",
				ErrUsernameTaken, username, record.Name, owner,
			)
		}
	}
	return nil
}

// memberUsername decrypts the credential of a stored member so the caller can
// compare it with the compiler's own view (which trims the value).
func (s *Service) memberUsername(sealed []byte, id string) (string, error) {
	plaintext, err := s.cipher.Open(sealed, associatedData(id))
	if err != nil {
		return "", fmt.Errorf("decrypt listener %q auth: %w", id, err)
	}
	var auth Auth
	if err := json.Unmarshal(plaintext, &auth); err != nil {
		return "", fmt.Errorf("decode listener %q auth: %w", id, err)
	}
	return strings.TrimSpace(auth.Username), nil
}

func normalizeSharedInboundOwner(owner string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(owner))
	if trimmed == "" {
		return "", nil
	}
	if !IsSharedInboundOwner(trimmed) {
		return "", fmt.Errorf("%w: shared_inbound must be %q, %q, or empty", ErrInvalid, SharedInboundStandardOwner, SharedInboundWebSocketOwner)
	}
	return trimmed, nil
}

// sharedInboundOwnerFor resolves the family a listener ends up in when the
// request is applied, treating an omitted marker as the membership default for
// the protocol rather than as "reserve a dedicated port".
//
// The reason the default had to move is recorded in
// .agents/notes/implemented/architecture/2026-09-26-shared-inbound-by-default.md:
// leaving it as "empty means dedicated" made every caller that did not know
// about shared inbounds produce another bound port, which is exactly the model
// the control plane promises not to use.
//
// The default is resolved from the *live* shared-inbound configuration, not
// from the protocol alone. Joining a family is only meaningful while the family
// is compiled: with the shared inbound switched off, a row marked as a member
// is skipped by the direct-listener path (see the compiler's owner != "" guard)
// and published nowhere, so a protocol-only default would turn "no port of its
// own" into "not published at all". A caller that passes the marker explicitly
// still gets it, because then the setting is the caller's decision to make.
func (s *Service) sharedInboundOwnerFor(requested, kind string) (string, error) {
	normalized, err := normalizeSharedInboundOwner(requested)
	if err != nil {
		return "", err
	}
	if normalized != "" {
		return normalized, nil
	}
	owner := DefaultSharedInboundOwnerForKind(kind)
	if owner == "" {
		return "", nil
	}
	if _, ok := s.sharedFamilySpec(owner); !ok {
		return "", nil
	}
	return owner, nil
}

// sharedFamilySpec reports the compiled shared-inbound configuration when the
// family carrying this protocol is currently published.
func (s *Service) sharedFamilySpec(owner string) (SharedInboundSpec, bool) {
	if s.sharedEndpoints == nil {
		// Without a resolver the service cannot know the deployment's shared
		// configuration, so it must not invent a membership the data plane may
		// not compile.
		return SharedInboundSpec{}, false
	}
	spec, ok := s.sharedEndpoints()
	if !ok {
		return SharedInboundSpec{}, false
	}
	switch owner {
	case SharedInboundStandardOwner:
		return spec, spec.Enabled
	case SharedInboundWebSocketOwner:
		return spec, spec.IncludeWebSocket
	default:
		return SharedInboundSpec{}, false
	}
}

// convergeMemberEndpoint moves a listener that became an aggregate member onto
// its family's entry point.
//
// A member does not own the socket: the family's carrier binds the port and the
// member is selected by username. Storing the caller's own port on a member row
// would leave the row describing an endpoint nothing listens on, which is the
// same "the control plane advertises a port that is not the entry point" defect
// this change exists to remove.
//
// The caller's port is therefore only meaningful for a listener that keeps a
// dedicated endpoint, which is why this runs after the family is resolved.
func (s *Service) convergeMemberEndpoint(normalized *normalizedListener, owner string) {
	if owner == "" {
		return
	}
	spec, ok := s.sharedFamilySpec(owner)
	if !ok {
		return
	}
	carrierKind := SharedInboundCarrierKind(owner, normalized.Kind)
	bindAddress, port := aggregateEndpoint(spec, owner, carrierKind)
	if bindAddress == "" || port == 0 {
		return
	}
	normalized.BindAddress = bindAddress
	normalized.Port = port
}

func mapStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}

func newID() (string, error) {
	var buffer [12]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate listener id: %w", err)
	}
	return "listener-" + hex.EncodeToString(buffer[:]), nil
}
