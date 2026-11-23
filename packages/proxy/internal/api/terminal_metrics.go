package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/HengXin666/hx-pxy/internal/terminal"
)

// TerminalResourceService exposes host resource sampling for the terminal UI.
type TerminalResourceService interface {
	HostSnapshot() terminal.HostSnapshot
}

// handleTerminalMetrics streams host + process resource samples for the
// terminal UI. The 1s cadence is coarse enough to keep CPU overhead on the
// order of reading a handful of /proc files per second, and fine enough for a
// FinalShell-style resource panel.
func (s *Server) handleTerminalMetrics(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	if s.terminal == nil {
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "terminal_unavailable", "terminal service is not running")
		return
	}
	res, ok := s.terminal.(TerminalResourceService)
	if !ok {
		s.writeAPIError(writer, request, http.StatusNotImplemented, "terminal_metrics_unsupported", "terminal metrics are not supported by this build")
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		s.writeAPIError(writer, request, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache, no-store")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	write := func() error {
		sample := res.HostSnapshot()
		payload, err := json.Marshal(sample)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "data: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := write(); err != nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
			if err := write(); err != nil {
				return
			}
		}
	}
}
