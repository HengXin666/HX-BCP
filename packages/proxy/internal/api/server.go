package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HengXin666/hx-pxy/internal/alert"
	"github.com/HengXin666/hx-pxy/internal/artifact"
	"github.com/HengXin666/hx-pxy/internal/auth"
	"github.com/HengXin666/hx-pxy/internal/bundle"
	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/hostnetwork"
	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/metrics"
	"github.com/HengXin666/hx-pxy/internal/node"
	"github.com/HengXin666/hx-pxy/internal/overview"
	"github.com/HengXin666/hx-pxy/internal/proxygroup"
	"github.com/HengXin666/hx-pxy/internal/proxyservice"
	"github.com/HengXin666/hx-pxy/internal/residential"
	"github.com/HengXin666/hx-pxy/internal/routingrules"
	"github.com/HengXin666/hx-pxy/internal/subscription"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

type BundleService interface {
	Create(context.Context, artifact.Kind, bundle.CreateOptions) (artifact.Record, error)
	List(artifact.Kind) ([]artifact.Record, error)
	Open(string) (artifact.Record, *os.File, error)
	Delete(string) error
	Verify(context.Context, string) (bundle.VerifyResult, error)
}

type SubscriptionService interface {
	Create(context.Context, subscription.CreateRequest) (subscription.Subscription, error)
	Get(context.Context, string) (subscription.Subscription, error)
	List(context.Context, int, int) ([]subscription.Subscription, error)
	Update(context.Context, string, subscription.UpdateRequest) (subscription.Subscription, error)
	Delete(context.Context, string, int) error
	Refresh(context.Context, string) (subscription.RefreshResult, error)
	RefreshMany(context.Context, []string) ([]subscription.BatchRefreshResult, error)
}

type NodeService interface {
	List(context.Context, node.Filter) ([]node.Node, error)
	// Count answers the size of a filtered node set so a paging client can fetch
	// the remaining pages concurrently (see handleNodes' with_total).
	Count(context.Context, node.Filter) (int, error)
	Get(context.Context, string) (node.Node, error)
	Check(context.Context, string) (node.CheckResult, error)
	CheckMany(context.Context, []string) ([]node.CheckResult, error)
	CheckManyProgress(context.Context, []string, func(node.CheckProgress) error) error
	Disable(context.Context, string) (node.Node, error)
	Enable(context.Context, string) (node.Node, error)
	QualitySettings(context.Context) (node.QualitySettings, error)
	UpdateQualitySettings(context.Context, node.QualitySettings) (node.QualitySettings, error)
}

type ProxyGroupService interface {
	Create(context.Context, proxygroup.CreateRequest) (proxygroup.Group, error)
	Get(context.Context, string) (proxygroup.Group, error)
	List(context.Context) ([]proxygroup.Group, error)
	Update(context.Context, string, proxygroup.UpdateRequest) (proxygroup.Group, error)
	Delete(context.Context, string, int) error
	// Members resolves the group's real node list. Get only returns the spec.
	Members(context.Context, string) (proxygroup.MembersView, error)
}

type ListenerService interface {
	Create(context.Context, listener.CreateRequest) (listener.Listener, error)
	Get(context.Context, string) (listener.Listener, error)
	List(context.Context) ([]listener.Listener, error)
	Update(context.Context, string, listener.UpdateRequest) (listener.Listener, error)
	Delete(context.Context, string, int) error
	ExportByShareToken(context.Context, string, string) (listener.ShareExport, error)
	RotateShareToken(context.Context, string) (listener.Listener, error)
}

type DataPlaneService interface {
	Apply(context.Context) error
	Status() mihomo.Status
	// SelectProxy points a `select` group at one of its members, so a consumer
	// can move to another node *inside* a group instead of treating the whole
	// group as spent. See handleGroupSelection.
	SelectProxy(context.Context, string, string) error
	// ReadGroup returns a group's live members and current selection, so the
	// control plane can report runtime state rather than only stored config.
	ReadGroup(context.Context, string) (mihomo.GroupSelection, error)
}

