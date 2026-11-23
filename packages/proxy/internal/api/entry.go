package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/entry"
	"github.com/HengXin666/hx-pxy/internal/slot"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// EntrySubscriptionPrefix is the public route of tenant Clash profiles.
const EntrySubscriptionPrefix = "/entry/sub/"

// EntryService is the unified entry as the API sees it.
type EntryService interface {
	Status(context.Context) entry.Status
	Nodes(string) []entry.Candidate
	Tenants() *entry.Tenants
	Slots(string) map[string][]slot.Entry
	ReleaseSlot(string, string)
	ShareToken(string) (string, error)
	Subscription(string, string, entry.ClashOptions) ([]byte, error)
}

// WithEntry wires the unified entry routes.
func WithEntry(service EntryService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("entry service is required")
		}
		server.entry = service
		return nil
	}
}

func (s *Server) registerEntryRoutes(mux *http.ServeMux) {
	if s.entry == nil {
		return
	}
	mux.HandleFunc("/api/v1/entry", s.handleEntryStatus)
	mux.HandleFunc("/api/v1/entry/nodes", s.handleEntryNodes)
	mux.HandleFunc("/api/v1/entry/tenants", s.handleEntryTenants)
	mux.HandleFunc("/api/v1/entry/tenants/", s.handleEntryTenant)
	mux.HandleFunc("/api/v1/entry/slots", s.handleEntrySlots)
	mux.HandleFunc("/api/v1/entry/slots/", s.handleEntrySlot)
	mux.HandleFunc(EntrySubscriptionPrefix, s.handleEntrySubscription)
}

func (s *Server) handleEntryStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, s.entry.Status(request.Context()))
}

func (s *Server) handleEntryNodes(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": s.entry.Nodes(request.URL.Query().Get("pool"))})
}

func (s *Server) handleEntryTenants(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		items, err := s.entry.Tenants().List(request.Context())
		if err != nil {
			s.handleEntryError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var body entry.TenantRequest
		if err := decodeJSONBody(writer, request, &body); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		created, err := s.entry.Tenants().Create(request.Context(), body)
		if err != nil {
			s.handleEntryError(writer, request, err)
			return
		}
		writer.Header().Set("Location", request.URL.Path+"/"+created.ID)
		writeJSON(writer, http.StatusCreated, created)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleEntryTenant(writer http.ResponseWriter, request *http.Request) {
	rest := strings.TrimPrefix(request.URL.Path, "/api/v1/entry/tenants/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.NotFound(writer, request)
		return
	}
	tenants := s.entry.Tenants()
	switch {
	case action == "reset-usage" && request.Method == http.MethodPost:
		if err := tenants.ResetUsage(request.Context(), id); err != nil {
			s.handleEntryError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	case action == "share" && request.Method == http.MethodGet:
		token, err := s.entry.ShareToken(id)
		if err != nil {
			s.handleEntryError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]string{"path": EntrySubscriptionPrefix + id + "/" + token})
	case action == "" && (request.Method == http.MethodPut || request.Method == http.MethodPatch):
		var body entry.TenantRequest
		if err := decodeJSONBody(writer, request, &body); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		updated, err := tenants.Update(request.Context(), id, body)
		if err != nil {
			s.handleEntryError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, updated)
	case action == "" && request.Method == http.MethodDelete:
		if err := tenants.Delete(request.Context(), id); err != nil {
			s.handleEntryError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	case action == "" || action == "reset-usage" || action == "share":
		methodNotAllowed(writer, request, http.MethodPut, http.MethodDelete, http.MethodPost, http.MethodGet)
	default:
		http.NotFound(writer, request)
	}
}

func (s *Server) handleEntrySlots(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, s.entry.Slots(request.URL.Query().Get("tenant")))
}

func (s *Server) handleEntrySlot(writer http.ResponseWriter, request *http.Request) {
	tenant, name, found := strings.Cut(strings.TrimPrefix(request.URL.Path, "/api/v1/entry/slots/"), "/")
	if !found || tenant == "" || name == "" {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodDelete {
		methodNotAllowed(writer, request, http.MethodDelete)
		return
	}
	s.entry.ReleaseSlot(tenant, name)
	writer.WriteHeader(http.StatusNoContent)
}

// handleEntrySubscription serves a tenant's Clash profile. The token is the only
// credential, the same model as /sub/.
func (s *Server) handleEntrySubscription(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	id, token, found := strings.Cut(strings.TrimPrefix(request.URL.Path, EntrySubscriptionPrefix), "/")
	if !found || id == "" || token == "" {
		http.NotFound(writer, request)
		return
	}
	query := request.URL.Query()
	options := entry.ClashOptions{Pool: query.Get("pool"), Protocol: query.Get("protocol"), Host: query.Get("host"), PanelHost: request.Host}
	if host, _, err := net.SplitHostPort(request.Host); err == nil {
		options.PanelHost = host
	}
	options.Port, _ = strconv.Atoi(query.Get("port"))
	options.Slots, _ = strconv.Atoi(query.Get("slots"))
	if countries := strings.TrimSpace(query.Get("cc")); countries != "" {
		options.Countries = strings.Split(countries, ",")
	}
	document, err := s.entry.Subscription(id, token, options)
	if err != nil {
		if errors.Is(err, entry.ErrNotFound) || errors.Is(err, store.ErrNotFound) {
			http.NotFound(writer, request)
			return
		}
		s.handleEntryError(writer, request, err)
		return
	}
	writer.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Disposition", `attachment; filename="hx-pxy.yaml"`)
	_, _ = writer.Write(document)
}

func (s *Server) handleEntryError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, entry.ErrNotFound):
		s.writeAPIError(writer, request, http.StatusNotFound, "not_found", "entry resource not found")
	case errors.Is(err, store.ErrConflict):
		s.writeAPIError(writer, request, http.StatusConflict, "conflict", "name taken or version changed, reload and retry")
	case errors.Is(err, entry.ErrInvalidTenant), errors.Is(err, entry.ErrInvalidUsername), errors.Is(err, entry.ErrPoolDenied):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "invalid_entry_request", err.Error())
	default:
		s.handleError(writer, request, err)
	}
}

