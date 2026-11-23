package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/HengXin666/hx-pxy/internal/routingrules"
)

func (s *Server) handleRoutingRules(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		config, err := s.routingRules.Get(request.Context())
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, config)
	case http.MethodPut:
		var body routingRulesPut
		if err := decodeJSONBody(writer, request, &body); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		// default and applies_to are accepted so a stale rules page does not
		// fail decoding, and ignored so that page cannot overwrite a YAML
		// apply. UpdateRuleSets reloads the stored intent under the service lock.
		updated, err := s.routingRules.UpdateRuleSets(request.Context(), body.RuleSets)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPut)
	}
}

// handleRoutingRulesApply accepts the YAML intent document and replaces the
// stored routing rules. The body is the document itself, not a JSON wrapper,
// so a fingerprint-browser profile can post the file it already keeps.
func (s *Server) handleRoutingRulesApply(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "payload_too_large", "yaml body is larger than 64KiB")
			return
		}
		s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "yaml body could not be read")
		return
	}
	config, err := s.routingRules.ApplyYAML(request.Context(), string(raw))
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, config)
}

// routingRulesPut decodes default and applies_to so an older rules page still
// parses, but the handler never writes them. YAML apply owns those fields.
type routingRulesPut struct {
	Default   *string                `json:"default"`
	AppliesTo *[]string              `json:"applies_to"`
	RuleSets  []routingrules.RuleSet `json:"rule_sets"`
}
