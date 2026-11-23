package entry

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/slot"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// ErrNotFound reports an unknown tenant or share token.
var ErrNotFound = errors.New("entry resource not found")

// Store is everything the entry service persists.
type Store interface {
	TenantStore
	ProfileStore
	ListNodeSourceClasses(context.Context) (map[string][]store.SourceClass, error)
}

// Config selects where the entry listens and whether exits are measured.
type Config struct {
	Listen string
	// PublicAddress is the host:port written into Clash profiles.
	PublicAddress string
	ExitProbe     bool
	// EgressInterface pins dials that leave the host to one interface.
	EgressInterface string
}

// Service owns the entry server, the routing snapshot and the background work.
type Service struct {
	config    Config
	store     Store
	tenants   *Tenants
	router    *Router
	server    *Server
	snapshots *snapshotHolder
	logger    *slog.Logger

	mutex     sync.Mutex
	catalog   mihomo.EntryCatalog
	listenErr string
	// rebuildMutex makes snapshot rebuilds take turns, so a slow rebuild of
	// an older catalog never overwrites a newer snapshot.
	rebuildMutex sync.Mutex
	probing      atomic.Bool
}

// NewService builds the entry service. Nothing listens until Start.
func NewService(ctx context.Context, config Config, entryStore Store, cipher Cipher, gateways GatewaySource, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	tenants, err := NewTenants(ctx, entryStore, cipher)
	if err != nil {
		return nil, err
	}
	snapshots := &snapshotHolder{}
	router := NewRouter(snapshots, gateways)
	router.SetEgressInterface(config.EgressInterface)
	server := NewServer(tenants, router, logger)
	tenants.changed = func(id string, deleted bool) {
		server.CloseTenant(id)
		if deleted {
			router.ReleaseTenant(id)
		}
	}
	return &Service{
		config:    config,
		store:     entryStore,
		tenants:   tenants,
		router:    router,
		server:    server,
		snapshots: snapshots,
		logger:    logger,
	}, nil
}

// OnCatalog receives each applied data-plane catalog.
func (s *Service) OnCatalog(catalog mihomo.EntryCatalog) {
	s.mutex.Lock()
	s.catalog = catalog
	s.mutex.Unlock()
	if err := s.rebuild(context.Background()); err != nil {
		s.logger.Warn("entry snapshot rebuild failed", "component", "entry", "error", err)
	}
}

// rebuild classifies the last catalog again with fresh profiles.
func (s *Service) rebuild(ctx context.Context) error {
	s.rebuildMutex.Lock()
	defer s.rebuildMutex.Unlock()
	s.mutex.Lock()
	catalog := s.catalog
	s.mutex.Unlock()
	classes, err := s.store.ListNodeSourceClasses(ctx)
	if err != nil {
		return err
	}
	profiles, err := s.store.ListNodeExitProfiles(ctx)
	if err != nil {
		return err
	}
	s.snapshots.value.Store(BuildSnapshot(catalog, classes, profiles, time.Now()))
	return nil
}

// Start listens and runs the background loops until ctx ends. A port that is
// taken is logged and reported in Status, the control plane keeps running.
func (s *Service) Start(ctx context.Context) {
	if strings.TrimSpace(s.config.Listen) != "" {
		listener, err := net.Listen("tcp", s.config.Listen)
		if err != nil {
			s.mutex.Lock()
			s.listenErr = err.Error()
			s.mutex.Unlock()
			s.logger.Error("entry listen failed", "component", "entry", "address", s.config.Listen, "error", err)
		} else {
			s.logger.Info("entry listening", "component", "entry", "address", listener.Addr().String())
			go func() {
				<-ctx.Done()
				_ = listener.Close()
			}()
			go func() {
				if err := s.server.Serve(ctx, listener); err != nil {
					s.logger.Error("entry stopped", "component", "entry", "error", err)
				}
			}()
		}
	}
	go s.loop(ctx)
}

func (s *Service) loop(ctx context.Context) {
	flush := time.NewTicker(15 * time.Second)
	probe := time.NewTicker(time.Minute)
	defer flush.Stop()
	defer probe.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = s.tenants.Flush(context.Background())
			return
		case <-flush.C:
			if err := s.tenants.Flush(ctx); err != nil {
				s.logger.Warn("entry usage flush failed", "component", "entry", "error", err)
			}
			s.router.Sweep()
		case <-probe.C:
			if s.config.ExitProbe && s.probing.CompareAndSwap(false, true) {
				go s.probeOnce(ctx)
			}
		}
	}
}

