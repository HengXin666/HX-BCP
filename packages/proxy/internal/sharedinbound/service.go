// Package sharedinbound migrates existing proxy services onto the shared
// entry points.
//
// The control plane used to give every service its own bound port. Switching
// the shared inbound on must therefore move the services that already exist,
// not only the ones created afterwards; otherwise an operator would end up with
// both models at once and would have to delete and recreate every service by
// hand.
//
// The migration is deliberately conservative: it only runs when the shared mode
// is explicitly enabled, it never touches a residential channel's managed WebSocket
// listener (those carry per-session IN-USER routes of their own), and it skips
// any service that has no credentials because the shared inbound identifies a
// member by username.
package sharedinbound

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// Repository reads the rows the migration has to reason about.
type Repository interface {
	ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error)
	// ListDockerAccessEntries names the companion entries created for
	// container access. A companion bound to a bridge gateway exists precisely
	// because the shared entry was not reachable, so folding it back onto the
	// shared entry would undo the operator's confirmed fix.
	ListDockerAccessEntries(context.Context) ([]store.DockerAccessEntryRecord, error)
}

// ListenerService reads and rewrites the managed listeners.
type ListenerService interface {
	List(context.Context) ([]listener.Listener, error)
	Update(context.Context, string, listener.UpdateRequest) (listener.Listener, error)
	// EnsureSharedInbounds publishes the aggregate rows after a migration.
	EnsureSharedInbounds(context.Context, listener.SharedInboundSpec, []store.ProxyGroupRecord, []listener.SharedInboundMember) ([]listener.Listener, error)
}

// SettingsReader exposes the configured entry points.
type SettingsReader interface {
	SharedInboundSpec(ctx context.Context) (listener.SharedInboundSpec, bool, error)
}

// Result reports what the migration did, so the caller can log or surface it.
type Result struct {
	// Migrated counts the services moved onto a shared entry point.
	Migrated int
	// SkippedWithoutCredentials lists services left on their own port because
	// the shared inbound could not route them.
	SkippedWithoutCredentials []string
}

// Service performs the migration.
type migrationTarget struct {
	owner       string
	bindAddress string
	port        int
	transport   listener.Transport
}

type Service struct {
	repository Repository
	listeners  ListenerService
	settings   SettingsReader
}

func NewService(repository Repository, listeners ListenerService, settings SettingsReader) (*Service, error) {
	if repository == nil || listeners == nil || settings == nil {
		return nil, errors.New("shared inbound migration requires a repository, a listener service and settings")
	}
	return &Service{repository: repository, listeners: listeners, settings: settings}, nil
}

// Reconcile moves every eligible service onto the shared entry points.
//
// It is idempotent: a service already marked as a member is skipped, so running
// it on every settings apply only ever costs one listing.
func (s *Service) Reconcile(ctx context.Context) (Result, error) {
	spec, enabled, err := s.settings.SharedInboundSpec(ctx)
	if err != nil {
		return Result{}, err
	}
	if !enabled {
		return Result{}, nil
	}
	residential, err := s.residentialListeners(ctx)
	if err != nil {
		return Result{}, err
	}
	current, err := s.listeners.List(ctx)
	if err != nil {
		return Result{}, err
	}
	// Deterministic order keeps the optimistic-lock versions predictable and
	// makes a failure report reproducible.
	sort.Slice(current, func(left, right int) bool { return current[left].ID < current[right].ID })

	result := Result{}
	for _, item := range current {
		target, migratable := targetFor(item, spec)
		if !migratable {
			continue
		}
		if _, isResidential := residential[item.ID]; isResidential {
			continue
		}
		if !item.AuthConfigured {
			// Without a username Mihomo would accept every connection on the
			// aggregate port, so the service stays on its own port until the
			// administrator gives it credentials.
			result.SkippedWithoutCredentials = append(result.SkippedWithoutCredentials, item.Name)
			continue
		}
		request := listener.UpdateRequest{
			Version:        item.Version,
			Name:           item.Name,
			Kind:           item.Kind,
			BindAddress:    target.bindAddress,
			Port:           target.port,
			ProxyGroupID:   item.ProxyGroupID,
			NodeID:         item.NodeID,
			Transport:      target.transport,
			PublicEndpoint: item.PublicEndpoint,
			SharedInbound:  target.owner,
			Enabled:        item.Enabled,
		}
		if _, err := s.listeners.Update(ctx, item.ID, request); err != nil {
			return result, fmt.Errorf("migrate service %q onto the shared inbound: %w", item.Name, err)
		}
		result.Migrated++
	}

	if result.Migrated == 0 {
		return result, nil
	}
	// Publish the aggregate rows once, after the last member moved. The
	// listener service applies the compiled configuration itself, so the caller
	// must not apply again.
	if _, err := s.listeners.EnsureSharedInbounds(ctx, spec, nil, nil); err != nil {
		return result, err
	}
	return result, nil
}

// targetFor reports where a listener should live under the shared model.
// A residential managed listener is never a target: its IN-USER routes are
// scoped to a dedicated channel entry point.
func targetFor(item listener.Listener, spec listener.SharedInboundSpec) (migrationTarget, bool) {
	if strings.TrimSpace(item.SharedInbound) != "" {
		return migrationTarget{}, false
	}
	switch {
	case item.Kind == "mixed" || item.Kind == "http" || item.Kind == "socks":
		if !spec.Enabled {
			return migrationTarget{}, false
		}
		return migrationTarget{
			owner:       listener.SharedInboundStandardOwner,
			bindAddress: spec.MixedBindAddress,
			port:        spec.MixedPort,
			transport:   item.Transport,
		}, true
	case item.Kind == "vless" || item.Kind == "vmess" || item.Kind == "trojan":
		if !spec.IncludeWebSocket {
			return migrationTarget{}, false
		}
		// Every WebSocket protocol of one family shares a single path so one
		// client URL works for all of them.
		return migrationTarget{
			owner:       listener.SharedInboundWebSocketOwner,
			bindAddress: "127.0.0.1",
			port:        spec.WSPort,
			transport:   listener.Transport{Type: "ws", WSPath: listener.SharedInboundRoutePath()},
		}, true
	default:
		return migrationTarget{}, false
	}
}

func (s *Service) residentialListeners(ctx context.Context) (map[string]struct{}, error) {
	channels, err := s.repository.ListResidentialChannels(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := s.repository.ListDockerAccessEntries(ctx)
	if err != nil {
		return nil, err
	}
	skip := make(map[string]struct{}, len(channels)*2+len(entries))
	for _, entry := range entries {
		skip[entry.EntryListenerID] = struct{}{}
	}
	for _, channel := range channels {
		if channel.ListenerID != "" {
			skip[channel.ListenerID] = struct{}{}
		}
		if channel.DirectListenerID != "" {
			skip[channel.DirectListenerID] = struct{}{}
		}
	}
	return skip, nil
}
