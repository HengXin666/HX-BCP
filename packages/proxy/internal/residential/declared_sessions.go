package residential

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// MaxDeclaredSessions bounds how many nodes one channel may publish. It
// matches the database CHECK constraint on session_count.
const MaxDeclaredSessions = 64

// declaredSessionID renders the stable logical id for an ordinal. Node names
// and credentials are keyed on this, so it must never depend on allocation
// state or on the current residential IP.
func declaredSessionID(index int) string {
	return fmt.Sprintf("s%02d", index)
}

// declaredSessionIndex parses a logical id produced by declaredSessionID and
// reports whether the id belongs to the declared set at all.
func declaredSessionIndex(sessionID string) (int, bool) {
	if len(sessionID) < 3 || sessionID[0] != 's' {
		return 0, false
	}
	index := 0
	for _, character := range sessionID[1:] {
		if character < '0' || character > '9' {
			return 0, false
		}
		index = index*10 + int(character-'0')
		if index > MaxDeclaredSessions {
			return 0, false
		}
	}
	if index < 1 {
		return 0, false
	}
	return index, true
}

// DeclaredNodeName is the published node name for one ordinal. Clients see
// this string in their subscription and it stays stable across rotations.
func DeclaredNodeName(channelName string, index int) string {
	return fmt.Sprintf("%s-%02d", strings.TrimSpace(channelName), index)
}

func validateSessionCount(count int, provider Provider) (int, error) {
	if count < 0 || count > MaxDeclaredSessions {
		return 0, fmt.Errorf("%w: session_count must be between 0 and %d", ErrInvalid, MaxDeclaredSessions)
	}
	// Zero preserves the pre-0.2.0 on-demand behaviour for channels that have
	// not opted into a published node list.
	if count == 0 {
		return 0, nil
	}
	if limit := provider.MaxConcurrentSessions; limit > 0 && count > limit {
		return 0, fmt.Errorf(
			"%w: session_count %d exceeds provider concurrent session limit %d",
			ErrInvalid, count, limit,
		)
	}
	return count, nil
}

func validateIdleReleaseSeconds(seconds int) (int, error) {
	if seconds < 0 {
		return 0, fmt.Errorf("%w: idle_release_seconds must not be negative", ErrInvalid)
	}
	// A very short idle window would release an allocation between two requests
	// of the same browser flow and burn provider quota for no benefit.
	if seconds > 0 && seconds < 60 {
		return 0, fmt.Errorf("%w: idle_release_seconds must be 0 or at least 60", ErrInvalid)
	}
	return seconds, nil
}

// SyncDeclaredSessions makes the persisted session set match the channel's
// declared session_count. Growing the count provisions the missing ordinals;
// shrinking it releases them from the tail so the surviving nodes keep their
// names, credentials and residential IPs.
//
// It is safe to call repeatedly: an ordinal that already exists is left alone.
func (s *Service) SyncDeclaredSessions(ctx context.Context, channelID string) error {
	s.clientSessionMutex.Lock()
	defer s.clientSessionMutex.Unlock()
	return s.syncDeclaredSessionsLocked(ctx, channelID)
}

func (s *Service) syncDeclaredSessionsLocked(ctx context.Context, channelID string) error {
	channel, err := s.repository.GetResidentialChannel(ctx, channelID)
	if err != nil {
		return mapStoreError(err)
	}
	if channel.Mode != ModeSticky || channel.SessionCount < 1 {
		return nil
	}
	providerRecord, err := s.repository.GetResidentialProvider(ctx, channel.ProviderID)
	if err != nil {
		return mapStoreError(err)
	}
	// 20260821 懒分配：preallocate=false 时创建 channel 不预占 IP；但对
	// cf-worker（Cloudflare Worker 订阅）仍做一次可达性校验——CF 订阅挂了
	// 必须在创建时就暴露（用户明确要求），而不是等到首个客户端请求。
	// 校验只拉一次订阅确认端点可用，不为 session 分配出口 IP。
	// 其他模式（session-template / api-list）保持完全懒：创建零 fetch。
	if !channel.Preallocate &&
		(strings.EqualFold(providerRecord.RotationMode, RotationCloudflareWorker) ||
			strings.EqualFold(providerRecord.RotationMode, RotationHXCFWsPxy)) {
		if err := s.verifyProviderReachableLocked(ctx, channel, providerRecord); err != nil {
			return err
		}
	}
	repaired, err := s.clearMissingClientSessionAllocations(ctx, channel)
	if err != nil {
		return err
	}
	existing, err := s.repository.ListResidentialClientSessions(ctx, channel.ID)
	if err != nil {
		return err
	}
	declared := make(map[int]store.ResidentialClientSessionRecord, len(existing))
	for _, session := range existing {
		index, ok := declaredSessionIndex(session.SessionID)
		if !ok {
			continue
		}
		declared[index] = session
	}

	// Shrink first so releasing quota can make room for nothing else to fail
	// on the provider's concurrency limit during a resize in both directions.
	for index, session := range declared {
		if index <= channel.SessionCount {
			continue
		}
		if err := s.deleteClientSession(ctx, channel, session); err != nil {
			return fmt.Errorf("release declared session %s: %w", session.SessionID, err)
		}
		delete(declared, index)
	}

	// 20260821 用户决策：默认懒分配（preallocate=false）——创建 channel 时
	// 只建凭据不预占 IP，首个客户端请求到达才分配；避免「一次性分配 N 个
	// IP」阻塞 channel 创建与 provider 并发上限。preallocate=true 保留旧的
	// 立即预分配行为（此时 idle_release_seconds=0 表示永驻、>0 表示用后释放）。
	eager := channel.Preallocate
	for index := 1; index <= channel.SessionCount; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, exists := declared[index]; exists {
			continue
		}
		if err := s.createDeclaredSession(ctx, channel, providerRecord, index, eager); err != nil {
			return fmt.Errorf("provision declared session %s: %w", declaredSessionID(index), err)
		}
	}
	if repaired {
		return s.republishClientSessionGroup(ctx, channel)
	}
	return nil
}

