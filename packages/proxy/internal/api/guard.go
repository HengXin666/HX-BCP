package api

import (
	"net/http"

	"github.com/HengXin666/hx-pxy/internal/auth"
)

// Agent Note: .agents/notes/implemented/architecture/2026-10-01-api-cannot-grow-the-group-graph.md
//
// This file owns one decision: which write surfaces an API-key caller may not
// use while the operator has the group-creation guard on. It is deliberately a
// separate file because the decision has three call sites (proxy-groups,
// proxy-services, quickstart) and this repository has already paid for letting
// one fact acquire two judge functions — they drift, and the drift is only
// visible as a behaviour difference nobody can explain.
//
// The guard is expressed as "these three paths, for machine credentials only".
// It is not a general-purpose authorization layer and must not grow into one:
// the moment it starts encoding per-route permissions it becomes a second RBAC
// system maintained by hand next to the real authentication.

// groupCreationGuardPaths lists the requests that would grow the proxy-group
// graph. Creating a group is the only operation the guard blocks; editing,
// deleting and every non-group write stay open to machine credentials.
//
// POST /api/v1/proxy-services and POST /api/v1/quickstart are on the list
// because both create a group as their first step: guarding only the literal
// group endpoint would leave the exact shortcut this guard exists to close.
// Their PUT counterparts are absent on purpose — editing a service cannot add
// a group, so blocking it would break unattended callers for no benefit.

var groupCreationGuardPaths = map[string]struct{}{
	"/api/v1/proxy-groups":   {},
	"/api/v1/proxy-services": {},
	"/api/v1/quickstart":     {},
}

// denyMachineGroupCreation reports whether this request must be rejected
// before it reaches a handler.
//
// The settings are read per request rather than from a cached copy: an operator
// who turns the guard on expects the next request to obey it, and the settings
// service caches nothing. The cost is one metadata read on three write paths,
// which is noise next to the writes themselves.
func (s *Server) denyMachineGroupCreation(request *http.Request, session auth.Session) bool {
	if request.Method != http.MethodPost {
		return false
	}
	if _, guarded := groupCreationGuardPaths[request.URL.Path]; !guarded {
		return false
	}
	if !session.MachineCredential {
		return false
	}
	if s.settings == nil {
		return false
	}
	settings, err := s.settings.Get(request.Context())
	if err != nil {
		// A settings read that fails must not silently disable the guard: the
		// safer default for a guard is to hold. Failing closed is recoverable
		// (the operator sees the error and fixes storage) whereas letting a
		// guarded write through is not.
		return true
	}
	// Load() always fills the field (Default() supplies the pointer and Update
	// merges an absent one), so nil here would mean the settings document was
	// built by hand without going through the service. Treat that as "off", the
	// documented default, rather than failing closed and locking the operator
	// out of the API for a field they never set.
	return settings.Guard.DenyAPIGroupCreate != nil && *settings.Guard.DenyAPIGroupCreate
}
