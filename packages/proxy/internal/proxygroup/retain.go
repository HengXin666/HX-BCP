package proxygroup

import (
	"context"
	"fmt"
	"time"

	"github.com/HengXin666/hx-pxy/internal/pipeline"
)

// memberHistory is the optional repository capability that reads the
// membership each group was last applied with (store.ListGroupMembers).
type memberHistory interface {
	ListGroupMembers(context.Context) (map[string]map[string]time.Time, error)
}

// incumbents reads a group's previous membership so the members view runs the
// same Retain decision the compiler does.
func (s *Service) incumbents(ctx context.Context, groupID string) (pipeline.Incumbents, error) {
	history, ok := s.repository.(memberHistory)
	if !ok {
		return pipeline.Incumbents{}, nil
	}
	all, err := history.ListGroupMembers(ctx)
	if err != nil {
		return pipeline.Incumbents{}, err
	}
	return pipeline.Incumbents{JoinedAt: all[groupID], Now: time.Now()}, nil
}

// validateRetain checks the optional Retain stage of a group. Retain only
// changes anything when the group caps its members (limit > 0), so a retain
// spec without a limit is refused rather than silently ignored: this
// repository's rule is that a field with no effect must not be accepted
// quietly. See .agents/notes/implemented/feature/2026-10-16-group-member-retain.md.
func validateRetain(spec SourceSpec) error {
	if spec.Retain == nil {
		return nil
	}
	if spec.Limit <= 0 {
		return fmt.Errorf("%w: retain requires limit > 0", ErrInvalid)
	}
	// Retain is a subscription-pool policy. Residential channel groups own
	// their exits through sessions and leases and must never be reshaped by
	// subscription ranking, so a group that is not subscription-sourced is
	// refused outright rather than silently ignored.
	if len(spec.SubscriptionIDs) == 0 {
		return fmt.Errorf("%w: retain only applies to subscription-sourced groups", ErrInvalid)
	}
	if err := spec.Retain.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}