type SystemInfo struct {
	Application        string   `json:"application"`
	Version            string   `json:"version"`
	RepositoryURL      string   `json:"repository_url"`
	UpdateCommand      string   `json:"update_command"`
	AutomaticUpdate    bool     `json:"automatic_update"`
	SupportedProtocols []string `json:"supported_protocols"`
}

type UpdaterService interface {
	TriggerUpdate(context.Context) error
}

type ProxyServiceService interface {
	Create(context.Context, proxyservice.CreateRequest) (proxyservice.ServiceRecord, error)
	Update(context.Context, proxyservice.UpdateRequest) (proxyservice.ServiceRecord, error)
}

type TrafficService interface {
	Summary(context.Context, string, string) (metrics.Summary, error)
	ListSummaries(context.Context, string, int, int) ([]metrics.Summary, error)
	Query(context.Context, string, string, time.Time, time.Time, int) (metrics.Series, error)
}

type TrafficRangeService interface {
	ListSummariesBetween(context.Context, string, time.Time, time.Time, int, int) ([]metrics.Summary, error)
}

type TrafficLiveService interface {
	LiveSnapshot() metrics.LiveSnapshot
	SubscribeLive() *metrics.LiveSubscription
}

type SettingsService interface {
	Get(context.Context) (systemsettings.Settings, error)
	Update(context.Context, systemsettings.Settings) (systemsettings.Settings, error)
}

type RoutingRulesService interface {
	Get(context.Context) (routingrules.Config, error)
	Update(context.Context, routingrules.Config) (routingrules.Config, error)
	UpdateRuleSets(context.Context, []routingrules.RuleSet) (routingrules.Config, error)
	ApplyYAML(context.Context, string) (routingrules.Config, error)
}

type OverviewService interface {
	Status() mihomo.Status
	OverviewSnapshot(context.Context) (overview.Snapshot, error)
}

// ResidentialService exposes dynamic residential IP proxy management. Rotation
// by token is deliberately part of this interface because the public rotate
// route resolves the channel from the token alone.
type ResidentialService interface {
	ListProviders(context.Context) ([]residential.Provider, error)
	GetProvider(context.Context, string) (residential.Provider, error)
	CreateProvider(context.Context, residential.CreateProviderRequest) (residential.Provider, error)
	UpdateProvider(context.Context, string, residential.UpdateProviderRequest) (residential.Provider, error)
	DeleteProvider(context.Context, string, int) error
	TestProvider(context.Context, string, string) (residential.TestResult, error)

	ListChannels(context.Context) ([]residential.Channel, error)
	GetChannel(context.Context, string) (residential.Channel, error)
	CreateChannel(context.Context, residential.CreateChannelRequest) (residential.Channel, error)
	UpdateChannel(context.Context, string, residential.UpdateChannelRequest) (residential.Channel, error)
	DeleteChannel(context.Context, string, int) error

	RotateChannel(context.Context, string) (residential.RotationResult, error)
	RotateChannelByToken(context.Context, string) (residential.RotationResult, error)
	ChannelStatusByToken(context.Context, string) (residential.ChannelStatus, error)
	EnsureClientSessionByToken(context.Context, string, string) (residential.ClientSession, error)
	GetClientSessionByToken(context.Context, string, string) (residential.ClientSession, error)
	RotateClientSessionByToken(context.Context, string, string) (residential.ClientSession, error)
	SwitchClientSessionRouteByToken(context.Context, string, string, string) (residential.ClientSession, error)
	DeleteClientSessionByToken(context.Context, string, string) error
	RotateChannelToken(context.Context, string) (residential.Channel, error)
	RefreshChannelPool(context.Context, string) error
	CfWorkerSubscriptions(context.Context) ([]residential.CfSubscription, error)
}

type Option func(*Server) error

func WithSubscriptions(service SubscriptionService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("subscription service is required")
		}
		server.subscriptions = service
		return nil
	}
}

func WithNodes(service NodeService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("node service is required")
		}
		server.nodes = service
		return nil
	}
}

func WithProxyGroups(service ProxyGroupService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("proxy group service is required")
		}
		server.proxyGroups = service
		return nil
	}
}

