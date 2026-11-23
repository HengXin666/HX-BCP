package residential

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// defaultRotateInterval is the minimum spacing between two rotations of one
// channel. Rotation is reachable over a public token route, so it is bounded by
// default rather than opt-in.
const defaultRotateInterval = 2 * time.Second

// rotateProbeTimeout bounds the best-effort background probe started after a
// successful rotation. It must not become the timeout of the public rotate
// request itself.
const rotateProbeTimeout = 2 * time.Second

// maximumTrackedChannels bounds the limiter's memory. Entries are evicted oldest
// first, so a large number of channels cannot grow it without limit.
const maximumTrackedChannels = 4096

// RotationResult reports the outcome of advancing a channel to the next IP.
type RotationResult struct {
	ChannelID string `json:"channel_id"`
	// SessionIndex is the pool slot now in use.
	SessionIndex int `json:"session_index"`
	PoolSize     int `json:"pool_size"`
	// ExitIP is the most recently observed egress address, carried over from the
	// last provider test probe. Empty when it has never been measured.
	ExitIP string `json:"exit_ip,omitempty"`
	// LatencyMS is retained for response compatibility. Rotation no longer waits
	// for reachability probes, so this is 0 for the current request.
	LatencyMS int       `json:"latency_ms,omitempty"`
	RotatedAt time.Time `json:"rotated_at"`
	// PoolRefreshed reports whether the pool was regenerated during this call.
	PoolRefreshed bool `json:"pool_refreshed"`
}

// ChannelStatus is the consumer-visible state of a channel, returned by the
// public status endpoint. It deliberately excludes credentials and the pool
// contents.
type ChannelStatus struct {
	SessionIndex  int        `json:"session_index"`
	PoolSize      int        `json:"pool_size"`
	ExitIP        string     `json:"exit_ip,omitempty"`
	LastRotatedAt *time.Time `json:"last_rotated_at,omitempty"`
	RotateCount   int        `json:"rotate_count"`
}

// rotateLimiter enforces a per-channel minimum interval between rotations.
type rotateLimiter struct {
	mu              sync.Mutex
	last            map[string]time.Time
	order           []string
	minimumInterval time.Duration
	now             func() time.Time
}

func newRotateLimiter(interval time.Duration) *rotateLimiter {
	return &rotateLimiter{
		last:            make(map[string]time.Time),
		order:           make([]string, 0, 16),
		minimumInterval: interval,
		now:             time.Now,
	}
}

// allow reports whether a rotation may proceed, and how long to wait otherwise.
func (limiter *rotateLimiter) allow(channelID string) (time.Duration, bool) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	if previous, exists := limiter.last[channelID]; exists {
		if elapsed := now.Sub(previous); elapsed < limiter.minimumInterval {
			return limiter.minimumInterval - elapsed, false
		}
	} else {
		if len(limiter.order) >= maximumTrackedChannels {
			oldest := limiter.order[0]
			limiter.order = limiter.order[1:]
			delete(limiter.last, oldest)
		}
		limiter.order = append(limiter.order, channelID)
	}
	limiter.last[channelID] = now
	return 0, true
}

// RotateChannel advances a sticky channel to its next residential IP.
//
// The fast path only moves the data-plane selector to the next pooled session,
// which is a control-socket call rather than a configuration recompile, so it
// does not disturb other channels' connections. When the pool has been fully
// traversed the sessions are regenerated so exit IPs keep changing instead of
// cycling through the same set forever.
func (s *Service) RotateChannel(ctx context.Context, channelID string) (RotationResult, error) {
	record, err := s.repository.GetResidentialChannel(ctx, channelID)
	if err != nil {
		return RotationResult{}, mapStoreError(err)
	}
	return s.rotate(ctx, record)
}

// RotateChannelByToken advances the channel addressed by a public rotate token.
func (s *Service) RotateChannelByToken(ctx context.Context, token string) (RotationResult, error) {
	record, err := s.channelByToken(ctx, token)
	if err != nil {
		return RotationResult{}, err
	}
	return s.rotate(ctx, record)
}

