package proxyservice

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

var ErrCreateFailed = errors.New("create proxy service failed")

type GroupService interface {
	Create(context.Context, proxygroup.CreateRequest) (proxygroup.Group, error)
	Get(context.Context, string) (proxygroup.Group, error)
	Update(context.Context, string, proxygroup.UpdateRequest) (proxygroup.Group, error)
	Delete(context.Context, string, int) error
}

type ListenerService interface {
	Create(context.Context, listener.CreateRequest) (listener.Listener, error)
	Delete(context.Context, string, int) error
	Get(context.Context, string) (listener.Listener, error)
	Update(context.Context, string, listener.UpdateRequest) (listener.Listener, error)
	// EnsureSharedInbounds converges the aggregate listener rows for the
	// requested protocol families and returns every managed aggregate.
	EnsureSharedInbounds(context.Context, listener.SharedInboundSpec, []store.ProxyGroupRecord, []listener.SharedInboundMember) ([]listener.Listener, error)
}

// GroupReader lists every proxy group; the shared-inbound anchor needs the
// enabled ones.
type GroupReader interface {
	List(context.Context) ([]proxygroup.Group, error)
}

// SettingsReader exposes the global settings that decide whether services are
// published per port or through the shared inbound.
type SettingsReader interface {
	Get(context.Context) (systemsettings.Settings, error)
}

type CreateRequest struct {
	Name          string                `json:"name"`
	Strategy      string                `json:"strategy"`
	SourceSpec    proxygroup.SourceSpec `json:"source_spec"`
	EmptyBehavior string                `json:"empty_behavior,omitempty"`
	Enabled       *bool                 `json:"enabled,omitempty"`
	// DialerProxyGroup tunnels this service's egress through another group.
	DialerProxyGroup string                `json:"dialer_proxy_group_id,omitempty"`
	Listener         ListenerCreateRequest `json:"listener"`
}

type ListenerCreateRequest struct {
	Name           string                  `json:"name"`
	Kind           string                  `json:"kind"`
	BindAddress    string                  `json:"bind_address"`
	Port           int                     `json:"port"`
	Auth           *listener.Auth          `json:"auth,omitempty"`
	Transport      listener.Transport      `json:"transport,omitempty"`
	PublicEndpoint listener.PublicEndpoint `json:"public_endpoint,omitempty"`
	Enabled        *bool                   `json:"enabled,omitempty"`
	// ProxyGroupID attaches this entry point to an already existing proxy group
	// instead of the one Create would have built alongside it. When set, Create
	// creates no group at all: the named group keeps its own strategy, members
	// and failure policy, and the service is only a new entry point onto it.
	//
	// The name is deliberately the same as on listener.CreateRequest, where it
	// means the same thing. A top-level group_id was rejected: UpdateRequest
	// already uses that name for "the service being updated", and this
	// repository has paid before for letting one name carry two roles.
	//
	// See .agents/notes/implemented/feature/2026-10-14-reuse-existing-group-for-an-entry.md.
	ProxyGroupID string `json:"proxy_group_id,omitempty"`
	// NodeID publishes this service on exactly one node and creates no group of
	// its own: the listener pins the node and fills the row's NOT NULL group
	// column with the family anchor.
	//
	// This exists so "one service, one node" is one call. Before it, the only
	// way to reach a single node from this endpoint was to build a one-member
	// group around it — the exact shape the group service now refuses. A remedy
	// that requires a second endpoint is a remedy nobody takes.
	//
	// Omit name, strategy, source_spec, enabled, empty_behavior and
	// dialer_proxy_group_id when using it: a pinned service has no group policy,
	// and accepting those fields while ignoring them is the silent divergence
	// this repository has already paid for.
	//
	// See .agents/notes/implemented/architecture/2026-10-18-a-group-is-not-a-node-handle.md.
	NodeID string `json:"node_id,omitempty"`
}

