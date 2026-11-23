package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/residential"
)

// residentialNodeExporter publishes the node exports of a residential channel
// that owns a share token. It is a local interface so listener-only deployments
// keep compiling without the residential service.
type residentialNodeExporter interface {
	ShareExportsByShareToken(context.Context, string, string) ([]listener.ShareExport, string, bool, error)
}

// handleConsumerNodes serves the programmatic node listing defined by
// docs/CONSUMER_INTEGRATION_CONTRACT.md.
//
// The share token travels in the path exactly like /sub/<token>, so a consumer
// that already holds a subscription URL already holds the listing URL. Every
// failure (unknown, disabled, not exportable) collapses into a bare 404: a
// distinct "wrong credential" reply would make listener existence probeable,
// and the contract has no retryable not-found because reading the node list is
// a pure read.
//
// The control plane only answers "which nodes exist and how do I reach them".
// Probing, ranking, retry and rotation scheduling belong to the consumer; the
// one server-side exception is a residential /ctl/next, which the consumer
// still has to call itself.
func (s *Server) handleConsumerNodes(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	if s.listeners == nil {
		http.NotFound(writer, request)
		return
	}
	token := strings.TrimPrefix(request.URL.Path, listener.ConsumerNodesPath)
	if token == "" || token == request.URL.Path || strings.Contains(token, "/") {
		http.NotFound(writer, request)
		return
	}
	// Share tokens are 16..64 characters; anything else cannot exist, and the
	// length gate keeps malformed input away from the repository.
	if len(token) < 16 || len(token) > 64 {
		http.NotFound(writer, request)
		return
	}
	sharePath := "/sub/" + token
	if exporter, ok := s.residential.(residentialNodeExporter); ok {
		exports, name, matched, err := exporter.ShareExportsByShareToken(request.Context(), token, request.Host)
		if err != nil {
			s.writeConsumerFailure(writer, request, err)
			return
		}
		if matched {
			bundle := listener.NewShareBundle(name, exports)
			s.writeConsumerPayload(writer, bundle.ConsumerPayload(sharePath))
			return
		}
	}
	export, err := s.listeners.ExportByShareToken(request.Context(), token, request.Host)
	if err != nil {
		s.writeConsumerFailure(writer, request, err)
		return
	}
	bundle := listener.NewShareBundle(export.Name, []listener.ShareExport{export})
	s.writeConsumerPayload(writer, bundle.ConsumerPayload(sharePath))
}

// writeConsumerFailure collapses every export failure into 404. The contract
// deliberately has no retryable not-found: a 404 means "check the token", and
// distinguishing it from "disabled" would leak whether a listener exists.
func (s *Server) writeConsumerFailure(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, listener.ErrShareDisabled) || errors.Is(err, listener.ErrNotFound) ||
		errors.Is(err, residential.ErrNotFound) || errors.Is(err, residential.ErrInvalid) {
		http.NotFound(writer, request)
		return
	}
	s.handleError(writer, request, err)
}

func (s *Server) writeConsumerPayload(writer http.ResponseWriter, payload listener.ConsumerPayload) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(writer, http.StatusOK, payload)
}
