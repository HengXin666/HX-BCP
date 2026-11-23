package proxygroup

import (
	"context"
	"fmt"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// Group types. Residential channel groups and subscription groups are both
// proxy groups, but they are different products: a residential group's exits
// are owned by a channel (sessions, leases, rotation) while a subscription
// group selects among nodes imported from subscriptions. API and SDK callers
// must be able to tell them apart and filter on it, so the type is an explicit
// field rather than something a caller infers from source_spec.
//
// See .agents/notes/implemented/feature/2026-10-16-proxy-group-type.md.
const (
	TypeResidential  = "residential"
	TypeSubscription = "subscription"
	// TypeCustom is every other group: explicit node lists, compositions of
	// other groups, DIRECT-only, or the anchor rows pinned listeners use.
	TypeCustom = "custom"
)

// ValidType reports whether value is a known group type.
func ValidType(value string) bool {
	switch value {
	case TypeResidential, TypeSubscription, TypeCustom:
		return true
	default:
		return false
	}
}

// ParseTypeFilter validates a comma-separated ?type= filter. Empty means no
// filter. An unknown value is a caller error, never silently ignored.
func ParseTypeFilter(raw string) (map[string]bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	filter := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		value := strings.ToLower(strings.TrimSpace(part))
		if !ValidType(value) {
			return nil, fmt.Errorf("%w: type must be one of %s, %s, %s", ErrInvalid, TypeResidential, TypeSubscription, TypeCustom)
		}
		filter[value] = true
	}
	return filter, nil
}

// typeFromSpec is the type a group has on its own record. A residential
// channel's group is recognised by ownership (see residentialGroupIDs), not by
// its spec, because the channel is what makes it residential.
func typeFromSpec(spec SourceSpec) string {
	if len(spec.SubscriptionIDs) > 0 {
		return TypeSubscription
	}
	return TypeCustom
}

// residentialOwners is the optional repository capability that lists
// residential channels; their proxy_group_id names the residential groups.
type residentialOwners interface {
	ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error)
}

func (s *Service) residentialGroupIDs(ctx context.Context) (map[string]bool, error) {
	owners, ok := s.repository.(residentialOwners)
	if !ok {
		return map[string]bool{}, nil
	}
	channels, err := owners.ListResidentialChannels(ctx)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(channels))
	for _, channel := range channels {
		if channel.ProxyGroupID != "" {
			ids[channel.ProxyGroupID] = true
		}
	}
	return ids, nil
}

// classify stamps the residential type on channel-owned groups.
func (s *Service) classify(ctx context.Context, groups []Group) error {
	residential, err := s.residentialGroupIDs(ctx)
	if err != nil {
		return err
	}
	for index := range groups {
		if residential[groups[index].ID] {
			groups[index].Type = TypeResidential
		}
	}
	return nil
}

func (s *Service) classifyOne(ctx context.Context, group Group) (Group, error) {
	groups := []Group{group}
	if err := s.classify(ctx, groups); err != nil {
		return Group{}, err
	}
	return groups[0], nil
}