type ListenerUpdateRequest struct {
	Name           string                  `json:"name"`
	Kind           string                  `json:"kind"`
	BindAddress    string                  `json:"bind_address"`
	Port           int                     `json:"port"`
	Auth           *listener.Auth          `json:"auth,omitempty"`
	ClearAuth      bool                    `json:"clear_auth,omitempty"`
	Transport      listener.Transport      `json:"transport,omitempty"`
	PublicEndpoint listener.PublicEndpoint `json:"public_endpoint,omitempty"`
	Enabled        *bool                   `json:"enabled,omitempty"`
	// NodeID moves this service's pin to another node. Omit it to keep the
	// stored pin: a composite update that does not mention the pin must not
	// release it, because "rename this service" would otherwise silently move
	// its egress onto whichever group the row happens to anchor to.
	//
	// See .agents/notes/implemented/simplification/2026-10-01-service-shape-follows-source-class.md.
	NodeID string `json:"node_id,omitempty"`
	// ClearNodePin releases the pin explicitly and hands the service back to
	// its group. It exists because an omitted NodeID means "keep", so the two
	// intentions cannot share one representation — the empty string is a
	// meaningful value on this field, and the row that stores it agrees
	// (listeners.node_id is NULLable and read through COALESCE).
	ClearNodePin bool `json:"clear_node_pin,omitempty"`
}

type UpdateRequest struct {
	GroupID        string                `json:"group_id"`
	GroupVersion   int                   `json:"group_version"`
	Name           string                `json:"name"`
	Strategy       string                `json:"strategy"`
	SourceSpec     proxygroup.SourceSpec `json:"source_spec"`
	Enabled        *bool                 `json:"enabled,omitempty"`
	EmptyBehavior  string                `json:"empty_behavior,omitempty"`
	FallbackTarget string                `json:"fallback_target_id,omitempty"`
	// DialerProxyGroup tunnels this service's egress through another group.
	DialerProxyGroup string                `json:"dialer_proxy_group_id,omitempty"`
	ListenerID       string                `json:"listener_id"`
	ListenerVersion  int                   `json:"listener_version"`
	Listener         ListenerUpdateRequest `json:"listener"`
}

type ServiceRecord struct {
	Group    proxygroup.Group  `json:"group"`
	Listener listener.Listener `json:"listener"`
}

type Service struct {
	groups    GroupService
	groupList GroupReader
	listeners ListenerService
	settings  SettingsReader
}

func NewService(groups GroupService, listeners ListenerService, settings SettingsReader) (*Service, error) {
	if groups == nil || listeners == nil {
		return nil, errors.New("proxy group and listener services are required")
	}
	service := &Service{groups: groups, listeners: listeners, settings: settings}
	if reader, ok := groups.(GroupReader); ok {
		service.groupList = reader
	}
	return service, nil
}

