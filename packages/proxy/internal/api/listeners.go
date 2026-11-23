package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

func (s *Server) handleListeners(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		items, err := s.listeners.List(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		if err := s.markDockerEntries(request, items); err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var createRequest listener.CreateRequest
		if err := decodeJSONBody(writer, request, &createRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		created, err := s.listeners.Create(request.Context(), createRequest)
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

// handleListenerShare serves the public token-addressed subscription export.
// The default response body is v2rayN's conventional base64 subscription.
func (s *Server) handleListenerShare(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	token := strings.TrimPrefix(request.URL.Path, "/sub/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(writer, request)
		return
	}
	if residentialShares, ok := s.residential.(residentialShareService); ok {
		bundle, matched, err := residentialShares.ExportByShareToken(request.Context(), token, request.Host)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		if matched {
			s.writeShareExport(writer, request, bundle)
			return
		}
	}
	if s.listeners == nil {
		http.NotFound(writer, request)
		return
	}
	export, err := s.listeners.ExportByShareToken(request.Context(), token, request.Host)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	s.writeShareExport(writer, request, export)
}

type shareRenderer interface {
	Render(string) (string, string, string, error)
}

func (s *Server) writeShareExport(writer http.ResponseWriter, request *http.Request, export shareRenderer) {
	format := requestedShareFormat(request)
	body, fileName, contentType, err := export.Render(format)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Content-Disposition", `attachment; filename="`+fileName+`"`)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("X-HX-Subscription-Format", canonicalShareFormat(format))
	writer.Header().Set("Vary", "User-Agent")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(body))
}

func requestedShareFormat(request *http.Request) string {
	if format := request.URL.Query().Get("format"); format != "" {
		return format
	}
	userAgent := strings.ToLower(request.UserAgent())
	switch {
	case strings.Contains(userAgent, "clash"), strings.Contains(userAgent, "mihomo"):
		return "clash"
	case strings.Contains(userAgent, "sing-box"):
		return "sing-box"
	default:
		return ""
	}
}

func canonicalShareFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "clash", "mihomo":
		return "clash"
	case "sing-box", "singbox":
		return "sing-box"
	case "uri":
		return "uri"
	default:
		return "v2rayn"
	}
}

func (s *Server) handleListener(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/listeners/")
	if path == "" || path == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	if identifier, action, found := strings.Cut(path, "/"); found {
		if action == "docker-access" && identifier != "" {
			s.handleListenerDockerAccess(writer, request, identifier)
			return
		}
		if action == "rotate-share" && identifier != "" {
			if request.Method != http.MethodPost {
				methodNotAllowed(writer, request, http.MethodPost)
				return
			}
			updated, err := s.listeners.RotateShareToken(request.Context(), identifier)
			if err != nil {
				s.handleError(writer, request, err)
				return
			}
			writeJSON(writer, http.StatusOK, updated)
			return
		}
		http.NotFound(writer, request)
		return
	}
	id := path
	switch request.Method {
	case http.MethodGet:
		item, err := s.listeners.Get(request.Context(), id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, item)
	case http.MethodPut:
		var updateRequest listener.UpdateRequest
		if err := decodeJSONBody(writer, request, &updateRequest); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := s.listeners.Update(request.Context(), id, updateRequest)
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
		// A Docker entry exists only to reach this service, so the Docker
		// access service deletes both: it rejects a stale version before
		// touching anything and removes the companion only after the source
		// delete succeeded.
		if s.dockerAccess != nil {
			err = s.dockerAccess.DeleteSource(request.Context(), id, version)
		} else {
			err = s.listeners.Delete(request.Context(), id, version)
		}
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}

func isListenerError(err error) bool {
	return errors.Is(err, listener.ErrNotFound) ||
		errors.Is(err, listener.ErrConflict) ||
		errors.Is(err, listener.ErrInvalid)
}