// ChannelStatusByToken reports the current exit state for a public token.
func (s *Service) ChannelStatusByToken(ctx context.Context, token string) (ChannelStatus, error) {
	record, err := s.channelByToken(ctx, token)
	if err != nil {
		return ChannelStatus{}, err
	}
	pool, err := s.repository.ListResidentialSessionNodes(ctx, record.ID)
	if err != nil {
		return ChannelStatus{}, err
	}
	return ChannelStatus{
		SessionIndex:  record.ActiveSessionIndex,
		PoolSize:      len(pool),
		ExitIP:        record.LastExitIP,
		LastRotatedAt: record.LastRotatedAt,
		RotateCount:   record.RotateCount,
	}, nil
}

// RotateChannelToken issues a new public rotate token, invalidating any rotate
// URL already handed to a consumer.
func (s *Service) RotateChannelToken(ctx context.Context, channelID string) (Channel, error) {
	record, err := s.repository.GetResidentialChannel(ctx, channelID)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	token, err := newToken()
	if err != nil {
		return Channel{}, err
	}
	updated, err := s.repository.RotateResidentialChannelToken(ctx, record.ID, token)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	provider, err := s.repository.GetResidentialProvider(ctx, updated.ProviderID)
	if err != nil {
		return Channel{}, mapStoreError(err)
	}
	return s.channelFromRecord(ctx, updated, s.providerFromRecord(provider))
}

func (s *Service) channelByToken(ctx context.Context, token string) (store.ResidentialChannelRecord, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return store.ResidentialChannelRecord{}, ErrNotFound
	}
	record, err := s.repository.GetResidentialChannelByRotateToken(ctx, token)
	if err != nil {
		return store.ResidentialChannelRecord{}, mapStoreError(err)
	}
	// A token for a non-sticky or disabled channel must not reveal that the
	// token existed, so it maps to the same not-found result.
	if record.Mode != ModeSticky || !record.Enabled {
		return store.ResidentialChannelRecord{}, ErrNotFound
	}
	return record, nil
}

func (s *Service) rotate(
	ctx context.Context,
	record store.ResidentialChannelRecord,
) (RotationResult, error) {
	if record.Mode != ModeSticky {
		return RotationResult{}, fmt.Errorf(
			"%w: only %q channels can rotate; %q leaves rotation to the provider",
			ErrInvalid,
			ModeSticky,
			ModePassthrough,
		)
	}
	if !record.Enabled {
		return RotationResult{}, fmt.Errorf("%w: channel is disabled", ErrInvalid)
	}
	if wait, allowed := s.rotateLimiter.allow(record.ID); !allowed {
		return RotationResult{}, fmt.Errorf(
			"%w: retry in %s",
			ErrRateLimited,
			wait.Round(time.Millisecond),
		)
	}

	pool, err := s.repository.ListResidentialSessionNodes(ctx, record.ID)
	if err != nil {
		return RotationResult{}, err
	}
	if len(pool) == 0 {
		return RotationResult{}, fmt.Errorf("%w: channel has no pooled sessions", ErrInvalid)
	}

	refreshDue, err := s.poolRefreshDue(ctx, record)
	if err != nil {
		return RotationResult{}, err
	}
	nextIndex := record.ActiveSessionIndex + 1
	refreshed := false
	if refreshDue || nextIndex >= len(pool) {
		// The pool is exhausted or stale. Regenerate it so the next cycle draws
		// fresh residential IPs instead of replaying expired session parameters.
		// A stale single-session pool is refreshed too; it otherwise has no way
		// to obtain a new exit address.
		if len(pool) > 1 || refreshDue {
			if err := s.RefreshChannelPool(ctx, record.ID); err != nil {
				return RotationResult{}, err
			}
			refreshed = true
			pool, err = s.repository.ListResidentialSessionNodes(ctx, record.ID)
			if err != nil {
				return RotationResult{}, err
			}
			if len(pool) == 0 {
				return RotationResult{}, fmt.Errorf("%w: channel has no pooled sessions", ErrInvalid)
			}
			nextIndex = 0
		} else {
			nextIndex = 0
		}
	}

	if !refreshed {
		if err := s.selectActiveSession(ctx, record, nextIndex); err != nil {
			return RotationResult{}, err
		}
	}

	// The exit address is only known through the provider test probe, so the
	// previously observed value is preserved rather than overwritten with a
	// guess.
	rotatedAt := s.now().UTC()
	updated, err := s.repository.SetResidentialChannelRotation(ctx, record.ID, nextIndex, record.LastExitIP, rotatedAt)
	if err != nil {
		return RotationResult{}, mapStoreError(err)
	}
	// Verify the freshly selected session in the background. The control-plane
	// probe can wait up to several seconds when the upstream chain is slow, so
	// it must never hold the public rotation request open.
	if s.checker != nil && nextIndex < len(pool) {
		s.checkSelectedSession(nodeProxyName(pool[nextIndex].Fingerprint))
	}
	return RotationResult{
		ChannelID:     updated.ID,
		SessionIndex:  updated.ActiveSessionIndex,
		PoolSize:      len(pool),
		ExitIP:        updated.LastExitIP,
		RotatedAt:     rotatedAt,
		PoolRefreshed: refreshed,
	}, nil
}