// Create builds one service: an egress anchor plus an entry point.
//
// The anchor is either created here from name/strategy/source_spec, or — when
// listener.proxy_group_id names one — reused as-is. Reuse exists so a residential
// channel (provider + channel, which owns a proxy group but is not a
// subscription) can gain extra entry points without a second group being built
// for the same exits. See
// .agents/notes/implemented/feature/2026-10-14-reuse-existing-group-for-an-entry.md.
func (s *Service) Create(ctx context.Context, request CreateRequest) (ServiceRecord, error) {
	reuseGroupID := strings.TrimSpace(request.Listener.ProxyGroupID)
	reusing := reuseGroupID != ""
	var group proxygroup.Group
	if pinned := strings.TrimSpace(request.Listener.NodeID); pinned != "" {
		if reusing {
			return ServiceRecord{}, fmt.Errorf("%w: listener.node_id pins this service to one node and listener.proxy_group_id reuses a group; send only one", proxygroup.ErrInvalid)
		}
		return s.createPinned(ctx, request)
	}
	if reusing {
		reused, err := s.reuseGroup(ctx, request, reuseGroupID)
		if err != nil {
			return ServiceRecord{}, err
		}
		group = reused
	} else {
		created, err := s.groups.Create(ctx, proxygroup.CreateRequest{
			Name:             request.Name,
			Strategy:         request.Strategy,
			SourceSpec:       request.SourceSpec,
			Enabled:          request.Enabled,
			EmptyBehavior:    request.EmptyBehavior,
			DialerProxyGroup: request.DialerProxyGroup,
		})
		if err != nil {
			if created.ID != "" {
				_ = s.groups.Delete(ctx, created.ID, created.Version)
			}
			return ServiceRecord{}, fmt.Errorf("%w: create proxy group: %v", ErrCreateFailed, err)
		}
		group = created
	}
	createdListener, err := s.createListener(ctx, group.ID, request.Listener)
	if err != nil {
		if reusing {
			// The group belongs to whoever created it (a residential channel
			// owns its own), so a failed entry point must leave it alone.
			return ServiceRecord{}, fmt.Errorf("%w: create listener: %v", ErrCreateFailed, err)
		}
		cleanupErr := s.groups.Delete(ctx, group.ID, group.Version)
		if cleanupErr != nil {
			return ServiceRecord{Group: group}, fmt.Errorf("%w: create listener: %v; cleanup proxy group: %v", ErrCreateFailed, err, cleanupErr)
		}
		return ServiceRecord{}, fmt.Errorf("%w: create listener: %v", ErrCreateFailed, err)
	}
	if err := s.syncSharedInbound(ctx); err != nil {
		if reusing {
			// Compensate only the new entry; the channel-owned group is shared.
			cleanupErr := s.listeners.Delete(ctx, createdListener.ID, createdListener.Version)
			if cleanupErr == nil {
				cleanupErr = s.syncSharedInbound(ctx)
			}
			return ServiceRecord{}, fmt.Errorf("%w: publish shared inbound: %v; cleanup listener: %v", ErrCreateFailed, err, cleanupErr)
		}
		cleanupErr := s.groups.Delete(ctx, group.ID, group.Version)
		if cleanupErr == nil {
			cleanupErr = s.syncSharedInbound(ctx)
		}
		return ServiceRecord{}, fmt.Errorf("%w: publish shared inbound: %v; cleanup proxy group: %v", ErrCreateFailed, err, cleanupErr)
	}
	return ServiceRecord{Group: group, Listener: createdListener}, nil
}

// createListener publishes the new service either on its own port or as a
// member of the shared inbound. A shared member keeps the requested protocol
// and credentials but ignores its bind address and port: the aggregate row
// already owns them, and the member is selected by username instead.
func (s *Service) createListener(ctx context.Context, groupID string, request ListenerCreateRequest) (listener.Listener, error) {
	settings, err := s.settingsOrDefault(ctx)
	if err != nil {
		return listener.Listener{}, err
	}
	shared := settings.SharedInbound
	kind := request.Kind
	owner, bindAddress, port := sharedOwnerFor(shared, kind)
	request.Name = strings.TrimSpace(request.Name)
	if owner == "" {
		return s.listeners.Create(ctx, listener.CreateRequest{
			Name:           request.Name,
			Kind:           kind,
			BindAddress:    request.BindAddress,
			Port:           request.Port,
			ProxyGroupID:   groupID,
			NodeID:         request.NodeID,
			Auth:           request.Auth,
			Transport:      request.Transport,
			PublicEndpoint: request.PublicEndpoint,
			Enabled:        request.Enabled,
		})
	}
	if request.Auth == nil || strings.TrimSpace(request.Auth.Username) == "" {
		return listener.Listener{}, fmt.Errorf("%w: the shared inbound routes members by username; enable username/password authentication", listener.ErrInvalid)
	}
	return s.listeners.Create(ctx, listener.CreateRequest{
		Name:           request.Name,
		Kind:           kind,
		BindAddress:    bindAddress,
		Port:           port,
		ProxyGroupID:   groupID,
		NodeID:         request.NodeID,
		Auth:           request.Auth,
		Transport:      listener.Transport{Type: "ws", WSPath: listener.SharedInboundRoutePath()},
		PublicEndpoint: request.PublicEndpoint,
		Enabled:        request.Enabled,
		SharedInbound:  owner,
	})
}

// sharedOwnerFor resolves the aggregate family (if any) that should carry a
// service of this protocol. It returns "" for the historical per-port mode.
func sharedOwnerFor(shared systemsettings.SharedInboundSettings, kind string) (string, string, int) {
	switch {
	case shared.Enabled() && !isWebSocketKind(kind):
		return listener.SharedInboundStandardOwner, shared.MixedBindAddress, shared.MixedPort
	case shared.AppliesToWebSocket() && isWebSocketKind(kind):
		return listener.SharedInboundWebSocketOwner, "127.0.0.1", shared.WSPort
	default:
		return "", "", 0
	}
}

