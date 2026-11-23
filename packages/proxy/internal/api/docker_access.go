package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/HengXin666/hx-pxy/internal/dockeraccess"
	"github.com/HengXin666/hx-pxy/internal/listener"
)

// DockerAccessService assesses, previews, applies and revokes the companion
// entries that let a container dial a proxy service. See
// .agents/notes/implemented/feature/2026-10-15-docker-access-entry.md.
type DockerAccessService interface {
	Assess(context.Context, string, string) (dockeraccess.Assessment, error)
	Apply(context.Context, string, dockeraccess.ApplyRequest) (dockeraccess.Assessment, error)
	Revoke(context.Context, string) error
	DeleteSource(context.Context, string, int) error
	Links(context.Context) ([]dockeraccess.Link, error)
}

// DockerAccessPath lists source -> companion links so the service list can
// label companion rows and remove them together with their source.
const DockerAccessPath = "/api/v1/docker-access"

func WithDockerAccess(service DockerAccessService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("docker access service is required")
		}
		server.dockerAccess = service
		return nil
	}
}

func (s *Server) handleDockerAccessLinks(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	links, err := s.dockerAccess.Links(request.Context())
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": links})
}

// handleListenerDockerAccess serves /api/v1/listeners/{id}/docker-access.
// GET is the assessment and preview; POST applies a previewed plan and
// requires confirm=true plus the plan id it was shown; DELETE revokes the
// companion entry.
func (s *Server) handleListenerDockerAccess(writer http.ResponseWriter, request *http.Request, id string) {
	if s.dockerAccess == nil {
		http.NotFound(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		result, err := s.dockerAccess.Assess(request.Context(), id, request.URL.Query().Get("gateway"))
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodPost:
		var body dockeraccess.ApplyRequest
		if err := decodeJSONBody(writer, request, &body); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := s.dockerAccess.Apply(request.Context(), id, body)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodDelete:
		if err := s.dockerAccess.Revoke(request.Context(), id); err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPost, http.MethodDelete)
	}
}

// markDockerEntries labels companion rows in a listener listing so no client
// mistakes a Docker entry for an ordinary service bound where it says.
func (s *Server) markDockerEntries(request *http.Request, items []listener.Listener) error {
	if s.dockerAccess == nil {
		return nil
	}
	links, err := s.dockerAccess.Links(request.Context())
	if err != nil {
		return err
	}
	linkOf := make(map[string]dockeraccess.Link, len(links))
	for _, link := range links {
		linkOf[link.EntryListenerID] = link
	}
	for index := range items {
		link := linkOf[items[index].ID]
		items[index].DockerAccessFor = link.ListenerID
		items[index].DockerAccessWithheld = link.Withheld
	}
	return nil
}
