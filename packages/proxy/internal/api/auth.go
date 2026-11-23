package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/auth"
)

const sessionCookieName = "hx_session"

type AuthService interface {
	Configured(context.Context) (bool, error)
	Setup(ctx context.Context, setupToken, username, password string) error
	Login(ctx context.Context, clientKey, username, password string) (auth.Session, error)
	Authenticate(ctx context.Context, token string) (auth.Session, error)
	AuthenticateAPIKey(ctx context.Context, key string) (auth.Session, error)
	Logout(ctx context.Context, token string) error
	LogoutAll(ctx context.Context) error
	ChangePassword(ctx context.Context, currentPassword, newPassword string) error
	ChangeUsername(ctx context.Context, currentPassword, newUsername string) error
	TwoFactorStatus(ctx context.Context, token string) (auth.TwoFactorStatus, error)
	BeginTwoFactorSetup(ctx context.Context) (auth.TwoFactorSetup, error)
	EnableTwoFactor(ctx context.Context, code string) error
	DisableTwoFactor(ctx context.Context, code string) error
	VerifyTwoFactor(ctx context.Context, token, clientKey, code string) error
	RenewTwoFactorVerification(ctx context.Context, token string) error
	CreateAPIKey(ctx context.Context, name string) (auth.APIKey, error)
	ListAPIKeys(ctx context.Context) ([]auth.APIKey, error)
	RevokeAPIKey(ctx context.Context, id string) error
}

func WithAuth(service AuthService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("auth service is required")
		}
		server.auth = service
		return nil
	}
}

func (s *Server) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/v1/auth/setup", s.handleAuthSetup)
	mux.HandleFunc("/api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/v1/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/v1/auth/logout-all", s.handleAuthLogoutAll)
	mux.HandleFunc("/api/v1/auth/password", s.handleAuthPassword)
	mux.HandleFunc("/api/v1/auth/username", s.handleAuthUsername)
	mux.HandleFunc("/api/v1/auth/2fa/status", s.handleAuthTwoFactorStatus)
	mux.HandleFunc("/api/v1/auth/2fa/setup", s.handleAuthTwoFactorSetup)
	mux.HandleFunc("/api/v1/auth/2fa/enable", s.handleAuthTwoFactorEnable)
	mux.HandleFunc("/api/v1/auth/2fa/disable", s.handleAuthTwoFactorDisable)
	mux.HandleFunc("/api/v1/auth/2fa/verify", s.handleAuthTwoFactorVerify)
	mux.HandleFunc("/api/v1/auth/api-keys", s.handleAuthAPIKeys)
	mux.HandleFunc("/api/v1/auth/api-keys/", s.handleAuthAPIKey)
}

