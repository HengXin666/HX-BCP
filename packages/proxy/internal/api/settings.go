package api

import (
	"net/http"

	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

func (s *Server) handleSettings(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		settings, err := s.settings.Get(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	case http.MethodPut:
		var body systemsettings.Settings
		if err := decodeJSONBody(writer, request, &body); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		settings, err := s.settings.Update(request.Context(), body)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut)
	}
}
