package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/proxygroup"
)

// handleGroupMembers reports which nodes a group actually holds.
//
// See .agents/notes/implemented/architecture/2026-10-04-group-selection-endpoints.md
//
// `Get` cannot answer this: a group stores a source spec (subscriptions +
// filters), so its membership is only known after the same resolution the
// compiler runs. Without this endpoint a consumer sees "one node rejected" and
// concludes the group is dead, discarding the other 150 members.
func (s *Server) handleGroupMembers(writer http.ResponseWriter, request *http.Request, id string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	view, err := s.proxyGroups.Members(request.Context(), id)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	// `now` is runtime state, not configuration: read it from the live data
	// plane. A failure here must not fail the whole read — the member list is
	// still useful, and the caller can tell "unknown" (empty) from a name.
	if s.dataplane != nil {
		if live, readErr := s.dataplane.ReadGroup(request.Context(), view.Name); readErr == nil {
			view.Now = live.Now
		}
	}
	writeJSON(writer, http.StatusOK, view)
}

// handleGroupSelection points a group at one of its members.
//
// Rejected for non-manual strategies instead of accepted-and-ignored: url-test
// re-picks by latency and load-balance rotates, so a selection there would be
// silently undone and the caller would believe it had changed egress.
func (s *Server) handleGroupSelection(writer http.ResponseWriter, request *http.Request, id string) {
	if request.Method != http.MethodPut {
		methodNotAllowed(writer, request, http.MethodPut)
		return
	}
	var body struct {
		Proxy string `json:"proxy"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	view, err := s.proxyGroups.Members(request.Context(), id)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	if !view.Selectable {
		s.writeAPIError(writer, request, http.StatusConflict, "group_not_selectable",
			"group "+view.Name+" uses strategy "+view.Strategy+": it picks its own member, so a manual selection would not stick")
		return
	}
	if s.dataplane == nil {
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "dataplane_unavailable", "mihomo is not running")
		return
	}
	target := strings.TrimSpace(body.Proxy)
	if target == "" {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "proxy is required")
		return
	}
	if !containsString(view.Members, target) {
		s.writeAPIError(writer, request, http.StatusBadRequest, "unknown_member",
			target+" is not a member of group "+view.Name)
		return
	}
	if err := s.dataplane.SelectProxy(request.Context(), view.Name, target); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "group": view.Name, "now": target})
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func (s *Server) handleProxyGroups(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		// ?type=residential|subscription|custom (comma-separated) filters by
		// group type; an unknown value is a 422, never silently ignored.
		filter, err := proxygroup.ParseTypeFilter(request.URL.Query().Get("type"))
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		items, err := s.listProxyGroups(request.Context(), filter)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var createRequest proxygroup.CreateRequest
		if err := decodeJSONBody(writer, request, &createRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		created, err := s.proxyGroups.Create(request.Context(), createRequest)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.Header().Set("Location", request.URL.Path+"/"+created.ID)
		writeJSON(writer, http.StatusCreated, created)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProxyGroup(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/api/v1/proxy-groups/")
	if id == "" || id == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	// Sub-resources: /proxy-groups/<id>/members and /selection.
	//
	// `members` answers "which nodes does this group actually hold?" — the
	// question `Get` cannot answer, because a group stores a *spec*
	// (subscriptions + filters), not a member list. A consumer that cannot see
	// members treats one rejected node as the whole group being spent, which is
	// wrong for a group holding 150+.
	if parts := strings.Split(id, "/"); len(parts) == 2 {
		switch parts[1] {
		case "members":
			s.handleGroupMembers(writer, request, parts[0])
			return
		case "selection":
			s.handleGroupSelection(writer, request, parts[0])
			return
		}
	}
	if strings.Contains(id, "/") {
		http.NotFound(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		item, err := s.proxyGroups.Get(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, item)
	case http.MethodPut:
		var updateRequest proxygroup.UpdateRequest
		if err := decodeJSONBody(writer, request, &updateRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := s.proxyGroups.Update(request.Context(), id, updateRequest)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	case http.MethodDelete:
		version, err := parseIntegerQuery(request, "version", 0, 1, 1_000_000_000)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if err := s.proxyGroups.Delete(request.Context(), id, version); err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func isProxyGroupError(err error) bool {
	return errors.Is(err, proxygroup.ErrNotFound) ||
		errors.Is(err, proxygroup.ErrConflict) ||
		errors.Is(err, proxygroup.ErrInvalid)
}

// listProxyGroups lists groups, keeping only the types in filter (nil = all).
// Residential channel groups and subscription groups share the resource but
// must be separable by every caller; see proxygroup/group_type.go.
func (s *Server) listProxyGroups(ctx context.Context, filter map[string]bool) ([]proxygroup.Group, error) {
	items, err := s.proxyGroups.List(ctx)
	if err != nil || filter == nil {
		return items, err
	}
	kept := make([]proxygroup.Group, 0, len(items))
	for _, item := range items {
		if filter[item.Type] {
			kept = append(kept, item)
		}
	}
	return kept, nil
}