// ConvergeSharedInbound republishes the aggregate listeners after a settings
// change (for example switching from dedicated ports to the shared inbound) or
// after a service deletion. It is exported so the settings applier can run it
// before the data plane is asked to compile.
func (s *Service) ConvergeSharedInbound(ctx context.Context) error {
	return s.syncSharedInbound(ctx)
}

// syncSharedInbound converges the aggregate rows after a membership change and
// applies the result to the data plane.
func (s *Service) syncSharedInbound(ctx context.Context) error {
	settings, err := s.settingsOrDefault(ctx)
	if err != nil {
		return err
	}
	spec := listener.SharedInboundSpec{
		Enabled:          settings.SharedInbound.Enabled(),
		IncludeWebSocket: settings.SharedInbound.AppliesToWebSocket(),
		MixedBindAddress: settings.SharedInbound.MixedBindAddress,
		MixedPort:        settings.SharedInbound.MixedPort,
		WSPort:           settings.SharedInbound.WSPort,
	}
	groups := []store.ProxyGroupRecord{}
	if s.groupList != nil {
		listed, listErr := s.groupList.List(ctx)
		if listErr != nil {
			return listErr
		}
		for _, group := range listed {
			groups = append(groups, store.ProxyGroupRecord{
				ID:      group.ID,
				Name:    group.Name,
				Enabled: group.Enabled,
			})
		}
	}
	// The listener service recomputes the desired state from the persisted
	// membership rows, so the member slice is only an ordering hint.
	if _, err := s.listeners.EnsureSharedInbounds(ctx, spec, groups, nil); err != nil {
		return err
	}
	return nil
}

func (s *Service) settingsOrDefault(ctx context.Context) (systemsettings.Settings, error) {
	if s.settings == nil {
		return systemsettings.Default(), nil
	}
	settings, err := s.settings.Get(ctx)
	if err != nil {
		return systemsettings.Settings{}, fmt.Errorf("read global settings: %w", err)
	}
	return settings, nil
}

func isWebSocketKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "vless", "vmess", "trojan":
		return true
	default:
		return false
	}
}