func WithListeners(service ListenerService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("listener service is required")
		}
		server.listeners = service
		return nil
	}
}

func WithDataPlane(service DataPlaneService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("dataplane service is required")
		}
		server.dataplane = service
		return nil
	}
}

func WithSystemInfo(info SystemInfo) Option {
	return func(server *Server) error {
		if strings.TrimSpace(info.Application) == "" || strings.TrimSpace(info.Version) == "" {
			return errors.New("system application and version are required")
		}
		info.SupportedProtocols = append([]string(nil), info.SupportedProtocols...)
		server.systemInfo = &info
		return nil
	}
}

func WithUpdater(service UpdaterService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("updater service is required")
		}
		server.updater = service
		return nil
	}
}

func WithProxyServices(service ProxyServiceService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("proxy service application service is required")
		}
		server.proxyServices = service
		return nil
	}
}

// WithQuickstart enables the one-call endpoint that reaches a published proxy.
func WithQuickstart(service QuickstartService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("quick start service is required")
		}
		server.quickstart = service
		return nil
	}
}

func WithTraffic(service TrafficService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("traffic service is required")
		}
		server.traffic = service
		return nil
	}
}

func WithSettings(service SettingsService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("global settings service is required")
		}
		server.settings = service
		return nil
	}
}

func WithRoutingRules(service RoutingRulesService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("routing rules service is required")
		}
		server.routingRules = service
		return nil
	}
}

func WithOverview(service OverviewService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("overview service is required")
		}
		server.overview = service
		return nil
	}
}

func WithLogs(handler http.Handler) Option {
	return func(server *Server) error {
		if handler == nil {
			return errors.New("proxy log handler is required")
		}
		server.logs = handler
		return nil
	}
}

func WithResidential(service ResidentialService) Option {
	return func(server *Server) error {
		if service == nil {
			return errors.New("residential proxy service is required")
		}
		server.residential = service
		return nil
	}
}

type Server struct {
	bundles           BundleService
	subscriptions     SubscriptionService
	nodes             NodeService
	proxyGroups       ProxyGroupService
	listeners         ListenerService
	proxyServices     ProxyServiceService
	traffic           TrafficService
	networkOps        *hostnetwork.Service
	settings          SettingsService
	routingRules      RoutingRulesService
	overview          OverviewService
	residential       ResidentialService
	fleet             FleetService
	logs              http.Handler
	dataplane         DataPlaneService
	systemInfo        *SystemInfo
	auth              AuthService
	alerts            AlertService
	terminal          TerminalService
	terminalHeartbeat terminalHeartbeatBudget
	updater           UpdaterService
	webRoot           string
	logger            *slog.Logger
	ready             atomic.Bool
	overviewInterval  time.Duration
	edgeSlots         chan struct{}
	quickstart        QuickstartService
	dockerAccess      DockerAccessService
}

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func NewServer(bundles BundleService, logger *slog.Logger, options ...Option) (*Server, error) {
	if bundles == nil {
		return nil, errors.New("bundle service is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	server := &Server{
		bundles:          bundles,
		logger:           logger,
		overviewInterval: time.Second,
		edgeSlots:        make(chan struct{}, maxEdgeRelayConnections),
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(server); err != nil {
			return nil, err
		}
	}
	server.ready.Store(true)
	return server, nil
}

func WithWebRoot(root string) Option {
	return func(server *Server) error {
		root = strings.TrimSpace(root)
		if root == "" {
			return nil
		}
		info, err := os.Stat(path.Join(root, "index.html"))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("web root %q does not contain index.html", root)
		}
		server.webRoot = root
		return nil
	}
}

func (s *Server) Handler() http.Handler {
	mux := s.routes()
	var handler http.Handler = mux
	if s.auth != nil {
		s.registerAuthRoutes(mux)
		handler = s.requireAuth(handler)
	}
	return s.requestContext(s.securityHeaders(gzipMiddleware(handler)))
}

