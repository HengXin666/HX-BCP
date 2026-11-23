package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/residential"
)

// handleResidentialPresets serves the vendor preset catalog. Presets carry a
// `verified` flag so the UI can warn that an unverified gateway syntax must be
// confirmed with a test connection before it is trusted.
func (s *Server) handleResidentialPresets(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"items":            residential.Presets(),
		"placeholders":     residential.SupportedPlaceholders(),
		"protocols":        residential.SupportedProtocols(),
		"worker_protocols": residential.SupportedWorkerProtocols(),
		"rotation_modes":   residential.SupportedRotationModes(),
		"region_modes":     residential.SupportedRegionModes(),
		"exit_ip_default":  residential.DefaultExitIPEndpoint,
	})
}

func (s *Server) handleResidentialProviders(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		items, err := s.residential.ListProviders(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var createRequest residential.CreateProviderRequest
		if err := decodeJSONBody(writer, request, &createRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		created, err := s.residential.CreateProvider(request.Context(), createRequest)
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

func (s *Server) handleResidentialProvider(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/residential/providers/")
	if path == "" || path == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	if identifier, action, found := strings.Cut(path, "/"); found {
		if action != "test" || identifier == "" {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, request, http.MethodPost)
			return
		}
		var testRequest struct {
			ExitIPEndpoint string `json:"exit_ip_endpoint,omitempty"`
		}
		if err := decodeJSONBody(writer, request, &testRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := s.residential.TestProvider(request.Context(), identifier, testRequest.ExitIPEndpoint)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	id := path
	switch request.Method {
	case http.MethodGet:
		item, err := s.residential.GetProvider(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, item)
	case http.MethodPut:
		var updateRequest residential.UpdateProviderRequest
		if err := decodeJSONBody(writer, request, &updateRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := s.residential.UpdateProvider(request.Context(), id, updateRequest)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	case http.MethodDelete:
		version, err := parseIntegerQuery(request, "version", 0, 1, 1_000_000_000)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if err := s.residential.DeleteProvider(request.Context(), id, version); err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func (s *Server) handleResidentialChannels(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		items, err := s.residential.ListChannels(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var createRequest residential.CreateChannelRequest
		if err := decodeJSONBody(writer, request, &createRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		created, err := s.residential.CreateChannel(request.Context(), createRequest)
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

func (s *Server) handleResidentialChannel(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/residential/channels/")
	if path == "" || path == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	if identifier, action, found := strings.Cut(path, "/"); found {
		if identifier == "" {
			http.NotFound(writer, request)
			return
		}
		s.handleResidentialChannelAction(writer, request, identifier, action)
		return
	}
	id := path
	switch request.Method {
	case http.MethodGet:
		item, err := s.residential.GetChannel(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, item)
	case http.MethodPut:
		var updateRequest residential.UpdateChannelRequest
		if err := decodeJSONBody(writer, request, &updateRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := s.residential.UpdateChannel(request.Context(), id, updateRequest)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	case http.MethodDelete:
		version, err := parseIntegerQuery(request, "version", 0, 1, 1_000_000_000)
		if err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if err := s.residential.DeleteChannel(request.Context(), id, version); err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

// handleResidentialChannelAction serves the administrator-side channel actions.
func (s *Server) handleResidentialChannelAction(
	writer http.ResponseWriter,
	request *http.Request,
	id string,
	action string,
) {
	if strings.HasPrefix(action, "sessions/") {
		parts := strings.Split(action, "/")
		if len(parts) != 3 || parts[2] != "next" || request.Method != http.MethodPost {
			if len(parts) == 3 && parts[2] == "next" {
				methodNotAllowed(writer, request, http.MethodPost)
			} else {
				http.NotFound(writer, request)
			}
			return
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		service, ok := s.residential.(residentialDeclaredControlService)
		if !ok {
			s.writeAPIError(writer, request, http.StatusNotImplemented, "residential_control_unavailable", "residential node control is unavailable")
			return
		}
		updated, err := service.RotateDeclaredSession(request.Context(), id, index)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
		return
	}
	switch action {
	case "rotate":
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, request, http.MethodPost)
			return
		}
		result, err := s.residential.RotateChannel(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case "rotate-token":
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, request, http.MethodPost)
			return
		}
		updated, err := s.residential.RotateChannelToken(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	case "rotate-share":
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, request, http.MethodPost)
			return
		}
		service, ok := s.residential.(residentialDeclaredControlService)
		if !ok {
			s.writeAPIError(writer, request, http.StatusNotImplemented, "residential_control_unavailable", "residential node control is unavailable")
			return
		}
		updated, err := service.RotateChannelShareToken(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	case "rotate-control":
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, request, http.MethodPost)
			return
		}
		service, ok := s.residential.(residentialDeclaredControlService)
		if !ok {
			s.writeAPIError(writer, request, http.StatusNotImplemented, "residential_control_unavailable", "residential node control is unavailable")
			return
		}
		updated, err := service.RotateChannelControlToken(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	case "refresh-pool":
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, request, http.MethodPost)
			return
		}
		if err := s.residential.RefreshChannelPool(request.Context(), id); err != nil {
			s.handleError(writer, request, err)
			return
		}
		item, err := s.residential.GetChannel(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, item)
	default:
		http.NotFound(writer, request)
	}
}

type residentialShareService interface {
	ExportByShareToken(context.Context, string, string) (listener.ShareBundle, bool, error)
}

type residentialDeclaredControlService interface {
	ControlNodesByToken(context.Context, string) (residential.ControlNodeList, error)
	RotateDeclaredSession(context.Context, string, int) (residential.ChannelSession, error)
	RotateDeclaredSessionByControlToken(context.Context, string, int, residential.RotateOptions) (residential.ControlNode, error)
	SwitchDeclaredSessionRouteByControlToken(context.Context, string, int, string, residential.RotateOptions) (residential.ControlNode, error)
	ClaimDeclaredNodeByControlToken(context.Context, string, int, residential.LeaseRequest) (residential.ControlNode, error)
	HeartbeatDeclaredNodeByControlToken(context.Context, string, int, residential.LeaseHeartbeatRequest) (residential.ControlNode, error)
	ReleaseDeclaredNodeByControlToken(context.Context, string, int, residential.LeaseReleaseRequest) (residential.ControlNode, error)
	RotateChannelShareToken(context.Context, string) (residential.Channel, error)
	RotateChannelControlToken(context.Context, string) (residential.Channel, error)
}

func (s *Server) handleResidentialControlPublic(writer http.ResponseWriter, request *http.Request) {
	service, ok := s.residential.(residentialDeclaredControlService)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/ctl/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	token := parts[0]
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if len(parts) == 2 && parts[1] == "nodes" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, request, http.MethodGet)
			return
		}
		result, err := service.ControlNodesByToken(request.Context(), token)
		if err != nil {
			s.handleResidentialControlError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	if len(parts) != 4 || parts[1] != "nodes" || request.Method != http.MethodPost {
		if len(parts) == 4 && parts[1] == "nodes" {
			methodNotAllowed(writer, request, http.MethodPost)
		} else {
			http.NotFound(writer, request)
		}
		return
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	var node residential.ControlNode
	switch parts[3] {
	case "next":
		var options residential.RotateOptions
		if decodeErr := decodeJSONBody(writer, request, &options); decodeErr != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		node, err = service.RotateDeclaredSessionByControlToken(request.Context(), token, index, options)
	case "route":
		var body struct {
			RouteMode            string `json:"route_mode"`
			LeaseID              string `json:"lease_id"`
			ExpectedAllocVersion *int   `json:"expected_alloc_version"`
		}
		if decodeErr := decodeJSONBody(writer, request, &body); decodeErr != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		node, err = service.SwitchDeclaredSessionRouteByControlToken(
			request.Context(), token, index, body.RouteMode,
			residential.RotateOptions{
				LeaseID:              body.LeaseID,
				ExpectedAllocVersion: body.ExpectedAllocVersion,
			},
		)
	case "claim":
		var body residential.LeaseRequest
		if decodeErr := decodeJSONBody(writer, request, &body); decodeErr != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		node, err = service.ClaimDeclaredNodeByControlToken(request.Context(), token, index, body)
	case "heartbeat":
		var body residential.LeaseHeartbeatRequest
		if decodeErr := decodeJSONBody(writer, request, &body); decodeErr != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		node, err = service.HeartbeatDeclaredNodeByControlToken(request.Context(), token, index, body)
	case "release":
		var body residential.LeaseReleaseRequest
		if decodeErr := decodeJSONBody(writer, request, &body); decodeErr != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		node, err = service.ReleaseDeclaredNodeByControlToken(request.Context(), token, index, body)
	default:
		http.NotFound(writer, request)
		return
	}
	if err != nil {
		s.handleResidentialControlError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, node)
}

func (s *Server) handleResidentialControlError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, residential.ErrNotFound) {
		http.NotFound(writer, request)
		return
	}
	s.handleError(writer, request, err)
}

// handleResidentialRotatePublic serves the consumer-facing rotation API.
//
//	GET    /rot/<token>                              -> legacy channel status
//	POST   /rot/<token>/next                         -> legacy channel rotation
//	PUT    /rot/<token>/sessions/<id>                -> ensure logical session
//	GET    /rot/<token>/sessions/<id>                -> logical session status
//	DELETE /rot/<token>/sessions/<id>                -> release logical session
//	POST   /rot/<token>/sessions/<id>/next           -> rotate only this session
//	POST   /rot/<token>/sessions/<id>/route          -> residential/direct
//
// The token is the only credential, so this route is intentionally outside the
// authenticated /api/v1 namespace. Responses never include the token, the
// gateway credentials or the pool contents, and rotation is rate limited per
// channel by the service.
func (s *Server) handleResidentialRotatePublic(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/rot/")
	if path == "" || path == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	token := parts[0]

	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")

	if len(parts) == 1 {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, request, http.MethodGet)
			return
		}
		status, err := s.residential.ChannelStatusByToken(request.Context(), token)
		if err != nil {
			s.handleRotateError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, status)
		return
	}
	if len(parts) >= 3 && parts[1] == "sessions" {
		s.handleResidentialClientSessionPublic(writer, request, token, parts[2:])
		return
	}
	if len(parts) != 2 || parts[1] != "next" {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	result, err := s.residential.RotateChannelByToken(request.Context(), token)
	if err != nil {
		s.handleRotateError(writer, request, err)
		return
	}
	// The channel id identifies a private resource, so the public response omits
	// it and reports only what the consumer needs.
	writeJSON(writer, http.StatusOK, map[string]any{
		"session_index":  result.SessionIndex,
		"pool_size":      result.PoolSize,
		"exit_ip":        result.ExitIP,
		"latency_ms":     result.LatencyMS,
		"rotated_at":     result.RotatedAt,
		"pool_refreshed": result.PoolRefreshed,
	})
}

func (s *Server) handleResidentialClientSessionPublic(
	writer http.ResponseWriter,
	request *http.Request,
	token string,
	parts []string,
) {
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	sessionID := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if action == "config" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, request, http.MethodGet)
			return
		}
		service, ok := s.residential.(residentialClientSessionConfigService)
		if !ok {
			s.writeAPIError(writer, request, http.StatusNotImplemented, "client_config_unsupported", "residential client config is unavailable")
			return
		}
		session, err := service.ClientSessionConfigByToken(request.Context(), token, sessionID)
		if err != nil {
			s.handleResidentialClientSessionError(writer, request, err)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Pragma", "no-cache")
		format := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("format")))
		if format == "json" {
			writeJSON(writer, http.StatusOK, session)
			return
		}
		if format != "" && format != "clash" && format != "yaml" && format != "yml" {
			s.writeAPIError(writer, request, http.StatusBadRequest, "unsupported_format", "format must be clash or json")
			return
		}
		config, err := residential.ClashConfig(session)
		if err != nil {
			s.handleResidentialClientSessionError(writer, request, err)
			return
		}
		writer.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		writer.Header().Set("X-HX-Subscription-Format", "clash")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(config)
		return
	}
	if action == "" {
		switch request.Method {
		case http.MethodPut:
			var options residential.ClientSessionOptions
			if decodeErr := decodeJSONBody(writer, request, &options); decodeErr != nil {
				s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", decodeErr.Error())
				return
			}
			session, err := s.ensureResidentialClientSession(
				request.Context(), token, sessionID, options,
			)
			if err != nil {
				s.handleResidentialClientSessionError(writer, request, err)
				return
			}
			writeJSON(writer, http.StatusOK, session)
		case http.MethodGet:
			session, err := s.residential.GetClientSessionByToken(request.Context(), token, sessionID)
			if err != nil {
				s.handleResidentialClientSessionError(writer, request, err)
				return
			}
			writeJSON(writer, http.StatusOK, session)
		case http.MethodDelete:
			if err := s.residential.DeleteClientSessionByToken(request.Context(), token, sessionID); err != nil {
				s.handleResidentialClientSessionError(writer, request, err)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			methodNotAllowed(writer, request, http.MethodPut, http.MethodGet, http.MethodDelete)
		}
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	var (
		session residential.ClientSession
		err     error
	)
	switch action {
	case "next":
		session, err = s.residential.RotateClientSessionByToken(request.Context(), token, sessionID)
	case "route":
		var routeRequest struct {
			RouteMode string `json:"route_mode"`
		}
		if decodeErr := decodeJSONBody(writer, request, &routeRequest); decodeErr != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		session, err = s.residential.SwitchClientSessionRouteByToken(
			request.Context(), token, sessionID, routeRequest.RouteMode,
		)
	default:
		http.NotFound(writer, request)
		return
	}
	if err != nil {
		s.handleResidentialClientSessionError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

type residentialClientSessionOptionsService interface {
	EnsureClientSessionByTokenWithOptions(
		context.Context,
		string,
		string,
		residential.ClientSessionOptions,
	) (residential.ClientSession, error)
}

type residentialClientSessionConfigService interface {
	ClientSessionConfigByToken(context.Context, string, string) (residential.ClientSession, error)
}

func (s *Server) ensureResidentialClientSession(
	ctx context.Context,
	token, sessionID string,
	options residential.ClientSessionOptions,
) (residential.ClientSession, error) {
	if options.CountryCode != "" {
		if service, ok := s.residential.(residentialClientSessionOptionsService); ok {
			return service.EnsureClientSessionByTokenWithOptions(ctx, token, sessionID, options)
		}
		return residential.ClientSession{}, fmt.Errorf("country_code session options are unsupported")
	}
	return s.residential.EnsureClientSessionByToken(ctx, token, sessionID)
}

// Session endpoints use ErrNotFound for opaque token/channel lookup failures.
// Other validation errors belong to caller-controlled session parameters and
// should retain the regular 422 API semantics.
func (s *Server) handleResidentialClientSessionError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, residential.ErrSessionExpired) {
		s.writeAPIError(writer, request, http.StatusGone, "session_expired", "residential client session expired")
		return
	}
	if errors.Is(err, residential.ErrNotFound) {
		http.NotFound(writer, request)
		return
	}
	s.handleError(writer, request, err)
}

// handleRotateError keeps public rotation failures opaque: an unknown, disabled
// or non-rotatable token must be indistinguishable from a wrong one so the route
// cannot be used to enumerate channels.
func (s *Server) handleRotateError(writer http.ResponseWriter, request *http.Request, err error) {
	if isResidentialLookupFailure(err) {
		http.NotFound(writer, request)
		return
	}
	s.handleError(writer, request, err)
}

func isResidentialLookupFailure(err error) bool {
	return errors.Is(err, residential.ErrNotFound) || errors.Is(err, residential.ErrInvalid)
}
