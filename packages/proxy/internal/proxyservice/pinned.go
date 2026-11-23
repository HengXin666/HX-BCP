package proxyservice

import (
	"context"
	"fmt"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/proxygroup"
)

// Publishing a service whose egress is exactly one node.
//
// A proxy group is a selection, not a handle for one node, and
// proxygroup.Service.Create now refuses a one-member group. That leaves "one
// service, one node" needing a first-class path on this endpoint, or the error
// would point at something the caller cannot do here.
//
// See .agents/notes/implemented/architecture/2026-10-18-a-group-is-not-a-node-handle.md;
// this file exists so service.go stays under this repository's file-size target.

// createPinned publishes a service whose egress is one node. It builds no proxy
// group of its own, which is the whole point: a group around a single node is a
// handle, not a selection. The listener still lands on the family anchor group,
// because listeners.proxy_group_id is NOT NULL and the compiler requires that
// name to exist; that anchor carries none of this service's traffic.
func (s *Service) createPinned(ctx context.Context, request CreateRequest) (ServiceRecord, error) {
	if conflicts := groupCreationFields(request); len(conflicts) > 0 {
		return ServiceRecord{}, fmt.Errorf(
			"%w: listener.node_id pins this service to one node and creates no proxy group; omit %s",
			proxygroup.ErrInvalid, strings.Join(conflicts, ", "),
		)
	}
	created, err := s.createListener(ctx, "", request.Listener)
	if err != nil {
		return ServiceRecord{}, fmt.Errorf("%w: create listener: %v", ErrCreateFailed, err)
	}
	if err := s.syncSharedInbound(ctx); err != nil {
		cleanupErr := s.listeners.Delete(ctx, created.ID, created.Version)
		if cleanupErr == nil {
			cleanupErr = s.syncSharedInbound(ctx)
		}
		return ServiceRecord{}, fmt.Errorf("%w: publish shared inbound: %v; cleanup listener: %v", ErrCreateFailed, err, cleanupErr)
	}
	group, err := s.groups.Get(ctx, created.ProxyGroupID)
	if err != nil {
		// The listener exists and is serving; the anchor group is only needed to
		// fill the response's group field. Reporting the create as failed would
		// be a lie the caller cannot act on.
		return ServiceRecord{Listener: created}, nil
	}
	return ServiceRecord{Group: group, Listener: created}, nil
}
