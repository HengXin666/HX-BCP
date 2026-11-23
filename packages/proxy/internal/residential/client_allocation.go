package residential

import (
	"context"
	"fmt"
	"time"

	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func sessionAllocationExpired(record store.ResidentialClientSessionRecord, now time.Time) bool {
	return record.RouteMode == ClientRouteResidential && record.ExpiresAt != nil &&
		!now.UTC().Before(record.ExpiresAt.UTC())
}

func (s *Service) allocateClientSessionNode(ctx context.Context, channel store.ResidentialChannelRecord, providerRecord store.ResidentialProviderRecord, logicalSessionID string, allocatedAt time.Time, countryCode string) (fingerprint string, expiresAt *time.Time, err error) {
	provider := s.providerFromRecord(providerRecord)
	var credentials Credentials
	credentials, err = s.providerCredentials(providerRecord)
	if err != nil {
		return "", nil, err
	}
	regionSelection, err := clientSessionRegionSelection(channel, countryCode)
	if err != nil {
		return "", nil, err
	}
	var sessions []Session
	sessions, err = s.providerSessions(ctx, provider, credentials, regionSelection, 1)
	if err != nil {
		return "", nil, fmt.Errorf("allocate residential IP: %w", err)
	}
	if len(sessions) == 0 {
		return "", nil, fmt.Errorf("%w: provider returned no residential IP", ErrInvalid)
	}
	session := sessions[0]
	if provider.RotationMode == RotationHXCFWsPxy && session.ID != "" {
		defer func() {
			if err != nil {
				s.destroyWsPxyIDs(ctx, provider.APIURL, []string{session.ID})
			}
		}()
	}
	fingerprint, err = sessionFingerprint(channel.ID, provider, session)
	if err != nil {
		return "", nil, err
	}
	displayName := channel.Name + " session " + logicalSessionID
	canonical := canonicalNodeConfig(provider, session, credentials.Password, displayName)
	encrypted, err := s.sealNodeConfig(canonical, fingerprint)
	if err != nil {
		return "", nil, err
	}
	nodeID, err := newID("node-residential")
	if err != nil {
		return "", nil, err
	}
	protocol := provider.Protocol
	if protocol == "https" {
		protocol = "http"
	}
	if _, err = s.repository.UpsertResidentialSessionNode(ctx, channel.ID, store.ResidentialSessionNode{
		ID: nodeID, Fingerprint: fingerprint, DisplayName: displayName,
		Protocol: protocol, CanonicalConfigEncrypted: encrypted,
	}, allocatedAt); err != nil {
		return "", nil, err
	}
	if lifetime := SessionPoolLifetime(provider); lifetime > 0 {
		value := allocatedAt.UTC().Add(lifetime)
		expiresAt = &value
	}
	return fingerprint, expiresAt, nil
}

func (s *Service) republishClientSessionGroup(ctx context.Context, channel store.ResidentialChannelRecord) error {
	group, err := s.repository.GetProxyGroup(ctx, channel.ProxyGroupID)
	if err != nil {
		return mapStoreError(err)
	}
	nodes, err := s.repository.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil {
		return err
	}
	sessions, err := s.repository.ListResidentialClientSessions(ctx, channel.ID)
	if err != nil {
		return err
	}
	referenced := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if session.RouteMode == ClientRouteResidential && session.NodeFingerprint != "" {
			referenced[session.NodeFingerprint] = struct{}{}
		}
	}
	nodeIDs := make([]string, 0, len(referenced))
	for _, node := range nodes {
		if _, ok := referenced[node.Fingerprint]; ok || channel.Mode == ModePassthrough {
			nodeIDs = append(nodeIDs, node.ID)
		}
	}
	_, err = s.groups.Update(ctx, group.ID, proxygroup.UpdateRequest{
		Version: group.Version, Name: group.Name, Strategy: group.Strategy,
		SourceSpec: proxygroup.SourceSpec{NodeIDs: nodeIDs, AllowEmpty: channel.Mode == ModeSticky},
		Enabled:    group.Enabled, EmptyBehavior: group.EmptyBehavior,
		// A passthrough channel's pool holds exactly one session by design; a
		// sticky one may hold none until a client is allocated.
		AllowSingleNode: true,
	})
	if err != nil {
		return err
	}
	return s.applyClientSessionRoutes(ctx)
}

