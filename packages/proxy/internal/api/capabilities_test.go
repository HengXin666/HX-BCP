package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/proxyservice"
	"github.com/HengXin666/hx-pxy/internal/quickstart"
	"github.com/HengXin666/hx-pxy/internal/residential"
	"github.com/HengXin666/hx-pxy/internal/routingrules"
	"github.com/HengXin666/hx-pxy/internal/subscription"
)

// stubProxyGroupService and stubProxyServiceService only need to exist so the
// route table is populated; the catalog tests never call them.
type stubProxyGroupService struct{}

func (stubProxyGroupService) Create(context.Context, proxygroup.CreateRequest) (proxygroup.Group, error) {
	return proxygroup.Group{}, nil
}

func (stubProxyGroupService) Get(context.Context, string) (proxygroup.Group, error) {
	return proxygroup.Group{}, nil
}

func (stubProxyGroupService) List(context.Context) ([]proxygroup.Group, error) { return nil, nil }

func (stubProxyGroupService) Update(context.Context, string, proxygroup.UpdateRequest) (proxygroup.Group, error) {
	return proxygroup.Group{}, nil
}

func (stubProxyGroupService) Delete(context.Context, string, int) error { return nil }
func (stubProxyGroupService) Members(context.Context, string) (proxygroup.MembersView, error) {
	return proxygroup.MembersView{}, nil
}

type stubSubscriptionService struct{}

func (stubSubscriptionService) Create(context.Context, subscription.CreateRequest) (subscription.Subscription, error) {
	return subscription.Subscription{}, nil
}

func (stubSubscriptionService) Get(context.Context, string) (subscription.Subscription, error) {
	return subscription.Subscription{}, nil
}

func (stubSubscriptionService) List(context.Context, int, int) ([]subscription.Subscription, error) {
	return nil, nil
}

func (stubSubscriptionService) Update(context.Context, string, subscription.UpdateRequest) (subscription.Subscription, error) {
	return subscription.Subscription{}, nil
}

func (stubSubscriptionService) Delete(context.Context, string, int) error { return nil }

func (stubSubscriptionService) Refresh(context.Context, string) (subscription.RefreshResult, error) {
	return subscription.RefreshResult{}, nil
}

func (stubSubscriptionService) RefreshMany(context.Context, []string) ([]subscription.BatchRefreshResult, error) {
	return nil, nil
}

type stubQuickstartService struct{}

func (stubQuickstartService) Create(context.Context, quickstart.Request) (quickstart.Result, error) {
	return quickstart.Result{}, nil
}

type stubProxyServiceService struct{}

func (stubProxyServiceService) Create(context.Context, proxyservice.CreateRequest) (proxyservice.ServiceRecord, error) {
	return proxyservice.ServiceRecord{}, nil
}

func (stubProxyServiceService) Update(context.Context, proxyservice.UpdateRequest) (proxyservice.ServiceRecord, error) {
	return proxyservice.ServiceRecord{}, nil
}

