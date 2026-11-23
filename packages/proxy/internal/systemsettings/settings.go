package systemsettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
)

const (
	MetadataKey      = "global_settings"
	legacyQualityKey = "node_quality_settings"
	legacyTestURL    = "https://www.gstatic.com/generate_204"
	defaultTestURL   = "http://cp.cloudflare.com/generate_204"

	// DefaultLogRetentionDays is how long the product keeps its own log files.
	// The retention pass reads this through Settings, so a settings change takes
	// effect on the next pass instead of requiring a restart.
	DefaultLogRetentionDays = 30
	// logRetentionMaxDays mirrors logretention.MaxDays: ten years is already
	// past any forensic value, and the bound keeps a typo from disabling the
	// cleanup forever.
	logRetentionMaxDays = 3650
)

var ErrInvalid = errors.New("invalid global settings")

type Reader interface {
	GetMetadata(context.Context, string) (string, error)
}

type Repository interface {
	Reader
	SetMetadata(context.Context, string, string) error
}

type Applier interface {
	Apply(context.Context) error
}

type HealthTarget struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

type QualitySettings struct {
	CheckIntervalSeconds int            `json:"check_interval_seconds"`
	TimeoutSeconds       int            `json:"timeout_seconds"`
	BatchSize            int            `json:"batch_size"`
	ProbeConcurrency     int            `json:"probe_concurrency"`
	TestURL              string         `json:"test_url"`
	HealthTargets        []HealthTarget `json:"health_targets"`
}

type DNSSettings struct {
	Enabled           bool     `json:"enabled"`
	IPv6              bool     `json:"ipv6"`
	EnhancedMode      string   `json:"enhanced_mode"`
	DefaultNameserver []string `json:"default_nameserver"`
	Nameserver        []string `json:"nameserver"`
	Fallback          []string `json:"fallback"`
}

type PerformanceSettings struct {
	TCPConcurrent     bool   `json:"tcp_concurrent"`
	UnifiedDelay      bool   `json:"unified_delay"`
	KeepAliveIdle     int    `json:"keep_alive_idle_seconds"`
	KeepAliveInterval int    `json:"keep_alive_interval_seconds"`
	FindProcessMode   string `json:"find_process_mode"`
	LogLevel          string `json:"log_level"`
}

type Settings struct {
	TUN         *TUNSettings        `json:"tun,omitempty"`
	Quality     QualitySettings     `json:"quality"`
	DNS         DNSSettings         `json:"dns"`
	Performance PerformanceSettings `json:"performance"`
	// Provision is the config-center ("授权/配置中心") gate. When Enabled and
	// Token are set, GET /provision/<token> answers with the plain list of
	// every enabled cf-worker provider's subscription URL — consumers (e.g.
	// the OutlookRegister cf_bpb preset) pull these and dial the CF workers
	// directly, without proxying traffic through this control plane.
	// 20260821 user decision: pxy is a config center, not a relay.
	Provision ProvisionSettings `json:"provision"`
	// Fleet is the built-in CF worker fleet maintainer (20260823): given CF
	// accounts (API tokens) it deploys/probes/recreates worker endpoints and
	// switches accounts when one is banned.
	Fleet FleetSettings `json:"fleet"`
	// SharedInbound collapses N proxy services onto one Mixed port and one
	// WebSocket port family so clients no longer need one process per group.
	SharedInbound SharedInboundSettings `json:"shared_inbound"`
	// Guard carries the operator's write-surface guards; see GuardSettings.
	Guard GuardSettings `json:"guard"`
	// LogRetentionDays is how many days the product keeps the log files it
	// writes itself (the managed Mihomo log and its byte-rotated backups) and
	// the stale short runtime instance directories under ~/.hx-pxy-runtime.
	// 0 disables the cleanup; the default (30) applies to any stored document
	// that does not mention the field, because Load decodes over Default().
	// See .agents/notes/implemented/feature/2026-10-14-log-retention-by-mtime.md.
	LogRetentionDays int `json:"log_retention_days"`
}