// clearMissingClientSessionAllocations repairs sessions left behind by an
// interrupted publish or by older releases that deleted a shared node while
// another logical session still referenced it. An empty allocation is a valid
// lazy state and will be filled by the next session rotation.
func (s *Service) clearMissingClientSessionAllocations(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
) (bool, error) {
	nodes, err := s.repository.ListResidentialSessionNodes(ctx, channel.ID)
	if err != nil {
		return false, err
	}
	known := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		known[node.Fingerprint] = struct{}{}
	}
	sessions, err := s.repository.ListResidentialClientSessions(ctx, channel.ID)
	if err != nil {
		return false, err
	}
	repaired := false
	for _, session := range sessions {
		if session.RouteMode != ClientRouteResidential || session.NodeFingerprint == "" {
			continue
		}
		if _, exists := known[session.NodeFingerprint]; exists {
			continue
		}
		if _, err := s.repository.ClearResidentialClientSessionAllocation(
			ctx,
			channel.ID,
			session.SessionID,
		); err != nil {
			return repaired, mapStoreError(err)
		}
		repaired = true
	}
	return repaired, nil
}

// repairAllDanglingClientSessionAllocations clears every sticky channel's
// session references to pool slots that no longer resolve to a node. One stale
// reference (from an interrupted rotation, an older release, or a manually
// disabled/retired node) would otherwise poison the next configuration apply,
// because the Mihomo compiler drops those per-session rules rather than
// failing, but the stale reference stays in the database and would keep every
// later apply fighting the same data.
//
// It is idempotent and only rewrites runtime allocation fields; credentials and
// node identity are untouched. Callers run it before a channel create or edit
// triggers a full configuration publish.
func (s *Service) repairAllDanglingClientSessionAllocations(ctx context.Context) error {
	channels, err := s.repository.ListResidentialChannels(ctx)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if channel.Mode != ModeSticky {
			continue
		}
		if _, err := s.clearMissingClientSessionAllocations(ctx, channel); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) replaceClientSessionAllocation(ctx context.Context, channel store.ResidentialChannelRecord, provider store.ResidentialProviderRecord, previous store.ResidentialClientSessionRecord, rotated bool) (store.ResidentialClientSessionRecord, error) {
	if _, err := s.clearMissingClientSessionAllocations(ctx, channel); err != nil {
		return store.ResidentialClientSessionRecord{}, err
	}
	previous, err := s.repository.GetResidentialClientSession(ctx, channel.ID, previous.SessionID)
	if err != nil {
		return store.ResidentialClientSessionRecord{}, mapStoreError(err)
	}
	now := s.now().UTC()
	if rotated && provider.RotationMode == RotationHXCFWsPxy && previous.NodeFingerprint != "" {
		updated, rotateErr := s.rotateWsPxyClientAllocation(ctx, channel, provider, previous, now)
		if rotateErr == nil {
			return updated, nil
		}
	}
	fingerprint, expiresAt, err := s.allocateClientSessionNode(ctx, channel, provider, previous.SessionID, now, previous.CountryCode)
	if err != nil {
		return store.ResidentialClientSessionRecord{}, err
	}
	updated, err := s.repository.UpdateResidentialClientSessionAllocation(ctx, channel.ID, previous.SessionID, fingerprint, now, expiresAt, rotated)
	if err != nil {
		s.destroyWsPxyFingerprint(ctx, provider, channel.ID, fingerprint)
		_ = s.repository.DeleteResidentialSessionNode(ctx, channel.ID, fingerprint)
		return store.ResidentialClientSessionRecord{}, mapStoreError(err)
	}
	if err := s.republishClientSessionGroup(ctx, channel); err != nil {
		_ = s.repository.RestoreResidentialClientSessionState(ctx, previous)
		s.destroyWsPxyFingerprint(ctx, provider, channel.ID, fingerprint)
		_ = s.repository.DeleteResidentialSessionNode(ctx, channel.ID, fingerprint)
		_ = s.republishClientSessionGroup(ctx, channel)
		return store.ResidentialClientSessionRecord{}, fmt.Errorf("publish residential client allocation: %w", err)
	}
	if previous.NodeFingerprint != "" && previous.NodeFingerprint != fingerprint {
		s.destroyWsPxyFingerprint(ctx, provider, channel.ID, previous.NodeFingerprint)
		_ = s.repository.DeleteResidentialSessionNode(ctx, channel.ID, previous.NodeFingerprint)
	}
	if err := s.closeChannelClientConnections(ctx, channel, updated.AuthUsername); err != nil {
		return store.ResidentialClientSessionRecord{}, err
	}
	return updated, nil
}

