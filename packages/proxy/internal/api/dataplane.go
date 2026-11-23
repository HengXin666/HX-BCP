package api

import (
	"net/http"
)

func (s *Server) handleDataPlaneStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, s.dataplane.Status())
}

func (s *Server) handleDataPlaneApply(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	if err := s.dataplane.Apply(request.Context()); err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, s.dataplane.Status())
}
