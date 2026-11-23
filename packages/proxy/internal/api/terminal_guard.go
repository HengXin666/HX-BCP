package api

import "net/http"

// requireTerminalTwoFactor mirrors the terminal WebSocket gate: file and
// metrics preside over data of equivalent sensitivity to a root shell, so
// they require a configured & recently-verified 2FA session — just opening
// them does not grant anything the WebSocket itself would not.
func (s *Server) requireTerminalTwoFactor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if s.auth == nil {
			next.ServeHTTP(writer, request)
			return
		}
		status, err := s.auth.TwoFactorStatus(request.Context(), sessionToken(request))
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		if !status.Enabled {
			s.writeAPIError(writer, request, http.StatusForbidden, "terminal_requires_two_factor", "configure and enable two-factor authentication before using the terminal file manager")
			return
		}
		if !status.Verified {
			s.writeAPIError(writer, request, http.StatusForbidden, "terminal_requires_two_factor_verification", "verify a current two-factor authentication code before using the terminal file manager")
			return
		}
		next.ServeHTTP(writer, request)
	})
}
