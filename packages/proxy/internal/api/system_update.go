package api

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleSystemUpdate(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	if s.auth == nil {
		s.writeAPIError(writer, request, http.StatusForbidden, "update_requires_auth", "automatic update requires administrator authentication")
		return
	}
	// authSession resolves both the session cookie and Bearer/X-API-Key machine
	// credentials, so scripts can trigger the update directly (20260823 user
	// request: support updating through the API).
	session, err := s.authSession(request)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	// The 2FA step-up only applies to interactive cookie sessions: an API-key
	// credential already carries administrator authority and cannot answer a
	// TOTP prompt. A cookie session with 2FA disabled may also update directly;
	// a verified code is required only when 2FA is actually enabled.
	if apiKeyToken(request) == "" {
		twoFactor, twoFactorErr := s.auth.TwoFactorStatus(request.Context(), session.Token)
		if twoFactorErr != nil {
			s.handleError(writer, request, twoFactorErr)
			return
		}
		if twoFactor.Enabled && !twoFactor.Verified {
			s.writeAPIError(writer, request, http.StatusForbidden, "update_requires_two_factor", "verify a current two-factor authentication code before updating")
			return
		}
	}

	updateContext, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	if err := s.updater.TriggerUpdate(updateContext); err != nil {
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "update_unavailable", err.Error())
		return
	}
	s.logger.Info(
		"automatic update scheduled",
		"audit", "system_update",
		"actor", session.Username,
		"remote", clientAddress(request),
	)
	writeJSON(writer, http.StatusAccepted, map[string]bool{"accepted": true})
}