// FleetSettings gates the built-in CF worker fleet maintainer.
type FleetSettings struct {
	Enabled                bool   `json:"enabled"`
	FleetSize              int    `json:"fleet_size"`
	ProbeIntervalSeconds   int    `json:"probe_interval_seconds"`
	MaxConsecutiveFailures int    `json:"max_consecutive_failures"`
	Proxy                  string `json:"proxy,omitempty"`
	// WorkerTemplate / ObfuscateScript point at HX-CF-Tunnel (20260823 user
	// decision: the fleet deploys the HX-CF-Tunnel cfnew worker with a random
	// obfuscation recipe per deploy).
	WorkerTemplate  string `json:"worker_template,omitempty"`
	ObfuscateScript string `json:"obfuscate_script,omitempty"`
	ObfuscateDir    string `json:"obfuscate_dir,omitempty"`
	// PublicHost is the public endpoint host for the per-account CF channel
	// (managed WebSocket channels require public_endpoint.host).
	PublicHost string `json:"public_host,omitempty"`
	PublicPort int    `json:"public_port,omitempty"`
}

// ProvisionSettings gates the public config-center endpoint.
type ProvisionSettings struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token,omitempty"`
}

// GuardSettings carries the operator's write-surface guards. They exist to stop
// unattended callers from growing the control plane's object graph.
//
// Why this is a guard and not a rule in the group service: the mistake being
// prevented is not a malformed request, it is a *modelling* choice that a human
// makes once and a script then repeats forever. The group service cannot tell
// "this caller wants one node" from "this caller wants a group that happens to
// select one node", and it should not try — the caller's identity is the only
// thing that separates an operator clicking a button from a loop creating one
// group per node. That identity lives in the credential, so the guard lives on
// the request path next to the authentication that resolves it.
//
// A single-member group is the shape this guard exists to prevent: a listener
// can name a node directly (see CreateRequest.NodeID in internal/listener), so
// materialising a group as a handle for one node adds an object that means
// nothing and accumulates. Measured before this guard existed: 61 groups for 28
// distinct nodes, one node having been wrapped in a group five times.
type GuardSettings struct {
	// DenyAPIGroupCreate blocks the group-creating write surface for API-key
	// callers: POST /api/v1/proxy-groups, POST /api/v1/proxy-services and
	// POST /api/v1/quickstart. A browser session is unaffected, because the
	// guard constrains *who* writes, not *what* may be written: the front end
	// remains free to create a multi-node group, and any caller may still edit
	// or delete an existing one.
	//
	// Every other write stays open to machine credentials on purpose. This is
	// not a general "read-only API key" switch: refreshing a subscription,
	// probing a node or rotating a listener is exactly what unattended callers
	// are for, and turning those off would break them to prevent a modelling
	// error they cannot even commit under this guard.
	//
	// A pointer, so that "the caller did not mention this field" is
	// distinguishable from "the caller wants it false". PUT /api/v1/settings
	// replaces the whole document, and a client that predates this field — or a
	// script that only wants to change the log level — decodes a missing key as
	// the zero value. With a plain bool that silently switched the guard off:
	// measured 2026-10-01, saving settings without this key cleared it while the
	// operator believed it was on. A safety gate must not be resettable by an
	// unrelated save, so an omitted field means "keep the stored value"; see
	// Service.Update for the merge, which is the only place that may act on nil.
	DenyAPIGroupCreate *bool `json:"deny_api_group_create,omitempty"`
}

// boolPointer is the smallest helper that lets a tri-state bool be expressed in
// a struct that is also the API DTO. Only the guard fields need it.
func boolPointer(value bool) *bool { return &value }

func Default() Settings {
	return Settings{
		TUN: &TUNSettings{},
		Quality: QualitySettings{
			CheckIntervalSeconds: 600,
			TimeoutSeconds:       10,
			BatchSize:            20,
			ProbeConcurrency:     4,
			TestURL:              defaultTestURL,
			HealthTargets: []HealthTarget{
				{ID: "chatgpt", Name: "ChatGPT", URL: "https://chatgpt.com/", Enabled: false},
				{ID: "claude", Name: "Claude", URL: "https://claude.ai/", Enabled: false},
				{ID: "github", Name: "GitHub", URL: "https://github.com/", Enabled: false},
				{ID: "google", Name: "Google", URL: "https://www.google.com/generate_204", Enabled: false},
				{ID: "telegram", Name: "Telegram", URL: "https://telegram.org/", Enabled: false},
			},
		},
		DNS: DNSSettings{
			Enabled:           true,
			IPv6:              false,
			EnhancedMode:      "normal",
			DefaultNameserver: []string{"223.5.5.5", "119.29.29.29"},
			Nameserver:        []string{"https://223.5.5.5/dns-query", "https://120.53.53.53/dns-query"},
			Fallback:          []string{},
		},
		Performance: PerformanceSettings{
			TCPConcurrent:     true,
			UnifiedDelay:      true,
			KeepAliveIdle:     15,
			KeepAliveInterval: 15,
			FindProcessMode:   "off",
			LogLevel:          "warning",
		},
		SharedInbound: DefaultSharedInbound(),
		// The user's ask (2026-10-14): keep the product's own logs for 30 days.
		LogRetentionDays: DefaultLogRetentionDays,
		// Default off: an upgraded installation must keep behaving exactly as
		// it did until an operator turns the guard on. Turning it on by default
		// would break every existing script on the next restart, with the
		// failure surfacing far from the change that caused it.
		Guard: GuardSettings{DenyAPIGroupCreate: boolPointer(false)},
		Fleet: FleetSettings{
			Enabled:                false,
			FleetSize:              10,
			ProbeIntervalSeconds:   300,
			MaxConsecutiveFailures: 3,
			// 20260823 user decision: deploy depends on HX-CF-Tunnel. The
			// defaults are relative to the executable (packaged cfworker/
			// dir); a configured absolute dev path takes precedence.
			WorkerTemplate:  "cfworker/worker.js",
			ObfuscateScript: "cfworker/obfuscate.sh",
			ObfuscateDir:    "/tmp/hx-cfworker-obf",
			PublicHost:      "pxy.woa.qzz.io",
			PublicPort:      443,
		},
	}
}