// routes registers every route this server serves. It is split out of Handler
// so a test can ask the real mux which pattern a documented path resolves to;
// the catalog's route table is checked against this mux rather than against a
// second hand-maintained list.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", s.handleLive)
	mux.HandleFunc("/health/ready", s.handleReady)
	mux.HandleFunc("/api/v1/backups", func(writer http.ResponseWriter, request *http.Request) {
		s.handleCollection(writer, request, artifact.KindBackup)
	})
	mux.HandleFunc("/api/v1/backups/", func(writer http.ResponseWriter, request *http.Request) {
		s.handleItem(writer, request, artifact.KindBackup, "/api/v1/backups/")
	})
	mux.HandleFunc("/api/v1/exports", func(writer http.ResponseWriter, request *http.Request) {
		s.handleCollection(writer, request, artifact.KindExport)
	})
	mux.HandleFunc("/api/v1/exports/", func(writer http.ResponseWriter, request *http.Request) {
		s.handleItem(writer, request, artifact.KindExport, "/api/v1/exports/")
	})
	if s.subscriptions != nil {
		mux.HandleFunc("/api/v1/subscriptions", s.handleSubscriptions)
		mux.HandleFunc("/api/v1/subscriptions/", s.handleSubscription)
	}
	if s.nodes != nil {
		mux.HandleFunc("/api/v1/nodes", s.handleNodes)
		mux.HandleFunc("/api/v1/nodes/", s.handleNode)
		mux.HandleFunc("/api/v1/node-settings", s.handleNodeSettings)
	}
	if s.proxyGroups != nil {
		mux.HandleFunc("/api/v1/proxy-groups", s.handleProxyGroups)
		mux.HandleFunc("/api/v1/proxy-groups/", s.handleProxyGroup)
	}
	if s.listeners != nil {
		mux.HandleFunc("/api/v1/listeners", s.handleListeners)
		mux.HandleFunc("/api/v1/listeners/", s.handleListener)
		if s.dockerAccess != nil {
			mux.HandleFunc(DockerAccessPath, s.handleDockerAccessLinks)
		}
		// Public WebSocket proxy routes use a reserved namespace and are
		// resolved to loopback Mihomo listeners by the edge relay.
		mux.HandleFunc(listener.WebSocketPathPrefix, s.handleEdgeRelay)
	}
	if s.listeners != nil || s.residential != nil {
		// Public token-addressed subscription export; the token itself is
		// the credential, so the route stays outside /api/v1 auth.
		mux.HandleFunc("/sub/", s.handleListenerShare)
		// Programmatic node listing: the same share token as /sub/, rendered as
		// stable JSON for consumers that dial nodes themselves instead of
		// importing a subscription. It shares the token namespace with /sub/,
		// never /api/v1/. See docs/CONSUMER_INTEGRATION_CONTRACT.md.
		mux.HandleFunc(listener.ConsumerNodesPath, s.handleConsumerNodes)
	}
	if s.proxyServices != nil {
		mux.HandleFunc("/api/v1/proxy-services", s.handleProxyServices)
		mux.HandleFunc("/api/v1/proxy-services/", s.handleProxyService)
	}
	// Machine-readable capability catalog: every management endpoint with its
	// field names, closed enums and a copy-pasteable example, built from the same
	// vocabularies the validators use. It saves an integrating agent from reading
	// Go source to discover a field or an enum value. Admin-only, because it
	// describes administrative write surfaces.
	mux.HandleFunc(CapabilityCatalogPath, s.handleCapabilities)
	if s.quickstart != nil {
		// One-call shortcut to a published proxy: register and refresh the
		// source, then create the group and its listener. Admin-only, like the
		// resource endpoints it replaces.
		mux.HandleFunc(QuickstartPath, s.handleQuickstart)
	}
	if s.fleet != nil {
		mux.HandleFunc("/api/v1/fleet/status", s.handleFleetStatus)
		mux.HandleFunc("/api/v1/fleet/sweep", s.handleFleetSweep)
		mux.HandleFunc("/api/v1/fleet/accounts", s.handleFleetAccounts)
		mux.HandleFunc("/api/v1/fleet/accounts/", s.handleFleetAccount)
	}
	if s.residential != nil {
		mux.HandleFunc("/api/v1/residential/presets", s.handleResidentialPresets)
		mux.HandleFunc("/api/v1/residential/providers", s.handleResidentialProviders)
		mux.HandleFunc("/api/v1/residential/providers/", s.handleResidentialProvider)
		mux.HandleFunc("/api/v1/residential/channels", s.handleResidentialChannels)
		mux.HandleFunc("/api/v1/residential/channels/", s.handleResidentialChannel)
		// Public token-addressed rotation for consumers. The token is the
		// credential, so these routes stay outside /api/v1 session auth in the
		// same way as the /sub/ subscription export.
		mux.HandleFunc("/rot/", s.handleResidentialRotatePublic)
		mux.HandleFunc("/ctl/", s.handleResidentialControlPublic)
		// Public token-addressed config center: GET /provision/<token> returns
		// the plain list of enabled cf-worker provider subscription URLs.
		// Consumers pull these and dial the workers directly — this control
		// plane is a config issuer, not a relay. 20260821.
		mux.HandleFunc("/provision/", s.handleProvisionConfig)
	}
	if s.traffic != nil {
		mux.HandleFunc("/api/v1/traffic", s.handleTraffic)
	}
	if s.settings != nil {
		mux.HandleFunc("/api/v1/settings", s.handleSettings)
	}
	if s.routingRules != nil {
		mux.HandleFunc("/api/v1/routing-rules", s.handleRoutingRules)
		mux.HandleFunc("/api/v1/routing-rules/apply", s.handleRoutingRulesApply)
		mux.HandleFunc("/api/v1/mcp", s.handleRoutingMCP)
	}
	if s.overview != nil {
		mux.HandleFunc("/api/v1/overview/stream", s.handleOverviewStream)
	}
	if s.logs != nil {
		mux.Handle("/api/v1/logs/stream", s.logs)
	}
	if s.dataplane != nil {
		mux.HandleFunc("/api/v1/dataplane/status", s.handleDataPlaneStatus)
		mux.HandleFunc("/api/v1/dataplane/apply", s.handleDataPlaneApply)
	}
	if s.systemInfo != nil {
		mux.HandleFunc("/api/v1/system/info", s.handleSystemInfo)
	}
	// Interface addresses for "use this from a container": the docker bridge
	// gateway and the LAN IPs. Facts about the host routing only.
	mux.HandleFunc("/api/v1/system/network", s.handleSystemNetwork)
	mux.HandleFunc("/api/v1/connections", s.handleConnections)
	mux.HandleFunc("/api/v1/system/network/inspect", s.handleNetworkInspect)
	mux.HandleFunc("/api/v1/system/network/operations", s.handleNetworkOperation)
	if s.updater != nil {
		mux.HandleFunc("/api/v1/system/update", s.handleSystemUpdate)
	}
	if s.alerts != nil {
		mux.HandleFunc("/api/v1/alerts", s.handleAlerts)
		mux.HandleFunc("/api/v1/alerts/", s.handleAlertItem)
	}
	if s.terminal != nil {
		mux.HandleFunc("/api/v1/terminal/status", s.handleTerminalStatus)
		mux.HandleFunc("/api/v1/terminal/exec", s.handleTerminalExec)
		mux.HandleFunc("/api/v1/terminal/ws", s.handleTerminalSocket)
		// Sensitive file/metrics surfaces carry the same authority as a root
		// shell, so they are gated by the same 2FA step-up as the WebSocket.
		mux.Handle("/api/v1/terminal/metrics", s.requireTerminalTwoFactor(http.HandlerFunc(s.handleTerminalMetrics)))
		mux.Handle("/api/v1/terminal/files", s.requireTerminalTwoFactor(http.HandlerFunc(s.handleTerminalFiles)))
		mux.Handle("/api/v1/terminal/files/mkdir", s.requireTerminalTwoFactor(http.HandlerFunc(s.handleTerminalFileMkdir)))
		mux.Handle("/api/v1/terminal/files/remove", s.requireTerminalTwoFactor(http.HandlerFunc(s.handleTerminalFileRemove)))
		// Host resource snapshot for the overview dashboard (admin-only, no 2FA
		// required because it exposes utilization numbers, not shell authority).
		mux.HandleFunc("/api/v1/system/resources", s.handleSystemResources)
		// Read-only host ops surfaces (disk + docker) for the terminal page's
		// ops sub-tabs; same auth posture as system/resources — numbers and
		// listings only, no shell authority, no 2FA step-up.
		mux.HandleFunc("/api/v1/system/disk", s.handleSystemDisk)
		mux.HandleFunc("/api/v1/docker/containers", s.handleDockerContainers)
	}
	if s.webRoot != "" {
		mux.Handle("/", newSPAHandler(s.webRoot))
	}
	return mux
}

