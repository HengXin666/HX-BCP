package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// handleProvisionConfig serves the config-center export.
//
//	GET /provision/<token>  -> plain text, one cf-worker subscription URL per line
//
// The token is the only credential (same model as /sub/ /rot/ /ctl/), and it
// must match the provision token configured in global settings. When the
// config center is disabled or the token is wrong, the endpoint answers 404 so
// the existence of the route is not probed. 20260821 user decision: pxy is a
// config center / authorization issuer — consumers pull these subscription
// URLs and dial the Cloudflare Workers directly, without relaying traffic
// through this control plane.
func (s *Server) handleProvisionConfig(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/provision/")
	if path == "" || path == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	// 路径只允许一个 token 段，禁止斜杠/点穿越。
	if strings.ContainsAny(path, "/.") {
		http.NotFound(writer, request)
		return
	}
	if s.settings == nil || s.residential == nil {
		http.NotFound(writer, request)
		return
	}
	settings, err := s.settings.Get(request.Context())
	if err != nil || !settings.Provision.Enabled {
		http.NotFound(writer, request)
		return
	}
	expected := []byte(strings.TrimSpace(settings.Provision.Token))
	given := []byte(path)
	if len(expected) == 0 || len(expected) != len(given) ||
		subtle.ConstantTimeCompare(expected, given) != 1 {
		http.NotFound(writer, request)
		return
	}
	subscriptions, err := s.residential.CfWorkerSubscriptions(request.Context())
	if err != nil {
		s.writeAPIError(writer, request, http.StatusInternalServerError, "provision_lookup_failed", "read cf-worker subscriptions failed")
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if len(subscriptions) == 0 {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	lines := make([]string, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		lines = append(lines, strings.TrimSpace(subscription.URL))
	}
	_, _ = writer.Write([]byte(strings.Join(lines, "\n") + "\n"))
}
