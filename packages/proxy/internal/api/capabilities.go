package api

import (
	"net/http"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/residential"
	"github.com/HengXin666/hx-pxy/internal/routingrules"
	"github.com/HengXin666/hx-pxy/internal/subscription"
)

// CapabilityCatalogPath is the machine-readable capability catalog. It lives in
// the session/API-key authenticated /api/v1 namespace, not in the public token
// namespace: the catalog describes administrative write surfaces, so an
// unauthenticated caller must not be able to enumerate them.
const CapabilityCatalogPath = "/api/v1/capabilities"

// CapabilityEnum is one closed vocabulary. Values are always the live list the
// validator accepts, never a copy, so a new enum member cannot be documented
// without also being accepted (and vice versa).
type CapabilityEnum struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Values      []string `json:"values"`
	// Default is the value applied when the field is omitted, when one exists.
	Default string `json:"default,omitempty"`
}

// CapabilityField documents one request field.
type CapabilityField struct {
	Name string `json:"name"`
	// Type is the JSON type an agent should send.
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	// Enum names the CapabilityEnum that closes this field's values.
	Enum string `json:"enum,omitempty"`
	// Default is applied by the server when the field is omitted.
	Default any `json:"default,omitempty"`
	// Example is a value that passes validation.
	Example any `json:"example,omitempty"`
	// SeeAlso names endpoints that must be called first, or whose output feeds
	// this field.
	SeeAlso []string `json:"see_also,omitempty"`
}