func (s *Server) handleAuthAPIKeys(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		items, err := s.auth.ListAPIKeys(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeJSONBody(writer, request, &body); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		created, err := s.auth.CreateAPIKey(request.Context(), body.Name)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.Header().Set("Location", request.URL.Path+"/"+created.ID)
		writeJSON(writer, http.StatusCreated, created)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleAuthAPIKey(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/auth/api-keys/")
	if path == "" || path == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodDelete {
		methodNotAllowed(writer, request, http.MethodDelete)
		return
	}
	if err := s.auth.RevokeAPIKey(request.Context(), path); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// requireAuth guards /api/v1/* once the administrator account exists.
// Before setup completes the API stays open, which is safe because the
// management listener is forced onto a loopback address until then.
// Mutating requests must also present the session's CSRF token.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	exempt := map[string]struct{}{
		"/api/v1/auth/status": {},
		"/api/v1/auth/setup":  {},
		"/api/v1/auth/login":  {},
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if s.auth == nil || !strings.HasPrefix(request.URL.Path, "/api/v1/") {
			next.ServeHTTP(writer, request)
			return
		}
		if _, isExempt := exempt[request.URL.Path]; isExempt {
			next.ServeHTTP(writer, request)
			return
		}
		configured, err := s.auth.Configured(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		if !configured {
			next.ServeHTTP(writer, request)
			return
		}
		session, err := s.authSession(request)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		switch request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			// API-key sessions carry an empty CSRF token: header credentials
			// are not attached by browsers automatically, so the CSRF gate
			// only applies to cookie sessions.
			if session.CSRFToken != "" && request.Header.Get("X-CSRF-Token") != session.CSRFToken {
				s.writeAPIError(writer, request, http.StatusForbidden, "csrf_token_mismatch", "missing or invalid CSRF token")
				return
			}
		}
		// The group-creation guard is checked after authentication resolves the
		// credential, because who is calling is the whole question it answers;
		// see denyMachineGroupCreation. It sits before dispatch so a guarded
		// request never reaches a handler that has already begun writing.
		if s.denyMachineGroupCreation(request, session) {
			s.writeAPIError(writer, request, http.StatusForbidden, "api_group_create_denied",
				"creating proxy groups through the API is disabled; create it in the web UI, or clear guard.deny_api_group_create in settings")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// authSession resolves the request credential to a session. A bearer API key
// (Authorization: Bearer <key> or X-API-Key: <key>) takes precedence over the
// session cookie, so scripts never need the login/CSRF dance.
func (s *Server) authSession(request *http.Request) (auth.Session, error) {
	if key := apiKeyToken(request); key != "" {
		return s.auth.AuthenticateAPIKey(request.Context(), key)
	}
	return s.auth.Authenticate(request.Context(), sessionToken(request))
}

// apiKeyToken extracts a machine credential from the request headers.
func apiKeyToken(request *http.Request) string {
	if key := strings.TrimSpace(request.Header.Get("X-API-Key")); key != "" {
		return key
	}
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	const scheme = "Bearer "
	if len(authorization) > len(scheme) && strings.EqualFold(authorization[:len(scheme)], scheme) {
		return strings.TrimSpace(authorization[len(scheme):])
	}
	return ""
}

func (s *Server) handleAuthStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	configured, err := s.auth.Configured(request.Context())
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	response := map[string]any{"configured": configured, "authenticated": false}
	if configured {
		if session, err := s.authSession(request); err == nil {
			response["authenticated"] = true
			response["username"] = session.Username
			response["csrf_token"] = session.CSRFToken
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) handleAuthSetup(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var body struct {
		SetupToken string `json:"setup_token"`
		Username   string `json:"username"`
		Password   string `json:"password"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.auth.Setup(request.Context(), body.SetupToken, body.Username, body.Password); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]string{"status": "configured"})
}

func (s *Server) handleAuthLogin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	session, err := s.auth.Login(request.Context(), clientAddress(request), body.Username, body.Password)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	http.SetCookie(writer, sessionCookie(request, session.Token, 0))
	writeJSON(writer, http.StatusOK, map[string]any{
		"username":   session.Username,
		"csrf_token": session.CSRFToken,
		"expires_at": session.ExpiresAt,
	})
}

func (s *Server) handleAuthLogout(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	if err := s.auth.Logout(request.Context(), sessionToken(request)); err != nil {
		s.handleError(writer, request, err)
		return
	}
	http.SetCookie(writer, sessionCookie(request, "", -1))
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAuthLogoutAll(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	if err := s.auth.LogoutAll(request.Context()); err != nil {
		s.handleError(writer, request, err)
		return
	}
	http.SetCookie(writer, sessionCookie(request, "", -1))
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAuthPassword(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPut {
		methodNotAllowed(writer, request, http.MethodPut)
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.auth.ChangePassword(request.Context(), body.CurrentPassword, body.NewPassword); err != nil {
		s.handleError(writer, request, err)
		return
	}
	http.SetCookie(writer, sessionCookie(request, "", -1))
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAuthUsername(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPut {
		methodNotAllowed(writer, request, http.MethodPut)
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewUsername     string `json:"new_username"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.auth.ChangeUsername(request.Context(), body.CurrentPassword, body.NewUsername); err != nil {
		s.handleError(writer, request, err)
		return
	}
	http.SetCookie(writer, sessionCookie(request, "", -1))
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAuthTwoFactorStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	status, err := s.auth.TwoFactorStatus(request.Context(), sessionToken(request))
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) handleAuthTwoFactorSetup(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	setup, err := s.auth.BeginTwoFactorSetup(request.Context())
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, setup)
}

func (s *Server) handleAuthTwoFactorEnable(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.auth.EnableTwoFactor(request.Context(), body.Code); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "enabled"})
}

func (s *Server) handleAuthTwoFactorDisable(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.auth.DisableTwoFactor(request.Context(), body.Code); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "disabled"})
}

func (s *Server) handleAuthTwoFactorVerify(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSONBody(writer, request, &body); err != nil {
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.auth.VerifyTwoFactor(request.Context(), sessionToken(request), clientAddress(request), body.Code); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "verified"})
}

func sessionToken(request *http.Request) string {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func sessionCookie(request *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	}
}

func clientAddress(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}