func (s *Server) handleSystemInfo(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	response := struct {
		SystemInfo
		DataPlaneVersion string `json:"dataplane_version,omitempty"`
	}{SystemInfo: *s.systemInfo}
	if s.dataplane != nil {
		response.DataPlaneVersion = s.dataplane.Status().Version
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

func (s *Server) handleLive(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "live"})
}

func (s *Server) handleReady(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	if !s.ready.Load() {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleCollection(writer http.ResponseWriter, request *http.Request, kind artifact.Kind) {
	switch request.Method {
	case http.MethodGet:
		records, err := s.bundles.List(kind)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"items": records})
	case http.MethodPost:
		var options bundle.CreateOptions
		if err := decodeJSONBody(writer, request, &options); err != nil {
			s.writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		record, err := s.bundles.Create(request.Context(), kind, options)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.Header().Set("Location", request.URL.Path+"/"+record.ID)
		writeJSON(writer, http.StatusCreated, record)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleItem(writer http.ResponseWriter, request *http.Request, kind artifact.Kind, prefix string) {
	remainder := strings.TrimPrefix(request.URL.Path, prefix)
	if remainder == "" || remainder == request.URL.Path {
		http.NotFound(writer, request)
		return
	}
	parts := strings.Split(remainder, "/")
	if len(parts) > 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch action {
	case "":
		s.handleMetadata(writer, request, kind, id)
	case "download":
		s.handleDownload(writer, request, kind, id)
	case "verify":
		s.handleVerify(writer, request, kind, id)
	default:
		http.NotFound(writer, request)
	}
}

func (s *Server) handleMetadata(writer http.ResponseWriter, request *http.Request, kind artifact.Kind, id string) {
	switch request.Method {
	case http.MethodGet:
		record, file, err := s.bundles.Open(id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		_ = file.Close()
		if record.Kind != kind {
			s.handleError(writer, request, artifact.ErrNotFound)
			return
		}
		writeJSON(writer, http.StatusOK, record)
	case http.MethodDelete:
		record, file, err := s.bundles.Open(id)
		if err != nil {
			s.handleError(writer, request, err)
			return
		}
		_ = file.Close()
		if record.Kind != kind {
			s.handleError(writer, request, artifact.ErrNotFound)
			return
		}
		if err := s.bundles.Delete(id); err != nil {
			s.handleError(writer, request, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, request, http.MethodGet, http.MethodDelete)
	}
}

func (s *Server) handleDownload(writer http.ResponseWriter, request *http.Request, kind artifact.Kind, id string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, request, http.MethodGet)
		return
	}
	record, file, err := s.bundles.Open(id)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	defer file.Close()
	if record.Kind != kind {
		s.handleError(writer, request, artifact.ErrNotFound)
		return
	}
	writer.Header().Set("Content-Type", record.ContentType)
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, path.Base(record.Filename)))
	writer.Header().Set("Content-Length", fmt.Sprintf("%d", record.Size))
	writer.Header().Set("ETag", `"sha256:`+record.SHA256+`"`)
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	if _, err := io.Copy(writer, file); err != nil {
		s.logger.WarnContext(request.Context(), "artifact download interrupted", "artifact_id", id, "error", err)
	}
}

func (s *Server) handleVerify(writer http.ResponseWriter, request *http.Request, kind artifact.Kind, id string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, request, http.MethodPost)
		return
	}
	record, file, err := s.bundles.Open(id)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	_ = file.Close()
	if record.Kind != kind {
		s.handleError(writer, request, artifact.ErrNotFound)
		return
	}
	result, err := s.bundles.Verify(request.Context(), id)
	if err != nil {
		s.handleError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) handleError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrSessionExpired):
		s.writeAPIError(writer, request, http.StatusUnauthorized, "unauthenticated", "authentication required")
	case errors.Is(err, auth.ErrInvalidCredentials):
		s.writeAPIError(writer, request, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
	case errors.Is(err, auth.ErrLockedOut):
		s.writeAPIError(writer, request, http.StatusTooManyRequests, "login_locked_out", "too many failed logins, retry later")
	case errors.Is(err, auth.ErrInvalidSetupToken):
		s.writeAPIError(writer, request, http.StatusForbidden, "invalid_setup_token", "setup token is missing or wrong")
	case errors.Is(err, auth.ErrTwoFactorUnavailable):
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "two_factor_unavailable", "two-factor authentication is unavailable")
	case errors.Is(err, auth.ErrTwoFactorNotConfigured):
		s.writeAPIError(writer, request, http.StatusConflict, "two_factor_not_configured", "configure two-factor authentication first")
	case errors.Is(err, auth.ErrTwoFactorAlreadyEnabled):
		s.writeAPIError(writer, request, http.StatusConflict, "two_factor_already_enabled", "two-factor authentication is already enabled")
	case errors.Is(err, auth.ErrTwoFactorNotEnabled):
		s.writeAPIError(writer, request, http.StatusConflict, "two_factor_not_enabled", "two-factor authentication is not enabled")
	case errors.Is(err, auth.ErrInvalidTwoFactorCode):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "invalid_two_factor_code", "the two-factor authentication code is invalid")
	case errors.Is(err, auth.ErrTwoFactorLockedOut):
		s.writeAPIError(writer, request, http.StatusTooManyRequests, "two_factor_locked_out", "too many two-factor authentication attempts, retry later")
	case errors.Is(err, auth.ErrAlreadyConfigured):
		s.writeAPIError(writer, request, http.StatusConflict, "already_configured", "administrator account already exists")
	case errors.Is(err, auth.ErrNotConfigured):
		s.writeAPIError(writer, request, http.StatusConflict, "admin_not_configured", "administrator account is not configured yet")
	case errors.Is(err, auth.ErrWeakPassword):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "weak_password", err.Error())
	case errors.Is(err, auth.ErrInvalidUsername):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "invalid_username", err.Error())
	case errors.Is(err, auth.ErrInvalidAPIKeyName):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "invalid_api_key_name", err.Error())
	case errors.Is(err, alert.ErrNotFound):
		s.writeAPIError(writer, request, http.StatusNotFound, "not_found", "alert not found")
	case errors.Is(err, alert.ErrInvalidSetting):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	case errors.Is(err, alert.ErrNoChannel):
		s.writeAPIError(writer, request, http.StatusConflict, "alert_channel_not_configured", "configure SMTP settings first")
	case errors.Is(err, artifact.ErrNotFound):
		s.writeAPIError(writer, request, http.StatusNotFound, "not_found", "artifact not found")
	case errors.Is(err, bundle.ErrSecretBundleDisabled):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "secret_export_disabled", err.Error())
	case errors.Is(err, node.ErrInvalidSettings):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	case errors.Is(err, listener.ErrUsernameTaken):
		s.writeAPIError(writer, request, http.StatusConflict, "shared_inbound_username_taken", err.Error())
	case errors.Is(err, proxygroup.ErrInvalid), errors.Is(err, listener.ErrInvalid), errors.Is(err, routingrules.ErrInvalid):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	case errors.Is(err, residential.ErrProviderUnreachable):
		s.writeAPIError(writer, request, http.StatusBadGateway, "provider_unreachable", err.Error())
	case errors.Is(err, residential.ErrRateLimited):
		s.writeAPIError(writer, request, http.StatusTooManyRequests, "rotate_rate_limited", err.Error())
	case errors.Is(err, residential.ErrLeaseHeld):
		s.writeAPIError(writer, request, http.StatusConflict, "lease_held", err.Error())
	case errors.Is(err, residential.ErrLeaseExpired):
		s.writeAPIError(writer, request, http.StatusConflict, "lease_expired", err.Error())
	case errors.Is(err, residential.ErrAllocVersionChanged):
		s.writeAPIError(writer, request, http.StatusConflict, "alloc_version_changed", err.Error())
	case errors.Is(err, residential.ErrInvalid):
		s.writeAPIError(writer, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	case errors.Is(err, residential.ErrNotFound):
		s.writeAPIError(writer, request, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, residential.ErrConflict):
		s.writeAPIError(writer, request, http.StatusConflict, "conflict", "resource changed or conflicts with existing configuration")
	case errors.Is(err, listener.ErrShareDisabled):
		s.writeAPIError(writer, request, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, proxygroup.ErrNotFound), errors.Is(err, listener.ErrNotFound), errors.Is(err, node.ErrNotFound):
		s.writeAPIError(writer, request, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, node.ErrCheckUnavailable), errors.Is(err, mihomo.ErrNotRunning):
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "node_check_unavailable", "node quality check is unavailable")
	case errors.Is(err, proxygroup.ErrApplyFailed), errors.Is(err, listener.ErrApplyFailed):
		s.writeAPIError(writer, request, http.StatusBadGateway, "dataplane_apply_failed", err.Error())
	case errors.Is(err, proxygroup.ErrReferenced):
		// Distinct from ErrConflict so the caller can act: this refusal is
		// resolved by pointing the listeners elsewhere (or pinning them to a
		// node), not by editing the group. Sent before the ErrConflict case
		// because errors.Is would also match a wrapped ErrConflict.
		s.writeAPIError(writer, request, http.StatusConflict, "proxy_group_in_use", err.Error())
	case errors.Is(err, proxygroup.ErrConflict), errors.Is(err, listener.ErrConflict):
		s.writeAPIError(writer, request, http.StatusConflict, "conflict", "resource changed or conflicts with existing configuration")
	case errors.Is(err, mihomo.ErrUnavailable):
		s.writeAPIError(writer, request, http.StatusServiceUnavailable, "dataplane_unavailable", "mihomo is not installed or configured")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		s.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "request was canceled or timed out")
	default:
		s.logger.ErrorContext(request.Context(), "API operation failed", "request_id", requestID(request), "error", err)
		s.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "operation failed")
	}
}

