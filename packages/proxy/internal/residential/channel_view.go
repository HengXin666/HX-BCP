package residential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// channelViewData carries the preloaded lookups that render every channel in
// one pass. Loading all providers, links, pool sizes, sessions and listeners
// up front turns the list view from one query per channel into a fixed set of
// queries regardless of channel count.
type channelViewData struct {
	providersByID        map[string]store.ResidentialProviderRecord
	providerIDsByChannel map[string][]string
	poolSizes            map[string]int
	sessionsByChannel    map[string][]store.ResidentialClientSessionRecord
	listenersByID        map[string]store.ListenerRecord
}

func (s *Service) ListChannels(ctx context.Context) ([]Channel, error) {
	records, err := s.repository.ListResidentialChannels(ctx)
	if err != nil {
		return nil, err
	}
	providers, err := s.repository.ListResidentialProviders(ctx)
	if err != nil {
		return nil, err
	}
	providerRecords := make(map[string]store.ResidentialProviderRecord, len(providers))
	for _, provider := range providers {
		providerRecords[provider.ID] = provider
	}
	channelIDs := make([]string, 0, len(records))
	for _, record := range records {
		channelIDs = append(channelIDs, record.ID)
	}
	view, err := s.loadChannelView(ctx, channelIDs, providerRecords)
	if err != nil {
		return nil, err
	}
	channels := make([]Channel, 0, len(records))
	for _, record := range records {
		channel, err := s.channelFromRecordWithView(ctx, record, s.providerFromRecord(providerRecords[record.ProviderID]), view)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

// loadChannelView fetches every aggregate the channel view needs in a fixed
// number of queries, independent of how many channels exist.
func (s *Service) loadChannelView(
	ctx context.Context,
	channelIDs []string,
	providerRecords map[string]store.ResidentialProviderRecord,
) (*channelViewData, error) {
	view := &channelViewData{
		providersByID:        providerRecords,
		providerIDsByChannel: map[string][]string{},
		poolSizes:            map[string]int{},
		sessionsByChannel:    map[string][]store.ResidentialClientSessionRecord{},
		listenersByID:        map[string]store.ListenerRecord{},
	}
	links, err := s.repository.ListChannelProvidersForChannels(ctx, channelIDs)
	if err != nil {
		return nil, err
	}
	view.providerIDsByChannel = links
	poolSizes, err := s.repository.CountResidentialSessionNodes(ctx, channelIDs)
	if err != nil {
		return nil, err
	}
	view.poolSizes = poolSizes
	sessions, err := s.repository.ListResidentialClientSessionsForChannels(ctx, channelIDs)
	if err != nil {
		return nil, err
	}
	view.sessionsByChannel = sessions
	listeners, err := s.repository.ListListeners(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range listeners {
		view.listenersByID[record.ID] = record
	}
	return view, nil
}

// channelProviders resolves the providers aggregated under a channel using the
// preloaded view; the per-item path is retained for single-channel lookups.
func (s *Service) channelProviders(ctx context.Context, channelID string, view *channelViewData) ([]Provider, error) {
	if view != nil {
		providers := make([]Provider, 0)
		for _, id := range view.providerIDsByChannel[channelID] {
			record, exists := view.providersByID[id]
			if !exists {
				continue
			}
			providers = append(providers, s.providerFromRecord(record))
		}
		return providers, nil
	}
	ids, err := s.repository.ListChannelProviders(ctx, channelID)
	if err != nil {
		return nil, err
	}
	providers := make([]Provider, 0, len(ids))
	for _, id := range ids {
		record, err := s.repository.GetResidentialProvider(ctx, id)
		if err != nil {
			continue
		}
		providers = append(providers, s.providerFromRecord(record))
	}
	return providers, nil
}

func (s *Service) GetChannel(ctx context.Context, id string) (Channel, error) {
	record, err := s.repository.GetResidentialChannel(ctx, id)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	provider, err := s.repository.GetResidentialProvider(ctx, record.ProviderID)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	return s.channelFromRecord(ctx, record, s.providerFromRecord(provider))
}

func (s *Service) channelFromRecord(ctx context.Context, record store.ResidentialChannelRecord, provider Provider) (Channel, error) {
	return s.channelFromRecordWithView(ctx, record, provider, nil)
}

func (s *Service) channelFromRecordWithView(ctx context.Context, record store.ResidentialChannelRecord, provider Provider, view *channelViewData) (Channel, error) {
	aggregated, err := s.channelProviders(ctx, record.ID, view)
	if err != nil {
		return Channel{}, err
	}
	poolSize := 0
	if view != nil {
		poolSize = view.poolSizes[record.ID]
	} else {
		pool, err := s.repository.ListResidentialSessionNodes(ctx, record.ID)
		if err != nil {
			return Channel{}, err
		}
		poolSize = len(pool)
	}
	sessions := []store.ResidentialClientSessionRecord(nil)
	if record.Mode == ModeSticky {
		if view != nil {
			sessions = view.sessionsByChannel[record.ID]
		} else {
			sessions, err = s.repository.ListResidentialClientSessions(ctx, record.ID)
			if err != nil {
				return Channel{}, err
			}
		}
	}
	activeSessionCount := 0
	if record.Mode == ModeSticky {
		for _, session := range sessions {
			if session.RouteMode == ClientRouteResidential && session.NodeFingerprint != "" {
				activeSessionCount++
			}
		}
	} else {
		activeSessionCount = poolSize
	}
	channel := Channel{
		ID: record.ID, Name: record.Name, ProviderID: record.ProviderID, ProviderName: provider.Name,
		Providers: aggregated,
		Mode:      record.Mode, ProxyGroupID: record.ProxyGroupID, ListenerID: record.ListenerID,
		Region: record.Region, RegionMode: normalizedChannelRegionMode(record.RegionMode),
		RandomRegions: parseRegionList(record.RandomRegions), SessionCount: record.SessionCount,
		IdleReleaseSeconds: record.IdleReleaseSeconds, Preallocate: record.Preallocate,
		ActiveSessionCount: activeSessionCount,
		PoolSize:           poolSize, ActiveSessionIndex: record.ActiveSessionIndex, RotateCount: record.RotateCount,
		LastRotatedAt: record.LastRotatedAt, LastExitIP: record.LastExitIP, PoolCreatedAt: record.PoolCreatedAt,
		PoolRefreshAfterSeconds: int(SessionPoolRefreshAge(provider).Seconds()),
		SessionTTLSeconds:       provider.SessionTTLSeconds, Enabled: record.Enabled, Version: record.Version,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if record.Mode == ModeSticky && record.RotateToken != "" {
		channel.RotatePath = "/rot/" + record.RotateToken
	}
	if record.Mode == ModeSticky && record.ControlToken != "" {
		channel.ControlPath = "/ctl/" + record.ControlToken
	}
	if record.Mode == ModeSticky && record.SessionCount > 0 {
		sessionViews, err := s.declaredSessionViews(record, sessions)
		if err != nil {
			return Channel{}, err
		}
		channel.Sessions = sessionViews
	}
	if record.DirectListenerID != "" {
		directRecord, err := s.listenerForChannel(ctx, record.DirectListenerID, view)
		if err == nil {
			channel.DirectEndpoint = &ChannelEndpoint{
				Kind: directRecord.Kind, BindAddress: directRecord.BindAddress, Port: directRecord.Port,
				AuthEnabled: directRecord.AuthMode != "none" && len(directRecord.AuthConfigEncrypted) > 0,
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return Channel{}, err
		}
	}
	listenerRecord, err := s.listenerForChannel(ctx, record.ListenerID, view)
	if err == nil {
		var transport listener.Transport
		if err := json.Unmarshal([]byte(listenerRecord.TransportJSON), &transport); err != nil {
			return Channel{}, fmt.Errorf("decode residential listener transport: %w", err)
		}
		channel.Endpoint = ChannelEndpoint{
			Kind: listenerRecord.Kind, BindAddress: listenerRecord.BindAddress, Port: listenerRecord.Port,
			AuthEnabled: listenerRecord.AuthMode != "none" && len(listenerRecord.AuthConfigEncrypted) > 0,
			Transport:   transport,
		}
		if err := json.Unmarshal([]byte(listenerRecord.PublicEndpointJSON), &channel.PublicEndpoint); err != nil {
			return Channel{}, fmt.Errorf("decode residential public endpoint: %w", err)
		}
		if listenerRecord.ShareToken != "" {
			channel.Endpoint.SharePath = "/sub/" + listenerRecord.ShareToken
			publishable := record.Mode == ModePassthrough && isResidentialWebSocketKind(listenerRecord.Kind)
			if record.Mode == ModeSticky && record.SessionCount > 0 {
				publishable = true
			}
			if publishable {
				channel.SubscriptionURL = listener.PublicPathURL(
					channel.PublicEndpoint,
					channel.Endpoint.SharePath+"?format=clash",
				)
			}
		}
		if channel.RotatePath != "" && channel.PublicEndpoint.TLS {
			channel.RotationURL = listener.PublicPathURL(channel.PublicEndpoint, channel.RotatePath)
		}
		if channel.ControlPath != "" {
			channel.ControlURL = listener.PublicPathURL(channel.PublicEndpoint, channel.ControlPath)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return Channel{}, err
	}
	return channel, nil
}

// listenerForChannel resolves one listener either from the preloaded view or
// from a direct lookup, preserving the not-found behavior of GetListener.
func (s *Service) listenerForChannel(ctx context.Context, id string, view *channelViewData) (store.ListenerRecord, error) {
	if view != nil {
		record, exists := view.listenersByID[id]
		if !exists {
			return store.ListenerRecord{}, store.ErrNotFound
		}
		return record, nil
	}
	return s.repository.GetListener(ctx, id)
}

func channelRegionSelection(mode RegionMode, region string, randomRegions []string, provider Provider) (RegionSelection, error) {
	if strings.TrimSpace(string(mode)) == "" && strings.TrimSpace(region) == "" && len(randomRegions) == 0 {
		selection := providerRegionSelection(provider)
		return normalizeRegionSelection(string(selection.Mode), selection.Region, selection.RandomRegions)
	}
	return normalizeRegionSelection(string(mode), region, randomRegions)
}

func normalizedChannelRegionMode(mode string) RegionMode {
	if mode == "" {
		return RegionModeFixed
	}
	return RegionMode(mode)
}
