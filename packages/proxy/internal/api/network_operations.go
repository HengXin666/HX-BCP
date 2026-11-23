package api

import (
	"errors"
	"net/http"

	"github.com/HengXin666/hx-pxy/internal/hostnetwork"
)

func (s *Server) handleNetworkInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, r, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, hostnetwork.Inspect())
}

func (s *Server) handleNetworkOperation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r, http.MethodPost)
		return
	}
	var body struct {
		Action    string `json:"action"`
		Container string `json:"container_id"`
		Confirm   bool   `json:"confirm"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		s.writeAPIError(w, r, 400, "invalid_request", err.Error())
		return
	}
	if s.auth == nil {
		s.writeAPIError(w, r, 403, "network_requires_auth", "administrator authentication required")
		return
	}
	session, err := s.authSession(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if body.Action != "diagnose" {
		if !body.Confirm {
			s.writeAPIError(w, r, 422, "confirmation_required", "confirm the host-wide forwarding change")
			return
		}
		if apiKeyToken(r) == "" {
			status, err := s.auth.TwoFactorStatus(r.Context(), session.Token)
			if err != nil {
				s.handleError(w, r, err)
				return
			}
			if !status.Enabled || !status.Verified {
				s.writeAPIError(w, r, 403, "network_requires_two_factor", "enable and verify 2FA before network repair")
				return
			}
		}
	}
	if s.networkOps == nil {
		s.writeAPIError(w, r, 503, "network_unavailable", "privileged helper unavailable")
		return
	}
	result, err := s.networkOps.Run(r.Context(), body.Action, body.Container)
	if err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, hostnetwork.ErrInvalid) {
			code = http.StatusUnprocessableEntity
		}
		if errors.Is(err, hostnetwork.ErrBusy) {
			code = http.StatusConflict
		}
		s.writeAPIError(w, r, code, "network_operation_failed", err.Error())
		return
	}
	s.logger.Info("network operation", "audit", "network_operation", "actor", session.Username, "action", body.Action, "exit_code", result.ExitCode)
	writeJSON(w, http.StatusOK, result)
}
