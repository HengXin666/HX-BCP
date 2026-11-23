package residential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// Channel modes.
const (
	// ModePassthrough authenticates the consumer at our listener and forwards to
	// the vendor gateway. Exit IP changes follow the vendor's own policy.
	ModePassthrough = "passthrough"
	// ModeSticky pins one pooled session per channel so the exit IP is stable,
	// and exposes an API to advance to the next residential IP.
	ModeSticky = "sticky"
)

// CreateChannel provisions an entry point without preallocating sticky IPs.
// The fail-closed empty group compiles to REJECT until a client creates its
// first logical session. Passthrough still needs one vendor gateway node.
func (s *Service) CreateChannel(ctx context.Context, request CreateChannelRequest) (Channel, error) {
	s.channelCreateMutex.Lock()
	defer s.channelCreateMutex.Unlock()

	// A stale residential session reference from any existing channel must not
	// poison the configuration publish this create triggers (see
	// repairAllDanglingClientSessionAllocations).
	if err := s.repairAllDanglingClientSessionAllocations(ctx); err != nil {
		return Channel{}, err
	}

	providerRecord, err := s.repository.GetResidentialProvider(ctx, strings.TrimSpace(request.ProviderID))
	if errors.Is(err, store.ErrNotFound) {
		return Channel{}, fmt.Errorf("%w: provider does not exist", ErrInvalid)
	}
	if err != nil {
		return Channel{}, err
	}
	if !providerRecord.Enabled {
		return Channel{}, fmt.Errorf("%w: provider is disabled", ErrInvalid)
	}
	provider := s.providerFromRecord(providerRecord)

	name := strings.TrimSpace(request.Name)
	if len(name) < 1 || len(name) > 128 {
		return Channel{}, fmt.Errorf("%w: name must contain 1 to 128 characters", ErrInvalid)
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode != ModePassthrough && mode != ModeSticky {
		return Channel{}, fmt.Errorf("%w: mode must be %q or %q", ErrInvalid, ModePassthrough, ModeSticky)
	}
	if mode == ModeSticky && !provider.SupportsSticky {
		return Channel{}, fmt.Errorf(
			"%w: provider rotation mode %q cannot pin sticky sessions; use %q mode",
			ErrInvalid,
			provider.RotationMode,
			ModePassthrough,
		)
	}
	regionSelection, err := channelRegionSelection(
		request.RegionMode,
		request.Region,
		request.RandomRegions,
		provider,
	)
	if err != nil {
		return Channel{}, err
	}
	region := regionSelection.Region
	enabled := request.Enabled == nil || *request.Enabled
	sessionCount, err := validateSessionCount(request.SessionCount, provider)
	if err != nil {
		return Channel{}, err
	}
	if sessionCount > 0 && mode != ModeSticky {
		return Channel{}, fmt.Errorf("%w: session_count requires %q mode", ErrInvalid, ModeSticky)
	}
	idleRelease, err := validateIdleReleaseSeconds(request.IdleRelease)
	if err != nil {
		return Channel{}, err
	}
	requestedProtocol := request.Protocol
	if requestedProtocol == "" {
		requestedProtocol = request.Listener.Kind
	} else if request.Listener.Kind != "" && !strings.EqualFold(requestedProtocol, request.Listener.Kind) {
		return Channel{}, fmt.Errorf("%w: protocol and listener.kind must match", ErrInvalid)
	}
	protocol, err := normalizeManagedChannelProtocol(requestedProtocol)
	if err != nil {
		return Channel{}, err
	}
	if request.DirectListener != nil {
		return Channel{}, fmt.Errorf(
			"%w: direct HTTP/SOCKS residential listeners are no longer accepted; use a managed WebSocket entry point",
			ErrInvalid,
		)
	}
	if strings.TrimSpace(request.Listener.BindAddress) != "" ||
		request.Listener.Port != 0 || request.Listener.Auth != nil ||
		strings.TrimSpace(request.Listener.Transport.Type) != "" ||
		strings.TrimSpace(request.Listener.Transport.WSPath) != "" {
		return Channel{}, fmt.Errorf(
			"%w: listener is managed by the server; omit protocol, port, path, and bootstrap credentials",
			ErrInvalid,
		)
	}
	if strings.TrimSpace(request.PublicEndpoint.Host) == "" {
		return Channel{}, fmt.Errorf("%w: public_endpoint.host is required for managed WebSocket channels", ErrInvalid)
	}

	channelID, err := newID("residential-channel")
	if err != nil {
		return Channel{}, err
	}
	managedListener, err := s.managedChannelListener(ctx, channelID, protocol)
	if err != nil {
		return Channel{}, err
	}
	bindAddress, err := residentialBindAddress(managedListener.BindAddress)
	if err != nil {
		return Channel{}, err
	}
	request.PublicEndpoint.Port = 443
	request.PublicEndpoint.TLS = true
	if isResidentialWebSocketKind(managedListener.Kind) && request.PublicEndpoint.Host != "" {
		if err := validateResidentialWebSocketEndpoint(request.PublicEndpoint); err != nil {
			return Channel{}, err
		}
	}
	rotateToken, err := newToken()
	if err != nil {
		return Channel{}, err
	}
	controlToken, err := newToken()
	if err != nil {
		return Channel{}, err
	}

	var nodeIDs []string
	var poolCreatedAt *time.Time
	if mode == ModePassthrough {
		credentials, err := s.providerCredentials(providerRecord)
		if err != nil {
			return Channel{}, err
		}
		now := s.now().UTC()
		nodeIDs, err = s.materializePool(ctx, channelID, name, provider, credentials, regionSelection, 1)
		if err != nil {
			return Channel{}, err
		}
		poolCreatedAt = &now
	}

	cleanupPool := func() {
		_ = s.repository.DeleteResidentialSessionPool(ctx, channelID)
	}

	group, err := s.groups.Create(ctx, proxygroup.CreateRequest{
		Name:     channelGroupName(name),
		Strategy: "manual",
		SourceSpec: proxygroup.SourceSpec{
			NodeIDs: nodeIDs, AllowEmpty: mode == ModeSticky,
		},
		Enabled:       &enabled,
		EmptyBehavior: "fail-closed",
		// A channel group's members are vendor sessions, not an operator's node
		// list: passthrough materialises exactly one, sticky starts empty. Refusing
		// that would break the product to enforce a rule about operator intent.
		// See internal/proxygroup/single_node_rule.go.
		AllowSingleNode: true,
	})
	if err != nil {
		if group.ID != "" {
			_ = s.groups.Delete(ctx, group.ID, group.Version)
		}
		cleanupPool()
		return Channel{}, fmt.Errorf("%w: create proxy group: %v", ErrInvalid, err)
	}

	createdListener, err := s.listeners.Create(ctx, listener.CreateRequest{
		Name:           channelListenerName(name),
		Kind:           managedListener.Kind,
		BindAddress:    bindAddress,
		Port:           managedListener.Port,
		ProxyGroupID:   group.ID,
		Auth:           managedListener.Auth,
		Transport:      managedListener.Transport,
		PublicEndpoint: request.PublicEndpoint,
		Enabled:        &enabled,
	})
	if err != nil {
		_ = s.groups.Delete(ctx, group.ID, group.Version)
		cleanupPool()
		return Channel{}, fmt.Errorf("%w: create listener: %v", ErrInvalid, err)
	}

	directListenerID := ""

	rollbackListeners := func() {
		if directListenerID != "" {
			if current, getErr := s.listeners.Get(ctx, directListenerID); getErr == nil {
				_ = s.listeners.Delete(ctx, current.ID, current.Version)
			}
		}
		_ = s.listeners.Delete(ctx, createdListener.ID, createdListener.Version)
		_ = s.groups.Delete(ctx, group.ID, group.Version)
		cleanupPool()
	}

	now := s.now().UTC()
	created, err := s.repository.CreateResidentialChannel(ctx, store.ResidentialChannelRecord{
		ID:                 channelID,
		Name:               name,
		ProviderID:         provider.ID,
		Mode:               mode,
		ProxyGroupID:       group.ID,
		ListenerID:         createdListener.ID,
		DirectListenerID:   directListenerID,
		Region:             region,
		RegionMode:         string(regionSelection.Mode),
		RandomRegions:      marshalRegionList(regionSelection.RandomRegions),
		SessionCount:       sessionCount,
		IdleReleaseSeconds: idleRelease,
		Preallocate:        request.Preallocate,
		ControlToken:       controlToken,
		RotateToken:        rotateToken,
		PoolCreatedAt:      poolCreatedAt,
		Enabled:            enabled,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		rollbackListeners()
		return Channel{}, mapStoreError(err)
	}
	// 20260823: aggregate additional providers under this channel (CF-渠道 →
	// CF-Node1..N). The primary provider plus the declared provider_ids form
	// the node list shown in the frontend; cf-worker channels bypass the
	// session machinery anyway, so the aggregation is management/export only.
	aggregateIDs := append([]string{provider.ID}, request.ProviderIDs...)
	validIDs := make([]string, 0, len(aggregateIDs))
	seen := make(map[string]bool, len(aggregateIDs))
	for _, aggregateID := range aggregateIDs {
		aggregateID = strings.TrimSpace(aggregateID)
		if aggregateID == "" || seen[aggregateID] {
			continue
		}
		aggregateRecord, aggregateErr := s.repository.GetResidentialProvider(ctx, aggregateID)
		if aggregateErr != nil || !aggregateRecord.Enabled {
			continue
		}
		seen[aggregateID] = true
		validIDs = append(validIDs, aggregateID)
	}
	if len(validIDs) > 0 {
		if err := s.repository.ReplaceChannelProviders(ctx, created.ID, validIDs); err != nil {
			_ = s.repository.DeleteResidentialChannel(ctx, created.ID, created.Version)
			rollbackListeners()
			return Channel{}, fmt.Errorf("aggregate channel providers: %w", err)
		}
	}
	// Declared sessions are what makes this channel publishable as an ordinary
	// subscription, so provisioning them is part of creating the channel rather
	// than a later background step.
	if sessionCount > 0 {
		if err := s.SyncDeclaredSessions(ctx, created.ID); err != nil {
			_ = s.repository.DeleteResidentialChannel(ctx, created.ID, created.Version)
			rollbackListeners()
			return Channel{}, fmt.Errorf("provision declared sessions: %w", err)
		}
		created, err = s.repository.GetResidentialChannel(ctx, created.ID)
		if err != nil {
			return Channel{}, mapStoreError(err)
		}
	}
	return s.channelFromRecord(ctx, created, provider)
}

func (s *Service) UpdateChannel(ctx context.Context, id string, request UpdateChannelRequest) (Channel, error) {
	if request.Version < 1 {
		return Channel{}, fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	// Repair stale session slots before the edit publishes a new configuration
	// so a dangling reference cannot fail the apply and leave the channel
	// halfway through the edit.
	if err := s.repairAllDanglingClientSessionAllocations(ctx); err != nil {
		return Channel{}, err
	}
	existing, err := s.repository.GetResidentialChannel(ctx, id)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	name := strings.TrimSpace(request.Name)
	if len(name) < 1 || len(name) > 128 {
		return Channel{}, fmt.Errorf("%w: name must contain 1 to 128 characters", ErrInvalid)
	}
	regionMode := request.RegionMode
	region := request.Region
	randomRegions := request.RandomRegions
	// Older clients do not send the region policy fields. Preserve a channel's
	// existing application-random policy during an unrelated edit; a new client
	// can explicitly select fixed mode to clear it.
	if strings.TrimSpace(string(regionMode)) == "" && strings.TrimSpace(region) == "" && len(randomRegions) == 0 {
		regionMode = RegionMode(existing.RegionMode)
		region = existing.Region
		randomRegions = parseRegionList(existing.RandomRegions)
	}
	regionSelection, err := normalizeRegionSelection(
		string(regionMode),
		region,
		randomRegions,
	)
	if err != nil {
		return Channel{}, err
	}
	providerRecord, err := s.repository.GetResidentialProvider(ctx, existing.ProviderID)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	// Omitted session fields preserve the current configuration so an unrelated
	// edit from an older client cannot silently unpublish a channel's nodes.
	if request.SessionCount != nil {
		sessionCount, err := validateSessionCount(*request.SessionCount, s.providerFromRecord(providerRecord))
		if err != nil {
			return Channel{}, err
		}
		if sessionCount > 0 && existing.Mode != ModeSticky {
			return Channel{}, fmt.Errorf("%w: session_count requires %q mode", ErrInvalid, ModeSticky)
		}
		existing.SessionCount = sessionCount
	}
	if request.IdleRelease != nil {
		idleRelease, err := validateIdleReleaseSeconds(*request.IdleRelease)
		if err != nil {
			return Channel{}, err
		}
		existing.IdleReleaseSeconds = idleRelease
	}
	if request.Preallocate != nil {
		existing.Preallocate = *request.Preallocate
	}
	if request.DirectListener != nil {
		return Channel{}, fmt.Errorf(
			"%w: direct HTTP/SOCKS residential listeners are no longer accepted; clear_direct_listener may remove a historical entry",
			ErrInvalid,
		)
	}
	// A channel created before 0.2.0 has no control token. Mint one lazily on
	// the next edit so an upgrade does not require recreating the channel.
	if existing.Mode == ModeSticky && existing.ControlToken == "" {
		controlToken, err := newToken()
		if err != nil {
			return Channel{}, err
		}
		existing.ControlToken = controlToken
	}
	previousEnabled := existing.Enabled
	previousSessionCount := existing.SessionCount
	previousDirectListenerID := existing.DirectListenerID
	existing.Name = name
	existing.Region = regionSelection.Region
	existing.RegionMode = string(regionSelection.Mode)
	existing.RandomRegions = marshalRegionList(regionSelection.RandomRegions)
	existing.Enabled = request.Enabled
	existing.UpdatedAt = s.now().UTC()
	if request.ClearDirect {
		existing.DirectListenerID = ""
	}

	updated, err := s.repository.UpdateResidentialChannel(ctx, existing, request.Version)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	// Disabling a channel must also take its managed group and entry listener
	// out of the data plane; otherwise the listener stays live while its
	// per-session rules have been filtered out. Enabling restores both.
	if request.Enabled != previousEnabled {
		if err := s.setChannelEnabledState(ctx, updated, request.Enabled); err != nil {
			return Channel{}, err
		}
	}
	if request.ClearDirect && previousDirectListenerID != "" {
		if current, getErr := s.listeners.Get(ctx, previousDirectListenerID); getErr == nil {
			if err := s.listeners.Delete(ctx, current.ID, current.Version); err != nil {
				return Channel{}, fmt.Errorf("remove direct listener: %w", err)
			}
		}
	}
	if request.PublicEndpoint != nil {
		currentListener, err := s.listeners.Get(ctx, updated.ListenerID)
		if err != nil {
			return Channel{}, err
		}
		if isResidentialWebSocketKind(currentListener.Kind) {
			if err := validateResidentialWebSocketEndpoint(*request.PublicEndpoint); err != nil {
				return Channel{}, err
			}
		}
		if err := listener.ValidatePublicEndpoint(*request.PublicEndpoint, currentListener.Port); err != nil {
			return Channel{}, err
		}
		if _, err := s.listeners.Update(ctx, currentListener.ID, listener.UpdateRequest{
			Version:        currentListener.Version,
			Name:           currentListener.Name,
			Kind:           currentListener.Kind,
			BindAddress:    currentListener.BindAddress,
			Port:           currentListener.Port,
			ProxyGroupID:   currentListener.ProxyGroupID,
			NodeID:         currentListener.NodeID,
			Transport:      currentListener.Transport,
			PublicEndpoint: *request.PublicEndpoint,
			Enabled:        request.Enabled,
		}); err != nil {
			return Channel{}, fmt.Errorf("update residential public endpoint: %w", err)
		}
	}
	// Resizing publishes or releases ordinals. Surviving sessions keep their
	// node names, credentials and residential IPs.
	if updated.SessionCount != previousSessionCount || updated.SessionCount > 0 {
		if err := s.SyncDeclaredSessions(ctx, updated.ID); err != nil {
			return Channel{}, fmt.Errorf("resize declared sessions: %w", err)
		}
		updated, err = s.repository.GetResidentialChannel(ctx, updated.ID)
		if err != nil {
			return Channel{}, mapStoreError(err)
		}
	}
	// Existing client allocations retain their current vendor session. The new
	// region is used only by subsequent allocation or rotation requests.
	return s.channelFromRecord(ctx, updated, s.providerFromRecord(providerRecord))
}

// setChannelEnabledState aligns the channel's managed proxy group and entry
// listener with the channel record. Disabling a channel therefore removes its
// entry point from the data plane, and re-enabling restores both without
// touching client credentials or session allocations.
func (s *Service) setChannelEnabledState(ctx context.Context, channel store.ResidentialChannelRecord, enabled bool) error {
	updateGroup := func() error {
		groupRecord, err := s.repository.GetProxyGroup(ctx, channel.ProxyGroupID)
		if err != nil {
			return mapStoreError(err)
		}
		var spec proxygroup.SourceSpec
		if err := json.Unmarshal([]byte(groupRecord.SourceSpecJSON), &spec); err != nil {
			return fmt.Errorf("decode channel proxy group %q source spec: %w", groupRecord.Name, err)
		}
		_, err = s.groups.Update(ctx, groupRecord.ID, proxygroup.UpdateRequest{
			Version:        groupRecord.Version,
			Name:           groupRecord.Name,
			Strategy:       groupRecord.Strategy,
			SourceSpec:     spec,
			Enabled:        enabled,
			EmptyBehavior:  groupRecord.EmptyBehavior,
			FallbackTarget: groupRecord.FallbackTargetID,
		})
		return err
	}
	updateListener := func() error {
		current, err := s.listeners.Get(ctx, channel.ListenerID)
		if err != nil {
			return err
		}
		_, err = s.listeners.Update(ctx, current.ID, listener.UpdateRequest{
			Version:        current.Version,
			Name:           current.Name,
			Kind:           current.Kind,
			BindAddress:    current.BindAddress,
			Port:           current.Port,
			ProxyGroupID:   current.ProxyGroupID,
			NodeID:         current.NodeID,
			Transport:      current.Transport,
			PublicEndpoint: current.PublicEndpoint,
			Enabled:        enabled,
		})
		return err
	}
	if enabled {
		// The listener service rejects a listener whose proxy group is
		// disabled, so enable the group before the listener.
		if err := updateGroup(); err != nil {
			return err
		}
		return updateListener()
	}
	// Disable the listener first while its group is still enabled, then take
	// the group down.
	if err := updateListener(); err != nil {
		return err
	}
	return updateGroup()
}

// DeleteChannel removes the channel and everything it provisioned, in reverse
// dependency order: listener, then group, then pooled session nodes.
func (s *Service) DeleteChannel(ctx context.Context, id string, version int) error {
	if version < 1 {
		return fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	record, err := s.repository.GetResidentialChannel(ctx, id)
	if err != nil {
		return mapStoreError(err)
	}
	if provider, providerErr := s.repository.GetResidentialProvider(ctx, record.ProviderID); providerErr == nil &&
		provider.RotationMode == RotationHXCFWsPxy {
		if pool, poolErr := s.repository.ListResidentialSessionNodes(ctx, record.ID); poolErr == nil {
			s.destroyWsPxyNodes(ctx, s.providerFromRecord(provider).APIURL, pool)
		}
	}
	if err := s.repository.DeleteResidentialChannel(ctx, id, version); err != nil {
		return mapStoreError(err)
	}
	for _, id := range []string{record.DirectListenerID, record.ListenerID} {
		if id == "" {
			continue
		}
		listenerRecord, err := s.repository.GetListener(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.listeners.Delete(ctx, listenerRecord.ID, listenerRecord.Version); err != nil {
			return fmt.Errorf("channel deleted but listener cleanup failed: %w", err)
		}
	}
	groupRecord, err := s.repository.GetProxyGroup(ctx, record.ProxyGroupID)
	if err == nil {
		if err := s.groups.Delete(ctx, groupRecord.ID, groupRecord.Version); err != nil {
			return fmt.Errorf("channel deleted but proxy group cleanup failed: %w", err)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := s.repository.DeleteResidentialSessionPool(ctx, record.ID); err != nil {
		return fmt.Errorf("channel deleted but session pool cleanup failed: %w", err)
	}
	return nil
}

func channelGroupName(channelName string) string {
	return "residential-" + channelName
}

func channelListenerName(channelName string) string {
	return "residential-" + channelName + "-entry"
}
