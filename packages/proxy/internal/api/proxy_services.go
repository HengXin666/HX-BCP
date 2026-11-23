package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/proxyservice"
)

func (s *Server) handleProxyServices(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var createRequest proxyservice.CreateRequest
	if err := decodeJSONBody(writer, request, &createRequest); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	created, err := s.proxyServices.Create(request.Context(), createRequest)
	if err != nil {
		if errors.Is(err, proxyservice.ErrCreateFailed) {
			s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "proxy_service_create_failed", err.Error())
			return
		}
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, created)
}

func (s *Server) handleProxyService(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/api/v1/proxy-services/")
	if id == "" || id == request.URL.Path || strings.Contains(id, "/") {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodPut {
		methodNotAllowed(writer, request, http.MethodPut)
		return
	}
	var updateRequest proxyservice.UpdateRequest
	if err := decodeJSONBody(writer, request, &updateRequest); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := bindProxyServiceTarget(id, &updateRequest); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	updated, err := s.proxyServices.Update(request.Context(), updateRequest)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, updated)
}

// bindProxyServiceTarget makes the request address authoritative for which
// service is being written, and rejects a body that cannot describe an update.
//
// The address used to be decorative: the handler parsed {id}, validated it, and
// then dropped it, so PUT /proxy-services/<any-id> edited whatever group_id the
// body named. That turned "update this service" into "update any service" for
// any caller holding a session, and it hid the real failure mode of a request
// whose body never arrived — an empty body decoded into a zero-valued
// UpdateRequest, which then looked up the empty id and answered 404, blaming a
// missing resource for a malformed request.
//
// The body may omit group_id, in which case the address supplies it; a body
// that contradicts the address is a client error, not a silent redirect.
func bindProxyServiceTarget(pathID string, request *proxyservice.UpdateRequest) error {
	request.GroupID = strings.TrimSpace(request.GroupID)
	request.ListenerID = strings.TrimSpace(request.ListenerID)
	switch {
	case request.GroupID == "":
		request.GroupID = pathID
	case request.GroupID != pathID:
		return fmt.Errorf("group_id %q does not match the addressed proxy service %q", request.GroupID, pathID)
	}
	if request.ListenerID == "" {
		return errors.New("listener_id is required")
	}
	// Both versions are optimistic-lock preconditions; a missing one is a
	// malformed request rather than a version conflict to report as 422.
	if request.GroupVersion < 1 {
		return errors.New("group_version is required and must be positive")
	}
	if request.ListenerVersion < 1 {
		return errors.New("listener_version is required and must be positive")
	}
	return nil
}