// CapabilityEndpoint documents one management endpoint: its method, path, the
// path parameters a caller must substitute, every request field, and a body
// that is known to be accepted.
type CapabilityEndpoint struct {
	ID          string            `json:"id"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Summary     string            `json:"summary"`
	Auth        string            `json:"auth"`
	PathParams  []string          `json:"path_params,omitempty"`
	Fields      []CapabilityField `json:"fields,omitempty"`
	Example     map[string]any    `json:"example,omitempty"`
	Errors      []CapabilityError `json:"errors,omitempty"`
	SeeAlso     []string          `json:"see_also,omitempty"`
	ResponseRef string            `json:"response_ref,omitempty"`
}

// CapabilityError is one classified failure a caller is expected to handle.
type CapabilityError struct {
	Status      int    `json:"status"`
	Code        string `json:"code"`
	Meaning     string `json:"meaning"`
	Remediation string `json:"remediation,omitempty"`
}

// CapabilityWorkflow is the ordered set of calls that reaches a working proxy.
// It exists because the endpoints alone do not say which order makes a usable
// service: a listener that references an empty group carries no traffic.
type CapabilityWorkflow struct {
	ID          string   `json:"id"`
	Summary     string   `json:"summary"`
	Steps       []string `json:"steps"`
	FastPath    string   `json:"fast_path,omitempty"`
	Description string   `json:"description,omitempty"`
}

// CapabilityCatalog is the response body of GET /api/v1/capabilities.
type CapabilityCatalog struct {
	// Version of the catalog schema itself, bumped when its shape changes.
	Version string `json:"version"`
	// ApplicationVersion is the running control-plane version.
	ApplicationVersion string `json:"application_version,omitempty"`
	// Overview explains the trust model an agent must respect.
	Overview string `json:"overview"`
	// Authentication describes how a program authenticates.
	Authentication CapabilityAuth `json:"authentication"`
	// Namespaces explains which paths are public and which are admin-only.
	Namespaces []CapabilityNamespace `json:"namespaces"`
	Enums      []CapabilityEnum      `json:"enums"`
	Endpoints  []CapabilityEndpoint  `json:"endpoints"`
	Workflows  []CapabilityWorkflow  `json:"workflows"`
	// NotSupported states the product boundary so an agent does not design
	// around capabilities the control plane deliberately does not have.
	NotSupported []string `json:"not_supported"`
	// ObjectModel states the type distinctions a caller must hold in mind. It
	// exists because the ids are opaque strings and the field names differ by a
	// plural `s`, which is exactly how an agent ends up putting a group id where
	// a node id belongs. See capabilityObjectModel.
	ObjectModel []string `json:"object_model"`
	Related     []string `json:"related"`
}

// CapabilityAuth documents the credential an automating caller presents.
type CapabilityAuth struct {
	Scheme  string   `json:"scheme"`
	Headers []string `json:"headers"`
	Notes   []string `json:"notes"`
}

// CapabilityNamespace distinguishes the public token space from admin routes.
type CapabilityNamespace struct {
	Prefix      string `json:"prefix"`
	Auth        string `json:"auth"`
	Purpose     string `json:"purpose"`
	ExamplePath string `json:"example_path,omitempty"`
}

// capabilityCatalogVersion is the schema version of this catalog document.
const capabilityCatalogVersion = "1"

func (s *Server) handleCapabilities(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, s.CapabilityCatalog())
}

// CapabilityCatalog builds the catalog from the live vocabularies. Every enum
// is read from the same function its validator uses, so this document cannot
// advertise a value the server would reject.
func (s *Server) CapabilityCatalog() CapabilityCatalog {
	version := ""
	if s.systemInfo != nil {
		version = s.systemInfo.Version
	}
	enums := []CapabilityEnum{
		{
			Name:        "listener_kind",
			Description: "Proxy protocol a listener publishes. The advanced kinds speak their protocol over WebSocket behind the reverse proxy; the standard kinds are directly dialable TCP ports.",
			Values:      listener.SupportedKinds(),
		},
		{
			Name:        "listener_transport",
			Description: "How the proxy protocol is carried. \"ws\" is required for every advanced kind and rejected for the standard ones.",
			Values:      listener.SupportedTransports(),
			Default:     "tcp",
		},
		{
			Name:        "proxy_group_strategy",
			Description: "How the group selects among its members.",
			Values:      proxygroup.SupportedStrategies(),
		},
		{
			Name:        "empty_behavior",
			Description: "What a group does when its source selects no node.",
			Values:      proxygroup.SupportedEmptyBehaviors(),
			Default:     "fail-closed",
		},
		{
			Name:        "node_state",
			Description: "Probe lifecycle state a source spec may filter on.",
			Values:      proxygroup.SupportedNodeStates(),
		},
		{
			Name:        "sort_by",
			Description: "Ordering applied to the nodes a source selects.",
			Values:      proxygroup.SupportedSortOrders(),
			Default:     "latency",
		},
		{
			Name:        "subscription_source_type",
			Description: "Where the node list comes from. \"inline\" covers a pasted flat proxy list as well as a pasted subscription document: both are one document, stored encrypted and parsed by the same reader.",
			Values:      subscription.SupportedSourceTypes(),
		},
		{
			Name:        "source_class",
			Description: "What a failure costs on this source, and therefore how its nodes are retired. \"pool\": mass disposable members (a failed one is gone for good) — quarantined after 3 consecutive failures, retired after 5, and no longer probed once quarantined. \"subscription\": a long-lived paid or curated source — quarantined after 5, keeps being probed with exponential backoff, retired only after 60. \"residential\": one to a handful of irreplaceable exits — never quarantined or retired automatically, always probed so a recovery is noticed. Required on subscription.create; may be changed on its own with subscription.update.",
			Values:      subscription.SupportedSourceClasses(),
		},
		{
			Name:        "residential_provider_protocol",
			Description: "Upstream gateway protocol the data plane dials for a residential provider.",
			Values:      residential.SupportedProtocols(),
		},
		{
			Name:        "residential_worker_protocol",
			Description: "Protocol a Cloudflare Worker panel subscription may emit.",
			Values:      residential.SupportedWorkerProtocols(),
		},
		{
			Name:        "residential_rotation_mode",
			Description: "How the vendor hands out a new exit IP.",
			Values:      residential.SupportedRotationModes(),
		},
		{
			Name:        "residential_region_mode",
			Description: "How a residential allocation picks its region.",
			Values:      residential.SupportedRegionModes(),
			Default:     "fixed",
		},
		{
			Name:        "node_protocol",
			Description: "Outbound protocol names accepted from native Mihomo YAML. Availability still depends on the installed Mihomo build.",
			Values:      nodeparse.SupportedProtocols(),
		},
		{
			Name:        "routing_default",
			Description: "Unmatched traffic for the groups named by applies_to. proxy keeps that group's inbound, direct falls through to MATCH,DIRECT, and reject appends tcp and udp REJECT only when applies_to is non-empty.",
			Values:      routingrules.SupportedDefaults(),
			Default:     routingrules.DefaultProxy,
		},
	}
	return CapabilityCatalog{
		Version:            capabilityCatalogVersion,
		ApplicationVersion: version,
		Overview:           "HX-Pxy is a proxy control plane. It configures a separate Mihomo data plane and publishes node lists; it never carries proxy traffic itself. Every write below either describes desired state or reads it back.",
		Authentication: CapabilityAuth{
			Scheme:  "bearer",
			Headers: []string{"Authorization: Bearer <api-key>", "X-API-Key: <api-key>"},
			Notes: []string{
				"Create a key once in the settings UI, or over POST /api/v1/auth/api-keys while signed in as the administrator.",
				"Header credentials are exempt from CSRF checks because a browser never attaches them automatically; a session cookie is not.",
				"Authorization takes precedence over X-API-Key when both are present.",
			},
		},
		Namespaces: []CapabilityNamespace{
			{
				Prefix:  "/api/v1/",
				Auth:    "api-key or session",
				Purpose: "Administrative read and write. Everything an operator can do in the UI is reachable here.",
			},
			{
				Prefix:      "/sub/<share-token>",
				Auth:        "token in path",
				Purpose:     "Public subscription export for a proxy client.",
				ExamplePath: "/sub/<share-token>",
			},
			{
				Prefix:      listener.ConsumerNodesPath,
				Auth:        "token in path",
				Purpose:     "Public frozen JSON node listing for a program that dials nodes itself. Shares the token namespace with /sub/, so a caller that holds a subscription URL already holds this URL.",
				ExamplePath: listener.ConsumerNodesPath + "<share-token>",
			},
			{
				Prefix:      "/ctl/",
				Auth:        "token in path",
				Purpose:     "Public residential session control (claim/heartbeat/release). This token can burn provider quota and rotate exits, so a read-only consumer must not hold it.",
				ExamplePath: "/ctl/<control-token>/next",
			},
			{
				Prefix:      "/rot/",
				Auth:        "token in path",
				Purpose:     "Public residential exit rotation for a channel.",
				ExamplePath: "/rot/<rotate-token>",
			},
			{
				Prefix:      "/provision/",
				Auth:        "token in path",
				Purpose:     "Public config issuer: the plain list of enabled cf-worker provider subscription URLs. Consumers pull these and dial the workers directly.",
				ExamplePath: "/provision/<token>",
			},
		},
		Enums:        enums,
		Endpoints:    append(capabilityEndpoints(), networkCapabilityEndpoints()...),
		Workflows:    capabilityWorkflows(),
		NotSupported: capabilityNotSupported(),
		ObjectModel:  capabilityObjectModel(),
		Related:      capabilityRelated(),
	}
}

// capabilityCatalogPathSet returns every path the catalog documents. Tests use
// it to assert the catalog cannot reference a route that is not registered.
func capabilityCatalogPathSet(catalog CapabilityCatalog) map[string]struct{} {
	paths := make(map[string]struct{}, len(catalog.Endpoints))
	for _, endpoint := range catalog.Endpoints {
		path := endpoint.Path
		if index := strings.Index(path, "<"); index >= 0 {
			path = path[:index]
		}
		paths[path] = struct{}{}
	}
	return paths
}