func Load(ctx context.Context, repository Reader) (Settings, error) {
	value, err := repository.GetMetadata(ctx, MetadataKey)
	if errors.Is(err, store.ErrNotFound) {
		settings := Default()
		legacy, legacyErr := repository.GetMetadata(ctx, legacyQualityKey)
		if errors.Is(legacyErr, store.ErrNotFound) {
			return settings, nil
		}
		if legacyErr != nil {
			return Settings{}, legacyErr
		}
		if err := json.Unmarshal([]byte(legacy), &settings.Quality); err != nil {
			return Settings{}, fmt.Errorf("decode legacy quality settings: %w", err)
		}
		if settings.Quality.ProbeConcurrency == 0 {
			settings.Quality.ProbeConcurrency = Default().Quality.ProbeConcurrency
		}
		if settings.Quality.HealthTargets == nil {
			settings.Quality.HealthTargets = Default().Quality.HealthTargets
		}
		migrateLegacyProbeDefaults(&settings)
		if err := Validate(settings); err != nil {
			return Settings{}, err
		}
		return settings, nil
	}
	if err != nil {
		return Settings{}, err
	}
	settings := Default()
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return Settings{}, fmt.Errorf("decode global settings: %w", err)
	}
	migrateLegacyProbeDefaults(&settings)
	if err := Validate(settings); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// migrateLegacyProbeDefaults aligns installs that still carry the original
// project defaults with Clash Verge's Mihomo URL-test defaults. A customized
// timeout or URL is left untouched.
func migrateLegacyProbeDefaults(settings *Settings) {
	if settings.Quality.TestURL == legacyTestURL && settings.Quality.TimeoutSeconds == 8 {
		settings.Quality.TestURL = defaultTestURL
		settings.Quality.TimeoutSeconds = 10
	}
	migrateLegacySharedInbound(settings)
}

// migrateLegacySharedInbound keeps an upgraded installation on its existing
// dedicated ports. A stored document that predates the shared inbound section
// decodes to the zero value, which defaults to the historical per-service
// behaviour; only a document that already carries the new defaults may adopt
// them. Reading back a stored document is therefore never a silent port move.
func migrateLegacySharedInbound(settings *Settings) {
	if settings.SharedInbound.Mode != "" {
		return
	}
	settings.SharedInbound.Mode = SharedInboundPerService
	defaults := DefaultSharedInbound()
	if strings.TrimSpace(settings.SharedInbound.MixedBindAddress) == "" {
		settings.SharedInbound.MixedBindAddress = defaults.MixedBindAddress
	}
	if settings.SharedInbound.MixedPort == 0 {
		settings.SharedInbound.MixedPort = defaults.MixedPort
	}
	if settings.SharedInbound.WSPort == 0 {
		settings.SharedInbound.WSPort = defaults.WSPort
	}
}

func Save(ctx context.Context, repository Repository, settings Settings) error {
	normalize(&settings)
	if err := Validate(settings); err != nil {
		return err
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode global settings: %w", err)
	}
	return repository.SetMetadata(ctx, MetadataKey, string(encoded))
}

// PreApplyFunc runs after the settings are persisted and before the data
// plane is asked to compile. It lets a settings change materialize the
// resources the new configuration depends on (for example the aggregate
// listeners of the shared inbound) inside the same atomic apply and rollback.
type PreApplyFunc func(context.Context) error

// Option customizes the settings service.
type Option func(*Service) error