func (s *Service) rotateWsPxyClientAllocation(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
	provider store.ResidentialProviderRecord,
	previous store.ResidentialClientSessionRecord,
	now time.Time,
) (store.ResidentialClientSessionRecord, error) {
	view := s.providerFromRecord(provider)
	id, err := s.wsPxySessionIDFromFingerprint(ctx, channel.ID, previous.NodeFingerprint)
	if err != nil {
		return store.ResidentialClientSessionRecord{}, err
	}
	if id == "" {
		return store.ResidentialClientSessionRecord{}, fmt.Errorf("%w: hx-cf-wspxy session id is missing", ErrInvalid)
	}
	if _, err := s.wsPxyControl().Rotate(ctx, view.APIURL, id); err != nil {
		return store.ResidentialClientSessionRecord{}, err
	}
	var expiresAt *time.Time
	if lifetime := SessionPoolLifetime(view); lifetime > 0 {
		value := now.UTC().Add(lifetime)
		expiresAt = &value
	}
	updated, err := s.repository.UpdateResidentialClientSessionAllocation(ctx, channel.ID, previous.SessionID, previous.NodeFingerprint, now, expiresAt, true)
	if err != nil {
		return store.ResidentialClientSessionRecord{}, mapStoreError(err)
	}
	if err := s.closeChannelClientConnections(ctx, channel, updated.AuthUsername); err != nil {
		return store.ResidentialClientSessionRecord{}, err
	}
	return updated, nil
}

func (s *Service) destroyWsPxyFingerprint(ctx context.Context, provider store.ResidentialProviderRecord, channelID, fingerprint string) {
	if provider.RotationMode != RotationHXCFWsPxy || fingerprint == "" {
		return
	}
	id, err := s.wsPxySessionIDFromFingerprint(ctx, channelID, fingerprint)
	if err != nil || id == "" {
		return
	}
	s.destroyWsPxyIDs(ctx, s.providerFromRecord(provider).APIURL, []string{id})
}

func (s *Service) deleteClientSession(ctx context.Context, channel store.ResidentialChannelRecord, current store.ResidentialClientSessionRecord) error {
	if err := s.repository.DeleteResidentialClientSession(ctx, channel.ID, current.SessionID); err != nil {
		return mapStoreError(err)
	}
	if err := s.republishClientSessionGroup(ctx, channel); err != nil {
		_, restoreErr := s.repository.CreateResidentialClientSession(ctx, current)
		_ = s.republishClientSessionGroup(ctx, channel)
		return fmt.Errorf("remove residential client session route: %w; restore: %v", err, restoreErr)
	}
	if current.NodeFingerprint != "" {
		if provider, err := s.repository.GetResidentialProvider(ctx, channel.ProviderID); err == nil {
			s.destroyWsPxyFingerprint(ctx, provider, channel.ID, current.NodeFingerprint)
		}
		_ = s.repository.DeleteResidentialSessionNode(ctx, channel.ID, current.NodeFingerprint)
	}
	return s.closeChannelClientConnections(ctx, channel, current.AuthUsername)
}