// verifyProviderReachableLocked performs one lightweight provider reachability
// check during lazy channel provisioning (preallocate=false): it fetches a
// single session shape from the provider but discards it, so a dead
// subscription surfaces at channel create/update time instead of on the first
// client request. It never allocates an exit IP.
func (s *Service) verifyProviderReachableLocked(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
	providerRecord store.ResidentialProviderRecord,
) error {
	provider := s.providerFromRecord(providerRecord)
	credentials, err := s.providerCredentials(providerRecord)
	if err != nil {
		return err
	}
	regionSelection, err := clientSessionRegionSelection(channel, "")
	if err != nil {
		return err
	}
	if provider.RotationMode == RotationHXCFWsPxy {
		if err := s.wsPxyControl().Health(ctx, provider.APIURL); err != nil {
			return fmt.Errorf("%w: hx-cf-wspxy control: %v", ErrProviderUnreachable, err)
		}
		return nil
	}
	sessions, err := s.providerSessions(ctx, provider, credentials, regionSelection, 1)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return fmt.Errorf("%w: provider returned no sessions", ErrInvalid)
	}
	return nil
}

// createDeclaredSession provisions one ordinal. When eager is false the
// session is created with credentials but no residential allocation, so a
// published subscription still lists the node and the first real request
// triggers allocation.
func (s *Service) createDeclaredSession(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
	providerRecord store.ResidentialProviderRecord,
	index int,
	eager bool,
) error {
	sessionID := declaredSessionID(index)
	password, err := s.newClientSessionPassword(ctx, channel.ListenerID)
	if err != nil {
		return err
	}
	encrypted, err := s.cipher.Seal([]byte(password), clientSessionAssociatedData(channel.ID, sessionID))
	if err != nil {
		return fmt.Errorf("encrypt declared session password: %w", err)
	}
	countryCode, err := clientSessionCountry(channel, "")
	if err != nil {
		return err
	}
	now := s.now().UTC()
	record := store.ResidentialClientSessionRecord{
		ChannelID:             channel.ID,
		SessionID:             sessionID,
		AuthUsername:          clientSessionUsername(channel.ID, sessionID),
		AuthPasswordEncrypted: encrypted,
		SessionIndex:          -1,
		DeclaredIndex:         index,
		RouteMode:             ClientRouteResidential,
		CountryCode:           countryCode,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if eager {
		fingerprint, expiresAt, err := s.allocateClientSessionNode(
			ctx, channel, providerRecord, sessionID, now, countryCode,
		)
		if err != nil {
			return err
		}
		record.NodeFingerprint = fingerprint
		record.AllocatedAt = &now
		record.ExpiresAt = expiresAt
	}
	created, err := s.repository.CreateResidentialClientSession(ctx, record)
	if err != nil {
		if record.NodeFingerprint != "" {
			s.destroyWsPxyFingerprint(ctx, providerRecord, channel.ID, record.NodeFingerprint)
			_ = s.repository.DeleteResidentialSessionNode(ctx, channel.ID, record.NodeFingerprint)
		}
		return mapStoreError(err)
	}
	if err := s.republishClientSessionGroup(ctx, channel); err != nil {
		_ = s.repository.DeleteResidentialClientSession(ctx, channel.ID, created.SessionID)
		if record.NodeFingerprint != "" {
			s.destroyWsPxyFingerprint(ctx, providerRecord, channel.ID, record.NodeFingerprint)
			_ = s.repository.DeleteResidentialSessionNode(ctx, channel.ID, record.NodeFingerprint)
		}
		_ = s.republishClientSessionGroup(ctx, channel)
		return fmt.Errorf("publish declared session: %w", err)
	}
	return nil
}

// declaredSessionViews renders the published node list for one channel in
// ordinal order. Every declared ordinal appears even when it currently has no
// residential allocation, because the node name is what a client subscribed to.
//
// ExitIP is left empty: a residential node record carries the vendor gateway
// address, not the egress address, so reporting one would require a probe per
// session on every channel read.
func (s *Service) declaredSessionViews(
	channel store.ResidentialChannelRecord,
	sessions []store.ResidentialClientSessionRecord,
) ([]ChannelSession, error) {
	if channel.SessionCount < 1 {
		return nil, nil
	}
	byIndex := make(map[int]store.ResidentialClientSessionRecord, len(sessions))
	for _, session := range sessions {
		if index, ok := declaredSessionIndex(session.SessionID); ok {
			byIndex[index] = session
		}
	}
	views := make([]ChannelSession, 0, channel.SessionCount)
	for index := 1; index <= channel.SessionCount; index++ {
		view := ChannelSession{
			Index:     index,
			SessionID: declaredSessionID(index),
			NodeName:  DeclaredNodeName(channel.Name, index),
			RouteMode: ClientRouteResidential,
		}
		if session, exists := byIndex[index]; exists {
			view.RouteMode = session.RouteMode
			view.CountryCode = session.CountryCode
			view.Allocated = session.NodeFingerprint != ""
			view.AllocatedAt = session.AllocatedAt
			view.ExpiresAt = session.ExpiresAt
			view.RotateCount = session.RotateCount
			view.LastRotatedAt = session.LastRotatedAt
			view.LastUsedAt = session.LastUsedAt
			view.AllocVersion = session.AllocVersion
			if leaseActive(session, s.now()) {
				view.LeaseHolder = session.LeaseHolder
				view.LeaseExpiresAt = session.LeaseExpiresAt
			}
		}
		views = append(views, view)
	}
	return views, nil
}

// ReleaseIdleDeclaredSessions returns residential allocations whose sessions
// have been unused for longer than the channel's idle window. Node names and
// credentials survive, so a published subscription stays valid and the next
// request reallocates transparently.
//
// Work is bounded by limit and serialized with allocation and rotation.
func (s *Service) ReleaseIdleDeclaredSessions(ctx context.Context, limit int) (int, error) {
	if limit < 1 {
		return 0, nil
	}
	s.clientSessionMutex.Lock()
	defer s.clientSessionMutex.Unlock()
	channels, err := s.repository.ListResidentialChannels(ctx)
	if err != nil {
		return 0, err
	}
	released := 0
	now := s.now().UTC()
	for _, channel := range channels {
		if released >= limit {
			break
		}
		if channel.Mode != ModeSticky || channel.IdleReleaseSeconds <= 0 {
			continue
		}
		idleWindow := time.Duration(channel.IdleReleaseSeconds) * time.Second
		sessions, err := s.repository.ListResidentialClientSessions(ctx, channel.ID)
		if err != nil {
			return released, err
		}
		for _, session := range sessions {
			if released >= limit {
				break
			}
			if err := ctx.Err(); err != nil {
				return released, err
			}
			if !declaredSessionIdle(session, now, idleWindow) {
				continue
			}
			if err := s.releaseDeclaredAllocation(ctx, channel, session); err != nil {
				return released, err
			}
			released++
		}
	}
	return released, nil
}

func declaredSessionIdle(
	session store.ResidentialClientSessionRecord,
	now time.Time,
	idleWindow time.Duration,
) bool {
	if _, ok := declaredSessionIndex(session.SessionID); !ok {
		return false
	}
	if session.RouteMode != ClientRouteResidential || session.NodeFingerprint == "" {
		return false
	}
	// A node under an active lease is owned by a consumer and counts as in
	// use even without data-plane traffic: the lease is the liveness signal.
	if leaseActive(session, now) {
		return false
	}
	// A session that has never been used still counts from its allocation
	// time, otherwise an unused eager allocation would be held forever.
	reference := session.LastUsedAt
	if reference == nil {
		reference = session.AllocatedAt
	}
	if reference == nil {
		return false
	}
	return now.Sub(reference.UTC()) >= idleWindow
}

// releaseDeclaredAllocation drops only the residential node. The session row,
// its ordinal and its credentials are preserved.
func (s *Service) releaseDeclaredAllocation(
	ctx context.Context,
	channel store.ResidentialChannelRecord,
	session store.ResidentialClientSessionRecord,
) error {
	updated, err := s.repository.ClearResidentialClientSessionAllocation(
		ctx, channel.ID, session.SessionID,
	)
	if err != nil {
		return mapStoreError(err)
	}
	if provider, providerErr := s.repository.GetResidentialProvider(ctx, channel.ProviderID); providerErr == nil {
		s.destroyWsPxyFingerprint(ctx, provider, channel.ID, session.NodeFingerprint)
	}
	if err := s.repository.DeleteResidentialSessionNode(ctx, channel.ID, session.NodeFingerprint); err != nil {
		return err
	}
	if err := s.republishClientSessionGroup(ctx, channel); err != nil {
		return fmt.Errorf("release idle residential allocation: %w", err)
	}
	return s.closeChannelClientConnections(ctx, channel, updated.AuthUsername)
}