func (s *Service) Update(ctx context.Context, request UpdateRequest) (ServiceRecord, error) {
	// An entry point belongs to exactly one service. Without this check the
	// update would happily hand another service's listener over to this group,
	// because the listener service only knows the row id it was given: the
	// caller (and the body) decided which group to attach it to. Naming someone
	// else's listener is a conflict, not a move.
	//
	// Checked before the first write: a rejected update must not consume a group
	// version, or the caller's next legitimate attempt fails on a stale one.
	if err := s.ensureListenerOwnedBy(ctx, request.ListenerID, request.GroupID); err != nil {
		return ServiceRecord{}, err
	}
	original, err := s.groups.Get(ctx, request.GroupID)
	if err != nil {
		return ServiceRecord{}, err
	}
	groupEnabled := original.Enabled
	if request.Enabled != nil {
		groupEnabled = *request.Enabled
	}
	updatedGroup, err := s.groups.Update(ctx, request.GroupID, proxygroup.UpdateRequest{
		Version:          request.GroupVersion,
		Name:             request.Name,
		Strategy:         request.Strategy,
		SourceSpec:       request.SourceSpec,
		Enabled:          groupEnabled,
		EmptyBehavior:    request.EmptyBehavior,
		FallbackTarget:   request.FallbackTarget,
		DialerProxyGroup: request.DialerProxyGroup,
	})
	if err != nil {
		return ServiceRecord{}, err
	}
	listenerEnabled := true
	if request.Listener.Enabled != nil {
		listenerEnabled = *request.Listener.Enabled
	}
	settings, err := s.settingsOrDefault(ctx)
	if err != nil {
		return ServiceRecord{}, err
	}
	owner, sharedBind, sharedPort := sharedOwnerFor(settings.SharedInbound, request.Listener.Kind)
	bind := request.Listener.BindAddress
	port := request.Listener.Port
	transport := request.Listener.Transport
	if owner != "" {
		bind = sharedBind
		port = sharedPort
		transport = listener.Transport{Type: "ws", WSPath: listener.SharedInboundRoutePath()}
	}
	// The pin is carried through unless the caller explicitly moves or clears
	// it. Measured 2026-10-01: this call used to drop the field entirely, so
	// saving an unrelated edit on a pinned service (`ioa-vm-mixed`) rewrote its
	// egress from the pinned node to the anchor group's members — a rename that
	// silently rerouted traffic. Reading the stored pin is cheap and it is the
	// only place that knows the current one.
	pinnedNodeID := strings.TrimSpace(request.Listener.NodeID)
	if request.Listener.ClearNodePin {
		pinnedNodeID = ""
	} else if pinnedNodeID == "" {
		current, err := s.listeners.Get(ctx, request.ListenerID)
		if err != nil {
			return ServiceRecord{}, err
		}
		pinnedNodeID = strings.TrimSpace(current.NodeID)
	}
	updatedListener, err := s.listeners.Update(ctx, request.ListenerID, listener.UpdateRequest{
		Version:        request.ListenerVersion,
		Name:           request.Listener.Name,
		Kind:           request.Listener.Kind,
		BindAddress:    bind,
		Port:           port,
		ProxyGroupID:   request.GroupID,
		NodeID:         pinnedNodeID,
		Auth:           request.Listener.Auth,
		Transport:      transport,
		PublicEndpoint: request.Listener.PublicEndpoint,
		ClearAuth:      request.Listener.ClearAuth,
		Enabled:        listenerEnabled,
		SharedInbound:  owner,
	})
	if err != nil {
		// The listener service already restored its own database record, so
		// this rollback apply compiles from a consistent state. Join any
		// rollback failure into the returned error instead of swallowing it.
		if _, rollbackErr := s.groups.Update(ctx, request.GroupID, proxygroup.UpdateRequest{
			Version:          updatedGroup.Version,
			Name:             original.Name,
			Strategy:         original.Strategy,
			SourceSpec:       original.SourceSpec,
			Enabled:          original.Enabled,
			EmptyBehavior:    original.EmptyBehavior,
			FallbackTarget:   original.FallbackTargetID,
			DialerProxyGroup: original.DialerProxyGroupID,
			// Restoring the spec that was already stored must never be refused by
			// the one-node rule: the row may be a legacy single-node group, and a
			// rollback that cannot run turns a recoverable listener error into a
			// group left on the new spec.
			AllowSingleNode: true,
		}); rollbackErr != nil {
			return ServiceRecord{}, errors.Join(err, fmt.Errorf("restore proxy group after failed listener update: %w", rollbackErr))
		}
		return ServiceRecord{}, err
	}
	if err := s.syncSharedInbound(ctx); err != nil {
		return ServiceRecord{}, fmt.Errorf("%w: %v", listener.ErrApplyFailed, err)
	}
	return ServiceRecord{Group: updatedGroup, Listener: updatedListener}, nil
}

// ensureListenerOwnedBy rejects an update that names a listener belonging to a
// different proxy group. The composite update exists so a service's group and
// its entry point stay consistent; re-pointing a foreign listener would silently
// empty the service that owned it.
func (s *Service) ensureListenerOwnedBy(ctx context.Context, listenerID, groupID string) error {
	listenerID = strings.TrimSpace(listenerID)
	if listenerID == "" {
		return fmt.Errorf("%w: listener_id is required", listener.ErrInvalid)
	}
	current, err := s.listeners.Get(ctx, listenerID)
	if err != nil {
		return err
	}
	if current.ProxyGroupID != groupID {
		return fmt.Errorf("%w: listener %q belongs to another proxy group", listener.ErrConflict, listenerID)
	}
	return nil
}

// sortGroups is a tiny helper kept so the anchor selection stays deterministic
// independent of the repository's ordering.
func sortGroups(groups []store.ProxyGroupRecord) {
	sort.Slice(groups, func(left, right int) bool { return groups[left].ID < groups[right].ID })
}