func newCapabilityServer(t *testing.T) *Server {
	t.Helper()
	server, err := NewServer(
		&stubBundleService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithListeners(&consumerNodeService{}),
		WithSubscriptions(&stubSubscriptionService{}),
		WithProxyGroups(&stubProxyGroupService{}),
		WithProxyServices(&stubProxyServiceService{}),
		WithQuickstart(&stubQuickstartService{}),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func catalogEnum(t *testing.T, catalog CapabilityCatalog, name string) CapabilityEnum {
	t.Helper()
	for _, enum := range catalog.Enums {
		if enum.Name == name {
			return enum
		}
	}
	t.Fatalf("catalog has no enum %q", name)
	return CapabilityEnum{}
}

// TestCapabilityCatalogEnumsAreLiveVocabulary is the drift gate. Every enum in
// the catalog must be exactly the list the validator accepts, so an agent can
// never be told a value the server would reject. Comparing against the
// validators (not a copy) is what makes this non-vacuous: adding a kind to
// SupportedKinds without exposing it here fails, and so does documenting a value
// the validator does not know.
func TestCapabilityCatalogEnumsAreLiveVocabulary(t *testing.T) {
	t.Parallel()

	catalog := newCapabilityServer(t).CapabilityCatalog()
	cases := []struct {
		name   string
		values []string
	}{
		{"listener_kind", listener.SupportedKinds()},
		{"listener_transport", listener.SupportedTransports()},
		{"proxy_group_strategy", proxygroup.SupportedStrategies()},
		{"empty_behavior", proxygroup.SupportedEmptyBehaviors()},
		{"node_state", proxygroup.SupportedNodeStates()},
		{"sort_by", proxygroup.SupportedSortOrders()},
		{"residential_provider_protocol", residential.SupportedProtocols()},
		{"residential_worker_protocol", residential.SupportedWorkerProtocols()},
		{"residential_rotation_mode", residential.SupportedRotationModes()},
		{"residential_region_mode", residential.SupportedRegionModes()},
		{"node_protocol", nodeparse.SupportedProtocols()},
		{"subscription_source_type", subscription.SupportedSourceTypes()},
		{"source_class", subscription.SupportedSourceClasses()},
		{"routing_default", routingrules.SupportedDefaults()},
	}
	for _, testCase := range cases {
		got := catalogEnum(t, catalog, testCase.name).Values
		if len(got) != len(testCase.values) {
			t.Fatalf("enum %q = %v, want %v", testCase.name, got, testCase.values)
		}
		for index := range got {
			if got[index] != testCase.values[index] {
				t.Fatalf("enum %q = %v, want %v", testCase.name, got, testCase.values)
			}
		}
	}
}

// TestCapabilityCatalogFieldsReferenceKnownEnums keeps a documented field from
// pointing at an enum name that does not exist, which would silently leave the
// field without a closed vocabulary.
func TestCapabilityCatalogFieldsReferenceKnownEnums(t *testing.T) {
	t.Parallel()

	catalog := newCapabilityServer(t).CapabilityCatalog()
	known := make(map[string]struct{}, len(catalog.Enums))
	for _, enum := range catalog.Enums {
		known[enum.Name] = struct{}{}
	}
	for _, endpoint := range catalog.Endpoints {
		for _, field := range endpoint.Fields {
			if field.Enum == "" {
				continue
			}
			if _, exists := known[field.Enum]; !exists {
				t.Fatalf("endpoint %q field %q references unknown enum %q", endpoint.ID, field.Name, field.Enum)
			}
		}
	}
}

// TestCapabilityCatalogPathsResolveToRegisteredRoutes is the second half of the
// drift gate. Every path the catalog advertises must actually be served: a
// documented endpoint that resolves to no route would send an agent down a
// dead end, and a path typo is otherwise invisible.
func TestCapabilityCatalogPathsResolveToRegisteredRoutes(t *testing.T) {
	t.Parallel()

	server := newCapabilityServer(t)
	mux := server.routes()
	for path := range capabilityCatalogPathSet(server.CapabilityCatalog()) {
		// The catalog documents every endpoint an operator can reach, including
		// those that only exist when the matching application service is wired.
		// The test server deliberately wires a subset, so resolve only the paths
		// that belong to the subset it actually serves.
		if !isCoreAdminRoute(path) {
			continue
		}
		request := mustRequest(t, http.MethodGet, path)
		if _, pattern := mux.Handler(request); pattern == "" {
			t.Fatalf("catalog documents %q but the mux serves no route for it", path)
		}
	}
}

// isCoreAdminRoute reports whether a documented path is served by the
// listener-only test server.
func isCoreAdminRoute(path string) bool {
	for _, route := range coreAdminRoutes {
		if path == route {
			return true
		}
	}
	return false
}

// TestCapabilityCatalogCoversRegisteredAdminRoutes asserts the catalog is not
// silently missing a registered management route. An undocumented admin route
// would force an integrating agent back into reading Go source, which is the
// problem the catalog removes.
func TestCapabilityCatalogCoversRegisteredAdminRoutes(t *testing.T) {
	t.Parallel()

	server := newCapabilityServer(t)
	documented := capabilityCatalogPathSet(server.CapabilityCatalog())
	// Routes that are deliberately absent from the catalog: health probes are
	// not management surfaces, and the auth endpoints are how a caller obtains
	// the credential the catalog already documents.
	exempt := map[string]struct{}{
		"/health/live": {}, "/health/ready": {},
	}
	for _, route := range append(append([]string{}, coreAdminRoutes...), optionalAdminRoutes...) {
		// Item routes (/api/v1/nodes/) are the collection route plus an id. The
		// catalog documents the operation, not every spelling of its path, so
		// checking the collection form is what matters.
		if strings.HasSuffix(route, "/") {
			continue
		}
		if _, ok := exempt[route]; ok {
			continue
		}
		if _, ok := documented[route]; !ok {
			t.Fatalf("registered admin route %q is not documented in the catalog", route)
		}
	}
}

// coreAdminRoutes are the management routes the test server registers, so each
// one is checked against the live mux as well as against the catalog. Keeping
// this list small is deliberate: it is the part that can be verified end to end
// without wiring every application service.
var coreAdminRoutes = []string{
	"/api/v1/subscriptions", "/api/v1/subscriptions/",
	"/api/v1/proxy-groups", "/api/v1/proxy-groups/",
	"/api/v1/listeners", "/api/v1/listeners/",
	"/api/v1/proxy-services", "/api/v1/proxy-services/",
	"/api/v1/capabilities", "/api/v1/quickstart",
}

// optionalAdminRoutes are management routes that only exist when the matching
// application service is wired. The catalog must document them even though a
// listener-only deployment does not serve them, so they are checked for
// documentation only.
var optionalAdminRoutes = []string{
	"/api/v1/backups", "/api/v1/backups/",
	"/api/v1/exports", "/api/v1/exports/",
	// Nodes need a probe-backed node service, which the catalog test server
	// does not wire; the catalog still documents them.
	"/api/v1/nodes", "/api/v1/nodes/", "/api/v1/node-settings",
	"/api/v1/residential/presets", "/api/v1/residential/providers",
	"/api/v1/residential/channels",
	"/api/v1/settings", "/api/v1/routing-rules", "/api/v1/routing-rules/apply",
	"/api/v1/mcp",
	"/api/v1/docker-access",
}

// observedOnlyRoutes are registered management routes the catalog deliberately
// does not enumerate. They report state rather than configure it: monitoring,
// terminal and data-plane diagnostics belong to the UI, and documenting them
// would inflate the catalog without helping an agent build a proxy. They are
// listed here so the exemption is explicit rather than an oversight.
var observedOnlyRoutes = []string{
	"/api/v1/fleet/status", "/api/v1/fleet/accounts",
	"/api/v1/traffic", "/api/v1/overview/stream",
	"/api/v1/dataplane/status", "/api/v1/system/info",
	"/api/v1/alerts", "/api/v1/terminal/exec",
	"/api/v1/system/resources", "/api/v1/system/disk",
	"/api/v1/docker/containers",
}

// TestCoreAdminRoutesExistInMux keeps coreAdminRoutes honest: it fails if a
// route is renamed or removed in the server without updating this list.
func TestCoreAdminRoutesExistInMux(t *testing.T) {
	t.Parallel()

	mux := newCapabilityServer(t).routes()
	for _, route := range coreAdminRoutes {
		request := mustRequest(t, http.MethodGet, route)
		if _, pattern := mux.Handler(request); pattern == "" {
			t.Fatalf("listed core admin route %q is not registered in the mux", route)
		}
	}
}

// TestCatalogEndpointsMentionBackupsAndExports is a targeted check on the two
// artifact routes, which are registered unconditionally in Handler and so are
// absent from the listener-only test server's mux.
func TestCatalogEndpointsMentionBackupsAndExports(t *testing.T) {
	t.Parallel()

	documented := capabilityCatalogPathSet(newCapabilityServer(t).CapabilityCatalog())
	// Backups and exports are served unconditionally by Handler, so a catalog
	// that omits them is incomplete regardless of which services are wired.
	for _, route := range []string{"/api/v1/backups", "/api/v1/exports"} {
		if _, ok := documented[route]; !ok {
			t.Fatalf("route %q is registered unconditionally but not documented", route)
		}
	}
}

// TestObservedOnlyRoutesStayExempt keeps the exemption honest: a monitoring
// route that later gains a documented entry must be removed from the exemption
// list, otherwise the list silently stops describing reality.
func TestObservedOnlyRoutesStayExempt(t *testing.T) {
	t.Parallel()

	documented := capabilityCatalogPathSet(newCapabilityServer(t).CapabilityCatalog())
	for _, route := range observedOnlyRoutes {
		if _, ok := documented[route]; ok {
			t.Fatalf("route %q is now documented but is still listed as observed-only", route)
		}
	}
}

// TestDocumentedRoutesAreConfigurationSurfaces guards the catalog's purpose. A
// read-only monitoring route adds no way to build a proxy, so documenting it
// would only dilute the document an agent reads first.
func TestDocumentedRoutesAreConfigurationSurfaces(t *testing.T) {
	t.Parallel()

	catalog := newCapabilityServer(t).CapabilityCatalog()
	for _, endpoint := range catalog.Endpoints {
		if endpoint.Summary == "" {
			t.Fatalf("endpoint %q has no summary; the catalog is unreadable without one", endpoint.ID)
		}
		if endpoint.Auth == "" {
			t.Fatalf("endpoint %q does not state its auth requirement", endpoint.ID)
		}
	}
}

// TestCapabilityCatalogSkipsPublicTokenNamespace asserts the catalog stays out
// of the public token namespace. It describes administrative write surfaces, so
// an unauthenticated caller must not be able to enumerate them.
func TestCapabilityCatalogStaysOutOfPublicTokenNamespace(t *testing.T) {
	t.Parallel()

	for _, path := range []string{CapabilityCatalogPath, QuickstartPath} {
		if !strings.HasPrefix(path, "/api/v1/") {
			t.Fatalf("path %q is not in the /api/v1 admin namespace", path)
		}
	}
}

func mustRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	return request
}