func (s *Server) writeAPIError(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	writeJSON(writer, status, errorResponse{Error: apiError{
		Code:      code,
		Message:   message,
		RequestID: requestID(request),
	}})
}

func (s *Server) requestContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := newRequestID()
		writer.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDKey{}, requestID)
		startedAt := time.Now()
		next.ServeHTTP(writer, request.WithContext(ctx))
		loggedPath := request.URL.Path
		if strings.HasPrefix(loggedPath, "/sub/") || strings.HasPrefix(loggedPath, "/rot/") ||
			strings.HasPrefix(loggedPath, "/ctl/") || strings.HasPrefix(loggedPath, listener.ConsumerNodesPath) {
			// Every token-addressed route carries a credential in the path; the
			// namespace is logged and the secret never is.
			loggedPath = "/" + strings.SplitN(strings.TrimPrefix(loggedPath, "/"), "/", 2)[0] + "/[redacted]"
		}
		s.logger.InfoContext(ctx, "HTTP request", "method", request.Method, "path", loggedPath, "duration_ms", time.Since(startedAt).Milliseconds(), "request_id", requestID)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}

func decodeJSONBody(writer http.ResponseWriter, request *http.Request, destination any) error {
	if request.Body == nil {
		return nil
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func methodNotAllowed(writer http.ResponseWriter, request *http.Request, methods ...string) {
	writer.Header().Set("Allow", strings.Join(methods, ", "))
	writeJSON(writer, http.StatusMethodNotAllowed, errorResponse{Error: apiError{
		Code:      "method_not_allowed",
		Message:   "method not allowed",
		RequestID: requestID(request),
	}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

type requestIDKey struct{}

func requestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDKey{}).(string)
	return value
}

func newRequestID() string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data[:])
}
