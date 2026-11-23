package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/terminal"
)

// maxExecCommandLength mirrors the helper-side bound; the API rejects longer
// commands before they reach the socket.
const maxExecCommandLength = 16 * 1024

// handleTerminalExec runs one non-interactive command on the server through
// the privileged root helper and returns its captured output. It is the
// API-drive remote-management path (20260823 user request): authenticated
// operators can drive the VPS without a PTY session.
//
// Security model:
//   - authentication: cookie session or Bearer/X-API-Key machine credential
//     (authSession), enforced by requireAuth on the whole API surface;
//   - step-up: a cookie session must present a recently verified 2FA code
//     when 2FA is enabled; an API-key credential already carries
//     administrator authority and cannot answer a TOTP prompt, so it is
//     accepted as the machine equivalent of the verified step-up;
//   - the helper only accepts the configured local user on the privileged
//     socket and bounds command length, timeout and output size;
//   - every execution is audit-logged with the actor and remote address.
func (s *Server) handleTerminalExec(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	if s.terminal == nil {
		s.writeAPIError(writer, request, http.StatusNotFound, "terminal_disabled", "terminal feature is not configured")
		return
	}
	session, err := s.authSession(request)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	if apiKeyToken(request) == "" {
		status, statusErr := s.auth.TwoFactorStatus(request.Context(), session.Token)
		if statusErr != nil {
			s.handleError(writer, request, statusErr)
			return
		}
		if status.Enabled && !status.Verified {
			s.writeAPIError(writer, request, http.StatusForbidden, "terminal_exec_requires_two_factor", "verify a current two-factor authentication code before remote execution")
			return
		}
	}
	var payload struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32*1024)).Decode(&payload); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}
	command := strings.TrimSpace(payload.Command)
	if command == "" {
		s.writeAPIError(writer, request, http.StatusBadRequest, "command_required", "command is required")
		return
	}
	if len(command) > maxExecCommandLength {
		s.writeAPIError(writer, request, http.StatusBadRequest, "command_too_long", "command exceeds the length limit")
		return
	}
	timeout := time.Duration(payload.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if timeout > 300*time.Second {
		timeout = 300 * time.Second
	}
	result, err := s.terminal.Execute(request.Context(), command, timeout)
	if err != nil {
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "terminal_exec_unavailable", err.Error())
		return
	}
	auditCommand := command
	if len(auditCommand) > 120 {
		auditCommand = auditCommand[:120] + "…"
	}
	s.logger.Info(
		"remote exec",
		"audit", "terminal_exec",
		"actor", session.Username,
		"remote", clientAddress(request),
		"exit_code", result.ExitCode,
		"command", auditCommand,
	)
	writeJSON(writer, http.StatusOK, map[string]any{
		"exit_code": result.ExitCode,
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
	})
}

var _ = terminal.ExecResult{} // keep the terminal dependency explicit