// WithPreApply installs the pre-apply hook.
func WithPreApply(hook PreApplyFunc) Option {
	return func(service *Service) error {
		service.preApply = hook
		return nil
	}
}

type Service struct {
	repository Repository
	applier    Applier
	preApply   PreApplyFunc
}

func NewService(repository Repository, applier Applier, options ...Option) (*Service, error) {
	if repository == nil {
		return nil, errors.New("settings repository is required")
	}
	service := &Service{repository: repository, applier: applier}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// apply runs the pre-apply convergence hook and then the data-plane apply.
func (s *Service) apply(ctx context.Context) error {
	if s.preApply != nil {
		if err := s.preApply(ctx); err != nil {
			return err
		}
	}
	if s.applier == nil {
		return nil
	}
	return s.applier.Apply(ctx)
}

func (s *Service) Get(ctx context.Context) (Settings, error) {
	return Load(ctx, s.repository)
}

func (s *Service) Update(ctx context.Context, settings Settings) (Settings, error) {
	previous, err := Load(ctx, s.repository)
	if err != nil {
		return Settings{}, err
	}
	if settings.TUN == nil {
		settings.TUN = previous.TUN
	}
	// PUT replaces the whole document, so a field the caller omitted arrives as
	// its zero value. For the guard fields that is not a statement about the
	// value — it is an absent statement, and treating it as "off" let any
	// unrelated save (change the log level; save from a client that predates
	// the field) silently disarm a switch the operator had turned on. Measured
	// 2026-10-01. Absent therefore means "unchanged", and this merge is the only
	// code allowed to read the nil case; everywhere else the value is concrete.
	if settings.Guard.DenyAPIGroupCreate == nil {
		settings.Guard.DenyAPIGroupCreate = boolPointer(previous.Guard.DenyAPIGroupCreate != nil && *previous.Guard.DenyAPIGroupCreate)
	}
	normalize(&settings)
	if err := Validate(settings); err != nil {
		return Settings{}, err
	}
	if err := Save(ctx, s.repository, settings); err != nil {
		return Settings{}, err
	}
	if err := s.apply(ctx); err != nil {
		rollbackErr := Save(ctx, s.repository, previous)
		if rollbackErr == nil {
			rollbackErr = s.apply(ctx)
		}
		return Settings{}, fmt.Errorf("apply global settings: %w; rollback: %v", err, rollbackErr)
	}
	return settings, nil
}

func Validate(settings Settings) error {
	if err := validateTUN(settings.TUN, settings.DNS); err != nil {
		return err
	}
	quality := settings.Quality
	if quality.CheckIntervalSeconds < 60 || quality.CheckIntervalSeconds > 86400 {
		return fmt.Errorf("%w: quality.check_interval_seconds must be between 60 and 86400", ErrInvalid)
	}
	if quality.TimeoutSeconds < 1 || quality.TimeoutSeconds > 30 {
		return fmt.Errorf("%w: quality.timeout_seconds must be between 1 and 30", ErrInvalid)
	}
	if quality.BatchSize < 1 || quality.BatchSize > 200 {
		return fmt.Errorf("%w: quality.batch_size must be between 1 and 200", ErrInvalid)
	}
	if quality.ProbeConcurrency < 1 || quality.ProbeConcurrency > 16 {
		return fmt.Errorf("%w: quality.probe_concurrency must be between 1 and 16", ErrInvalid)
	}
	if err := validatePublicHTTPURL(quality.TestURL); err != nil {
		return fmt.Errorf("%w: quality.test_url: %v", ErrInvalid, err)
	}
	if settings.Fleet.Enabled {
		if settings.Fleet.FleetSize < 1 || settings.Fleet.FleetSize > 50 {
			return fmt.Errorf("%w: fleet.fleet_size must be between 1 and 50", ErrInvalid)
		}
		if settings.Fleet.ProbeIntervalSeconds < 30 || settings.Fleet.ProbeIntervalSeconds > 86400 {
			return fmt.Errorf("%w: fleet.probe_interval_seconds must be between 30 and 86400", ErrInvalid)
		}
		if settings.Fleet.MaxConsecutiveFailures < 1 || settings.Fleet.MaxConsecutiveFailures > 10 {
			return fmt.Errorf("%w: fleet.max_consecutive_failures must be between 1 and 10", ErrInvalid)
		}
	}
	if len(quality.HealthTargets) > 12 {
		return fmt.Errorf("%w: at most 12 health targets are allowed", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(quality.HealthTargets))
	for _, target := range quality.HealthTargets {
		if !validID(target.ID) || len(target.Name) < 1 || len(target.Name) > 40 {
			return fmt.Errorf("%w: health target id or name is invalid", ErrInvalid)
		}
		if _, duplicate := seen[target.ID]; duplicate {
			return fmt.Errorf("%w: duplicate health target id %q", ErrInvalid, target.ID)
		}
		seen[target.ID] = struct{}{}
		if err := validatePublicHTTPURL(target.URL); err != nil {
			return fmt.Errorf("%w: health target %q: %v", ErrInvalid, target.ID, err)
		}
	}
	if settings.DNS.EnhancedMode != "normal" && settings.DNS.EnhancedMode != "fake-ip" && settings.DNS.EnhancedMode != "redir-host" {
		return fmt.Errorf("%w: dns.enhanced_mode is invalid", ErrInvalid)
	}
	if len(settings.DNS.DefaultNameserver) < 1 || len(settings.DNS.DefaultNameserver) > 8 || len(settings.DNS.Nameserver) < 1 || len(settings.DNS.Nameserver) > 8 || len(settings.DNS.Fallback) > 8 {
		return fmt.Errorf("%w: DNS server lists must contain 1-8 primary entries and at most 8 fallback entries", ErrInvalid)
	}
	for _, server := range append(append(append([]string{}, settings.DNS.DefaultNameserver...), settings.DNS.Nameserver...), settings.DNS.Fallback...) {
		if len(server) < 1 || len(server) > 256 || strings.ContainsAny(server, "\r\n") {
			return fmt.Errorf("%w: invalid DNS server entry", ErrInvalid)
		}
	}
	performance := settings.Performance
	if performance.KeepAliveIdle < 0 || performance.KeepAliveIdle > 600 || performance.KeepAliveInterval < 0 || performance.KeepAliveInterval > 600 {
		return fmt.Errorf("%w: keep-alive values must be between 0 and 600 seconds", ErrInvalid)
	}
	if performance.FindProcessMode != "off" && performance.FindProcessMode != "strict" && performance.FindProcessMode != "always" {
		return fmt.Errorf("%w: performance.find_process_mode is invalid", ErrInvalid)
	}
	if performance.LogLevel != "silent" && performance.LogLevel != "error" && performance.LogLevel != "warning" && performance.LogLevel != "info" && performance.LogLevel != "debug" {
		return fmt.Errorf("%w: performance.log_level is invalid", ErrInvalid)
	}
	// Provision（配置中心）：启用时必须给 token，且 token 必须满足 URL 路径段
	// 安全要求（同 validID：小写字母数字 - _）。token 即公开端点的唯一凭据。
	if settings.Provision.Enabled {
		if !validID(settings.Provision.Token) {
			return fmt.Errorf("%w: provision.token must be 1-32 lowercase chars, digits, - or _", ErrInvalid)
		}
	}
	if err := validateSharedInbound(settings.SharedInbound); err != nil {
		return err
	}
	if settings.LogRetentionDays < 0 || settings.LogRetentionDays > logRetentionMaxDays {
		return fmt.Errorf("%w: log_retention_days must be between 0 (disabled) and %d", ErrInvalid, logRetentionMaxDays)
	}
	return nil
}

func normalize(settings *Settings) {
	settings.Quality.TestURL = strings.TrimSpace(settings.Quality.TestURL)
	for index := range settings.Quality.HealthTargets {
		settings.Quality.HealthTargets[index].ID = strings.ToLower(strings.TrimSpace(settings.Quality.HealthTargets[index].ID))
		settings.Quality.HealthTargets[index].Name = strings.TrimSpace(settings.Quality.HealthTargets[index].Name)
		settings.Quality.HealthTargets[index].URL = strings.TrimSpace(settings.Quality.HealthTargets[index].URL)
	}
	trimList(settings.DNS.DefaultNameserver)
	trimList(settings.DNS.Nameserver)
	trimList(settings.DNS.Fallback)
	settings.Provision.Token = strings.TrimSpace(settings.Provision.Token)
	if !settings.Provision.Enabled {
		settings.Provision.Token = ""
	}
	normalizeSharedInbound(&settings.SharedInbound)
}

func trimList(values []string) {
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
}

func validID(value string) bool {
	if len(value) < 1 || len(value) > 32 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func validatePublicHTTPURL(value string) error {
	if len(value) > 512 {
		return errors.New("URL must not exceed 512 characters")
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("URL must be an absolute HTTP(S) URL without credentials")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return errors.New("URL must not target a private or special-use IP")
	}
	return nil
}
