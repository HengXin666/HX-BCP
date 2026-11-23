package proxyservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/proxygroup"
)

// Reusing an existing proxy group instead of creating one.
//
// A service is an egress anchor plus an entry point. Most callers want both
// built together, but a residential channel already owns a proxy group (see
// internal/residential/channel.go), so a second entry point onto the same exits
// must be able to name that group rather than build a duplicate. The decision,
// its rejected alternatives and its known costs live in
// .agents/notes/implemented/feature/2026-10-14-reuse-existing-group-for-an-entry.md;
// this file exists so service.go stays under this repository's file-size target.

// reuseGroup resolves the existing group a create request points its entry
// point at. Every failure that is the caller's to fix is reported as
// proxygroup.ErrInvalid, which the HTTP layer maps onto 422 validation_failed:
// a request naming a group that does not exist or is disabled is a bad request,
// not an internal error.
func (s *Service) reuseGroup(ctx context.Context, request CreateRequest, groupID string) (proxygroup.Group, error) {
	if conflicts := groupCreationFields(request); len(conflicts) > 0 {
		return proxygroup.Group{}, fmt.Errorf(
			"%w: listener.proxy_group_id reuses an existing proxy group; omit %s",
			proxygroup.ErrInvalid, strings.Join(conflicts, ", "),
		)
	}
	existing, err := s.groups.Get(ctx, groupID)
	if err != nil {
		if errors.Is(err, proxygroup.ErrNotFound) {
			return proxygroup.Group{}, fmt.Errorf("%w: proxy group %q does not exist", proxygroup.ErrInvalid, groupID)
		}
		return proxygroup.Group{}, fmt.Errorf("read reused proxy group %q: %w", groupID, err)
	}
	if !existing.Enabled {
		return proxygroup.Group{}, fmt.Errorf("%w: proxy group %q is disabled", proxygroup.ErrInvalid, groupID)
	}
	return existing, nil
}

// groupCreationFields lists the create-only fields a reuse request must not
// carry, in a stable order so the message is deterministic. Refusing them
// instead of ignoring them is deliberate: a caller that also described a group
// would otherwise believe it created the group it described, and this
// repository's rule is that a field which has no effect must not be accepted
// quietly. See
// .agents/notes/implemented/simplification/2026-10-01-service-shape-follows-source-class.md.
func groupCreationFields(request CreateRequest) []string {
	fields := make([]string, 0, 6)
	if strings.TrimSpace(request.Name) != "" {
		fields = append(fields, "name")
	}
	if strings.TrimSpace(request.Strategy) != "" {
		fields = append(fields, "strategy")
	}
	if !isEmptySourceSpec(request.SourceSpec) {
		fields = append(fields, "source_spec")
	}
	if request.Enabled != nil {
		fields = append(fields, "enabled")
	}
	if strings.TrimSpace(request.EmptyBehavior) != "" {
		fields = append(fields, "empty_behavior")
	}
	if strings.TrimSpace(request.DialerProxyGroup) != "" {
		fields = append(fields, "dialer_proxy_group_id")
	}
	return fields
}

func isEmptySourceSpec(spec proxygroup.SourceSpec) bool {
	return !spec.AllowEmpty &&
		len(spec.NodeIDs) == 0 &&
		len(spec.GroupIDs) == 0 &&
		len(spec.SubscriptionIDs) == 0 &&
		len(spec.NameKeywords) == 0 &&
		len(spec.Regions) == 0 &&
		len(spec.Protocols) == 0 &&
		len(spec.States) == 0 &&
		spec.MaxLatencyMS == 0 &&
		spec.SortBy == "" &&
		spec.Limit == 0 &&
		!spec.IncludeDirect &&
		spec.TestURL == "" &&
		spec.IntervalSeconds == 0 &&
		spec.Retain == nil
}