// poolRefreshDue uses the provider's configured session lifetime rather than
// the last consumer rotation. A sticky channel can sit idle long enough for
// every rendered session to expire, so rotation itself also checks this state
// before selecting a node.
func (s *Service) poolRefreshDue(
	ctx context.Context,
	record store.ResidentialChannelRecord,
) (bool, error) {
	if record.Mode != ModeSticky {
		return false, nil
	}
	createdAt := record.PoolCreatedAt
	if createdAt == nil {
		// Pools created before the lifecycle migration use the channel creation
		// time as a conservative age estimate until their first refresh.
		if record.CreatedAt.IsZero() {
			return true, nil
		}
		legacyCreatedAt := record.CreatedAt
		createdAt = &legacyCreatedAt
	}
	providerRecord, err := s.repository.GetResidentialProvider(ctx, record.ProviderID)
	if err != nil {
		return false, mapStoreError(err)
	}
	refreshAge := SessionPoolRefreshAge(s.providerFromRecord(providerRecord))
	if refreshAge <= 0 {
		return false, nil
	}
	return !s.now().UTC().Before(createdAt.UTC().Add(refreshAge)), nil
}

// checkSelectedSession launches a bounded best-effort probe after the runtime
// state has been committed. A probe result is intentionally not part of the
// rotation response: callers need the selector switch immediately, even when
// the chained upstream is temporarily slow.
func (s *Service) checkSelectedSession(proxyName string) {
	checker := s.checker
	if checker == nil {
		return
	}
	go func() {
		probeContext, cancel := context.WithTimeout(context.Background(), rotateProbeTimeout)
		defer cancel()
		_, _ = checker.CheckProxyReachable(probeContext, proxyName)
	}()
}

// selectActiveSession points the channel's proxy group at one pooled session.
func (s *Service) selectActiveSession(
	ctx context.Context,
	record store.ResidentialChannelRecord,
	index int,
) error {
	if s.selector == nil {
		return nil
	}
	pool, err := s.repository.ListResidentialSessionNodes(ctx, record.ID)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(pool) {
		return fmt.Errorf("%w: session index %d is outside the pool", ErrInvalid, index)
	}
	groupRecord, err := s.repository.GetProxyGroup(ctx, record.ProxyGroupID)
	if err != nil {
		return mapStoreError(err)
	}
	if err := s.selector.SelectProxy(ctx, groupRecord.Name, nodeProxyName(pool[index].Fingerprint)); err != nil {
		return fmt.Errorf("switch residential session: %w", err)
	}
	return nil
}

// nodeProxyName mirrors the Mihomo compiler's node naming so the selector can
// address a pooled session by the name the data plane knows it by.
//
// It must stay in sync with internal/dataplane/mihomo.nodeProxyName.
func nodeProxyName(fingerprint string) string {
	fingerprint = strings.TrimSpace(fingerprint)
	if len(fingerprint) > 16 {
		fingerprint = fingerprint[:16]
	}
	return "hx-node-" + fingerprint
}

// marshalCanonical encodes a canonical node config with stable key ordering.
func marshalCanonical(canonical map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode residential node config: %w", err)
	}
	return encoded, nil
}