// probeOnce measures one batch of exits off the flush loop.
func (s *Service) probeOnce(ctx context.Context) {
	defer s.probing.Store(false)
	measured, err := s.router.probeExits(ctx, s.store, time.Now())
	if err != nil {
		s.logger.Warn("entry exit probe failed", "component", "entry", "error", err)
		return
	}
	if measured > 0 {
		if err := s.rebuild(ctx); err != nil {
			s.logger.Warn("entry snapshot rebuild failed", "component", "entry", "error", err)
		}
	}
}

// Status is the operator view of the entry.
type Status struct {
	Listen      string         `json:"listen"`
	ListenError string         `json:"listen_error,omitempty"`
	HubAddress  string         `json:"hub_address"`
	HubError    string         `json:"hub_error,omitempty"`
	ExitProbe   bool           `json:"exit_probe"`
	Pools       map[string]int `json:"pools"`
	Groups      []string       `json:"groups"`
	Gateways    []string       `json:"gateways"`
	Connections Stats          `json:"connections"`
}

// Status reports what the entry can serve right now.
func (s *Service) Status(ctx context.Context) Status {
	snapshot := s.snapshots.load()
	pools := map[string]int{PoolSubscription: 0, PoolSubscriptionResidential: 0, PoolResidential: 0, PoolDirect: 1}
	for _, candidate := range snapshot.Nodes {
		if !candidate.Alive {
			continue
		}
		for _, pool := range candidate.Pools {
			pools[pool]++
		}
	}
	groups := make([]string, 0, len(snapshot.groups))
	for _, group := range snapshot.groups {
		groups = append(groups, group.Name)
	}
	sort.Strings(groups)
	gatewayNames := []string{}
	if gateways, err := s.router.loadGateways(ctx); err == nil {
		for _, gateway := range gateways {
			gatewayNames = append(gatewayNames, gateway.Name)
		}
		pools[PoolResidential] = len(gateways)
	}
	s.mutex.Lock()
	listenErr := s.listenErr
	s.mutex.Unlock()
	return Status{
		Listen: s.config.Listen, ListenError: listenErr, HubAddress: snapshot.HubAddress,
		HubError: snapshot.HubError, ExitProbe: s.config.ExitProbe, Pools: pools, Groups: groups, Gateways: gatewayNames,
		Connections: s.server.Stats(),
	}
}

// Nodes lists the routable subscription nodes, optionally of one pool.
func (s *Service) Nodes(pool string) []Candidate {
	snapshot := s.snapshots.load()
	items := make([]Candidate, 0, len(snapshot.Nodes))
	for _, candidate := range snapshot.Nodes {
		if pool == "" || containsString(candidate.Pools, pool) {
			items = append(items, candidate)
		}
	}
	return items
}

// Tenants exposes the tenant registry.
func (s *Service) Tenants() *Tenants { return s.tenants }

// Slots lists live slot bindings.
func (s *Service) Slots(tenant string) map[string][]slot.Entry { return s.router.Slots(tenant) }

// ReleaseSlot drops one slot binding.
func (s *Service) ReleaseSlot(tenant, name string) { s.router.ReleaseSlot(tenant, name) }

// InvalidateGateways drops the residential provider cache.
func (s *Service) InvalidateGateways() { s.router.InvalidateGateways() }

// ShareToken is the token in a tenant's public subscription URL. It changes when
// the tenant secret rotates.
func (s *Service) ShareToken(tenantID string) (string, error) {
	_, secret, ok := s.tenants.SecretFor(tenantID)
	if !ok {
		return "", ErrNotFound
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("entry-share:" + tenantID))
	return hex.EncodeToString(mac.Sum(nil)[:16]), nil
}

// Subscription renders a tenant's Clash profile when the token matches.
func (s *Service) Subscription(tenantID, token string, options ClashOptions) ([]byte, error) {
	want, err := s.ShareToken(tenantID)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal([]byte(want), []byte(token)) {
		return nil, ErrNotFound
	}
	record, secret, _ := s.tenants.SecretFor(tenantID)
	if !record.Enabled {
		return nil, ErrNotFound
	}
	if publicHost, publicPort, err := net.SplitHostPort(s.config.PublicAddress); err == nil {
		if options.Host == "" {
			options.Host = publicHost
		}
		if options.Port == 0 {
			options.Port = atoiPort(publicPort)
		}
	}
	if options.Host == "" {
		options.Host = options.PanelHost
	}
	if options.Port == 0 {
		if _, port, err := net.SplitHostPort(s.config.Listen); err == nil {
			options.Port = atoiPort(port)
		}
	}
	return RenderClash(record, secret, s.snapshots.load(), options)
}
