package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/HengXin666/hx-pxy/internal/quickstart"
)

// QuickstartPath is the one-call endpoint that reaches a published proxy.
//
// The individual resource endpoints remain the source of truth and are still
// what the UI uses; this exists because reaching a working proxy otherwise
// needs several ordered calls, and the order is not discoverable from the
// endpoints alone. See GET /api/v1/capabilities for the full catalog.
const QuickstartPath = "/api/v1/quickstart"

// quickstartBodyLimit matches the subscription body limit: this endpoint can
// carry a whole proxy list as source_config inline content, so it must accept at
// least as much as the subscriptions endpoint that stores it.
const quickstartBodyLimit = subscriptionBodyLimit

// QuickstartService builds a working proxy from one request. It is a local
// interface so a deployment that omits the orchestration still compiles.
type QuickstartService interface {
	Create(context.Context, quickstart.Request) (quickstart.Result, error)
}

func (s *Server) handleQuickstart(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	if s.quickstart == nil {
		http.NotFound(writer, request)
		return
	}
	var createRequest quickstart.Request
	// A proxy list rides in the request body, so this endpoint needs the same
	// body ceiling the subscriptions endpoint uses rather than the default 64 KiB
	// — a few thousand endpoints exceed it, and the failure would look like a
	// malformed request instead of "your list is too long".
	if err := decodeJSONBodyWithLimit(writer, request, &createRequest, quickstartBodyLimit); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := s.quickstart.Create(request.Context(), createRequest)
	if err != nil {
		switch {
		case errors.Is(err, quickstart.ErrInvalid):
			s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "invalid_quickstart", err.Error())
		case errors.Is(err, quickstart.ErrCreateFailed):
			s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "quickstart_failed", err.Error())
		default:
			s.handleError(writer, request, err)
		}
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}
