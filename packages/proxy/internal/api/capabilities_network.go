package api

import "net/http"

// See .agents/notes/implemented/feature/2026-09-16-admin-capability-catalog.md.
func networkCapabilityEndpoints() []CapabilityEndpoint {
	return []CapabilityEndpoint{
		{ID: "network.addresses", Method: http.MethodGet, Path: "/api/v1/system/network", Auth: "api-key", Summary: "Host IPv4 candidates for Docker copies, not a reachability guarantee."},
		{ID: "network.inspect", Method: http.MethodGet, Path: "/api/v1/system/network/inspect", Auth: "api-key", Summary: "Read interface kinds, visible TUN device and IPv4 forwarding state."},
		{ID: "connections.read", Method: http.MethodGet, Path: "/api/v1/connections", Auth: "api-key", Summary: "Read at most 500 current sampled connections. Optional origin query: local, docker, external or unknown. No stored connection history."},
		{ID: "network.operate", Method: http.MethodPost, Path: "/api/v1/system/network/operations", Auth: "api-key or session with recent 2FA for repairs", Summary: "Bounded network diagnostics or explicit runtime forwarding changes; no firewall reset or Docker restart.", Fields: []CapabilityField{
			{Name: "action", Type: "string", Required: true, Description: "One of diagnose, enable-forwarding, disable-forwarding."},
			{Name: "container_id", Type: "string", Description: "Only for diagnose: 12-64 lowercase hex characters. Empty diagnoses host."},
			{Name: "confirm", Type: "boolean", Description: "Must be true for either forwarding change."},
		}, Example: map[string]any{"action": "diagnose"}, Errors: []CapabilityError{
			{Status: 422, Code: "confirmation_required", Meaning: "Repair requires explicit confirmation."},
			{Status: 403, Code: "network_requires_two_factor", Meaning: "Browser repair requires enabled and recently verified 2FA."},
			{Status: 409, Code: "network_operation_failed", Meaning: "Another operation is running."},
			{Status: 503, Code: "network_unavailable", Meaning: "Privileged helper is unavailable."},
		}},
	}
}
