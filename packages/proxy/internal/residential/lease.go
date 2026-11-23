package residential

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// Lease constants define the bounded TTL window of a node lease. A consumer
// must heartbeat inside the window or its exclusive ownership lapses and any
// other consumer may claim the node.
const (
	// DefaultLeaseTTLSeconds applies when a claim/heartbeat omits ttl_seconds.
	DefaultLeaseTTLSeconds = 300
	// MinLeaseTTLSeconds is the shortest lease a caller may request. It is
	// long enough that an occasional heartbeat is not a hot loop.
	MinLeaseTTLSeconds = 60
	// MaxLeaseTTLSeconds caps a lease so a crashed consumer can never hold a
	// node window hostage for more than one day.
	MaxLeaseTTLSeconds = 86400
)

// LeaseRequest is the body of POST /ctl/<token>/nodes/<index>/claim. Holder
// identifies the consuming service; the same holder may reclaim or refresh.
type LeaseRequest struct {
	Holder     string `json:"holder"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// LeaseHeartbeatRequest is the body of POST /ctl/<token>/nodes/<index>/heartbeat.
// It extends the lease held under the given lease_id.
type LeaseHeartbeatRequest struct {
	LeaseID    string `json:"lease_id"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// LeaseReleaseRequest is the body of POST /ctl/<token>/nodes/<index>/release.
// Releasing is idempotent: releasing an already-lapsed lease succeeds.
type LeaseReleaseRequest struct {
	LeaseID string `json:"lease_id"`
}

// ControlLease is the consumer-visible lease state of one declared node.
// LeaseID is a capability token: it is only returned to the holder (claim and
// heartbeat responses) and never rendered in the node pool list.
type ControlLease struct {
	Holder    string     `json:"holder"`
	LeaseID   string     `json:"lease_id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// RotateOptions carries the optional compare-and-swap guard for next/route.
// Both fields are optional so legacy consumers keep working unguarded; new
// integrations send them to get the unified-window guarantees.
type RotateOptions struct {
	// LeaseID is required once the node is leased by another holder. Sending
	// the holder's own lease_id proves ownership of the IP window.
	LeaseID string `json:"lease_id,omitempty"`
	// ExpectedAllocVersion makes rotation/route switching a CAS: when set and
	// stale, the request fails with ErrAllocVersionChanged instead of silently
	// double-rotating a window someone else already rotated.
	ExpectedAllocVersion *int `json:"expected_alloc_version,omitempty"`
}

// leaseActive reports whether a session currently holds an unexpired lease.
func leaseActive(session store.ResidentialClientSessionRecord, now time.Time) bool {
	return session.LeaseID != "" && session.LeaseExpiresAt != nil &&
		now.UTC().Before(session.LeaseExpiresAt.UTC())
}

func validateLeaseHolder(holder string) error {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return fmt.Errorf("%w: lease holder is required", ErrInvalid)
	}
	if len(holder) > 64 {
		return fmt.Errorf("%w: lease holder must be at most 64 characters", ErrInvalid)
	}
	for _, character := range holder {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' && character != '.' && character != ':' {
			return fmt.Errorf("%w: lease holder may contain only letters, digits, '-', '_', '.' and ':'", ErrInvalid)
		}
	}
	return nil
}

func leaseTTL(seconds int) (time.Duration, error) {
	if seconds == 0 {
		seconds = DefaultLeaseTTLSeconds
	}
	if seconds < MinLeaseTTLSeconds || seconds > MaxLeaseTTLSeconds {
		return 0, fmt.Errorf(
			"%w: ttl_seconds must be between %d and %d",
			ErrInvalid, MinLeaseTTLSeconds, MaxLeaseTTLSeconds,
		)
	}
	return time.Duration(seconds) * time.Second, nil
}

// ClaimDeclaredNodeByControlToken acquires the exclusive lease of one declared
// node. A free node (never leased or lapsed lease) is granted immediately; the
// same holder may refresh its lease. A node leased by another holder is
// rejected with ErrLeaseHeld so two services can never share one IP window.
func (s *Service) ClaimDeclaredNodeByControlToken(
	ctx context.Context,
	token string,
	index int,
	request LeaseRequest,
) (ControlNode, error) {
	s.clientSessionMutex.Lock()
	defer s.clientSessionMutex.Unlock()
	channel, err := s.controlChannel(ctx, token)
	if err != nil {
		return ControlNode{}, err
	}
	if err := validateDeclaredIndex(channel, index); err != nil {
		return ControlNode{}, err
	}
	holder := strings.TrimSpace(request.Holder)
	if err := validateLeaseHolder(holder); err != nil {
		return ControlNode{}, err
	}
	ttl, err := leaseTTL(request.TTLSeconds)
	if err != nil {
		return ControlNode{}, err
	}
	session, err := s.repository.GetResidentialClientSession(ctx, channel.ID, declaredSessionID(index))
	if err != nil {
		return ControlNode{}, mapStoreError(err)
	}
	now := s.now().UTC()
	if leaseActive(session, now) && session.LeaseHolder != holder {
		return ControlNode{}, fmt.Errorf(
			"%w: node %d is leased by %q until %s",
			ErrLeaseHeld, index, session.LeaseHolder,
			session.LeaseExpiresAt.UTC().Format(time.RFC3339),
		)
	}
	leaseID, err := newToken()
	if err != nil {
		return ControlNode{}, err
	}
	expiresAt := now.Add(ttl)
	updated, err := s.repository.SetResidentialClientSessionLease(
		ctx, channel.ID, session.SessionID, holder, leaseID, &expiresAt,
	)
	if err != nil {
		return ControlNode{}, mapStoreError(err)
	}
	_ = s.repository.TouchResidentialClientSession(ctx, channel.ID, session.SessionID, now)
	node, err := s.controlNodeView(ctx, channel, index)
	if err != nil {
		return ControlNode{}, err
	}
	node.AllocVersion = updated.AllocVersion
	node.Lease = &ControlLease{
		Holder:    updated.LeaseHolder,
		LeaseID:   updated.LeaseID,
		ExpiresAt: updated.LeaseExpiresAt,
	}
	return node, nil
}

// HeartbeatDeclaredNodeByControlToken extends a held lease. It requires the
// lease_id returned by the original claim; a mismatched or lapsed lease fails
// so a stale holder cannot keep another service out of the window.
func (s *Service) HeartbeatDeclaredNodeByControlToken(
	ctx context.Context,
	token string,
	index int,
	request LeaseHeartbeatRequest,
) (ControlNode, error) {
	s.clientSessionMutex.Lock()
	defer s.clientSessionMutex.Unlock()
	channel, err := s.controlChannel(ctx, token)
	if err != nil {
		return ControlNode{}, err
	}
	if err := validateDeclaredIndex(channel, index); err != nil {
		return ControlNode{}, err
	}
	leaseID := strings.TrimSpace(request.LeaseID)
	if leaseID == "" {
		return ControlNode{}, fmt.Errorf("%w: lease_id is required", ErrInvalid)
	}
	ttl, err := leaseTTL(request.TTLSeconds)
	if err != nil {
		return ControlNode{}, err
	}
	session, err := s.repository.GetResidentialClientSession(ctx, channel.ID, declaredSessionID(index))
	if err != nil {
		return ControlNode{}, mapStoreError(err)
	}
	now := s.now().UTC()
	if !leaseActive(session, now) {
		return ControlNode{}, fmt.Errorf(
			"%w: node %d has no active lease; claim it first",
			ErrLeaseExpired, index,
		)
	}
	if session.LeaseID != leaseID {
		return ControlNode{}, fmt.Errorf(
			"%w: node %d is leased by %q under a different lease",
			ErrLeaseHeld, index, session.LeaseHolder,
		)
	}
	expiresAt := now.Add(ttl)
	updated, err := s.repository.SetResidentialClientSessionLease(
		ctx, channel.ID, session.SessionID, session.LeaseHolder, leaseID, &expiresAt,
	)
	if err != nil {
		return ControlNode{}, mapStoreError(err)
	}
	_ = s.repository.TouchResidentialClientSession(ctx, channel.ID, session.SessionID, now)
	node, err := s.controlNodeView(ctx, channel, index)
	if err != nil {
		return ControlNode{}, err
	}
	node.AllocVersion = updated.AllocVersion
	node.Lease = &ControlLease{
		Holder:    updated.LeaseHolder,
		LeaseID:   updated.LeaseID,
		ExpiresAt: updated.LeaseExpiresAt,
	}
	return node, nil
}

// ReleaseDeclaredNodeByControlToken releases a held lease. It is idempotent:
// releasing a node with no active lease succeeds so a client's shutdown path
// never needs to distinguish "already released" from "released now".
func (s *Service) ReleaseDeclaredNodeByControlToken(
	ctx context.Context,
	token string,
	index int,
	request LeaseReleaseRequest,
) (ControlNode, error) {
	s.clientSessionMutex.Lock()
	defer s.clientSessionMutex.Unlock()
	channel, err := s.controlChannel(ctx, token)
	if err != nil {
		return ControlNode{}, err
	}
	if err := validateDeclaredIndex(channel, index); err != nil {
		return ControlNode{}, err
	}
	leaseID := strings.TrimSpace(request.LeaseID)
	if leaseID == "" {
		return ControlNode{}, fmt.Errorf("%w: lease_id is required", ErrInvalid)
	}
	session, err := s.repository.GetResidentialClientSession(ctx, channel.ID, declaredSessionID(index))
	if err != nil {
		return ControlNode{}, mapStoreError(err)
	}
	now := s.now().UTC()
	if !leaseActive(session, now) || session.LeaseID != leaseID {
		// Idempotent success: the lease is already gone from this caller's
		// point of view.
		return s.controlNodeView(ctx, channel, index)
	}
	if _, err := s.repository.SetResidentialClientSessionLease(
		ctx, channel.ID, session.SessionID, "", "", nil,
	); err != nil {
		return ControlNode{}, mapStoreError(err)
	}
	return s.controlNodeView(ctx, channel, index)
}

// checkSessionGuard enforces the unified window standard before a consumer
// mutates a declared node's allocation or route. It must run inside the
// clientSessionMutex critical section together with the mutation it guards, so
// the state it reads cannot change before the mutation commits.
func (s *Service) checkSessionGuard(
	current store.ResidentialClientSessionRecord,
	options RotateOptions,
) error {
	now := s.now().UTC()
	if leaseActive(current, now) {
		if options.LeaseID == "" {
			return fmt.Errorf(
				"%w: node %s is leased by %q; hold the lease before rotating or switching its route",
				ErrLeaseHeld, current.SessionID, current.LeaseHolder,
			)
		}
		if options.LeaseID != current.LeaseID {
			return fmt.Errorf(
				"%w: node %s is leased by %q under a different lease",
				ErrLeaseHeld, current.SessionID, current.LeaseHolder,
			)
		}
	}
	if options.ExpectedAllocVersion != nil &&
		*options.ExpectedAllocVersion != current.AllocVersion {
		return fmt.Errorf(
			"%w: node %s allocation version is %d, expected %d; re-read the node pool",
			ErrAllocVersionChanged, current.SessionID, current.AllocVersion,
			*options.ExpectedAllocVersion,
		)
	}
	return nil
}
