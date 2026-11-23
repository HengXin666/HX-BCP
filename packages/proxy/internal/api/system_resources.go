package api

import (
	"net/http"

	"github.com/HengXin666/hx-pxy/internal/terminal"
)

// SystemResourceService provides a low-frequency host resource snapshot for
// the overview/dashboard. It is intentionally a thin wrapper so the same
// collector used by the terminal UI serves the overview page.
type SystemResourceService interface {
	HostSnapshot() terminal.HostSnapshot
}

// handleSystemResources returns one point-in-time host snapshot for the
// overview. The overview page polls at a coarse rate (every ~10s), so this is
// cheap: only the collector keeps /proc-based state and its polling is
// demand-driven. Unlike the terminal SSE, this endpoint does not require 2FA
// step-up because it exposes no shell authority — only utilization numbers.
func (s *Server) handleSystemResources(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	resolver, ok := s.systemResources()
	if !ok {
		s.writeAPIError(writer, request, http.StatusNotFound, "system_resources_unavailable", "host resource monitoring is not available")
		return
	}
	writeJSON(writer, http.StatusOK, resolver.HostSnapshot())
}

// systemResources resolves whichever server component implements the
// SystemResourceService surface (the terminal service), preferring the
// dedicated field when one is wired in the future.
func (s *Server) systemResources() (SystemResourceService, bool) {
	if s.terminal == nil {
		return nil, false
	}
	res, ok := s.terminal.(SystemResourceService)
	return res, ok
}
