package api

import (
	"github.com/HengXin666/hx-pxy/internal/metrics"
	"net/http"
)

type activeConnectionSource interface {
	ActiveConnections() metrics.ActiveConnections
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r, http.MethodGet)
		return
	}
	source, ok := s.traffic.(activeConnectionSource)
	if !ok {
		s.writeAPIError(w, r, 503, "connections_unavailable", "connection sampling unavailable")
		return
	}
	value := source.ActiveConnections()
	origin := r.URL.Query().Get("origin")
	if origin != "" && origin != "local" && origin != "docker" && origin != "external" && origin != "unknown" {
		s.writeAPIError(w, r, 422, "validation_failed", "invalid origin")
		return
	}
	if origin != "" {
		filtered := []metrics.ConnectionDetail{}
		for _, item := range value.Items {
			if item.Origin == origin {
				filtered = append(filtered, item)
			}
		}
		value.Items = filtered
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, value)
}