// entryCapabilityEndpoints documents the unified entry for agents.
func entryCapabilityEndpoints() []CapabilityEndpoint {
	return []CapabilityEndpoint{
		{
			ID: "entry.status", Method: http.MethodGet, Path: "/api/v1/entry", Auth: "api-key",
			Summary: "Unified entry status: listen address, node count per pool (resi, subresi, sub, direct), enabled groups and residential gateways. Clients dial the entry with a dialect username such as acme-pool-resi-cc-us-session-job42-ttl-30.",
		},
		{
			ID: "entry.nodes.list", Method: http.MethodGet, Path: "/api/v1/entry/nodes", Auth: "api-key",
			Summary: "Routable subscription nodes with pool, country, ASN and exit measurement time. Filter with ?pool=subresi.",
		},
		{
			ID: "entry.tenant.list", Method: http.MethodGet, Path: "/api/v1/entry/tenants", Auth: "api-key",
			Summary: "List entry tenants with their allowed pools, residential quota and usage.",
		},
		{
			ID: "entry.tenant.create", Method: http.MethodPost, Path: "/api/v1/entry/tenants", Auth: "api-key",
			Summary: "Create an entry tenant. The secret is returned once and is the proxy password.",
			Fields: []CapabilityField{
				{Name: "name", Type: "string", Required: true, Description: "Letters, digits or underscore. First segment of the proxy username."},
				{Name: "pools", Type: "array<string>", Description: "Any of resi, subresi, sub, direct, group. Default sub, subresi, group."},
				{Name: "resi_quota_bytes", Type: "integer", Description: "Residential bytes allowed before reset, 0 for no limit."},
				{Name: "note", Type: "string"},
			},
			SeeAlso: []string{"entry.status"},
		},
		{
			ID: "entry.slots.list", Method: http.MethodGet, Path: "/api/v1/entry/slots", Auth: "api-key",
			Summary: "Live slot bindings: which node or residential session each tenant slot is bound to and when it expires. Filter with ?tenant=.",
		},
	}
}
