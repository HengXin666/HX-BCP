package dockeraccess

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

// Companion ports for bridge placements are taken from a small, fixed window
// so they are predictable for firewall rules and never collide with the
// conventional 7890/7891 shared entry ports.
const (
	bridgePortFirst = 17890
	bridgePortLast  = 17989
)

// Repository persists the source -> companion links.
type Repository interface {
	ListResidentialChannels(context.Context) ([]store.ResidentialChannelRecord, error)
	ListDockerAccessEntries(context.Context) ([]store.DockerAccessEntryRecord, error)
	CreateDockerAccessEntry(context.Context, store.DockerAccessEntryRecord) error
	DeleteDockerAccessEntry(context.Context, string) error
	DeleteDockerAccessEntryFor(context.Context, string) error
}

// Listeners is the subset of the listener service this package drives. All
// writes go through it so validation, credential sealing and data plane
// apply/rollback stay in one place.
type Listeners interface {
	List(context.Context) ([]listener.Listener, error)
	Get(context.Context, string) (listener.Listener, error)
	Create(context.Context, listener.CreateRequest) (listener.Listener, error)
	Delete(context.Context, string, int) error
}

// SettingsReader reads the shared-entry configuration.
type SettingsReader interface {
	Get(context.Context) (systemsettings.Settings, error)
}

// SharedConverger republishes the shared entry rows after membership changes.
type SharedConverger interface {
	ConvergeSharedInbound(context.Context) error
}

// ApplyRequest confirms a previewed plan.
type ApplyRequest struct {
	Gateway string `json:"gateway"`
	PlanID  string `json:"plan_id"`
	Confirm bool   `json:"confirm"`
}

// Link is the public view of one source -> companion link.
type Link struct {
	ListenerID      string    `json:"listener_id"`
	EntryListenerID string    `json:"entry_listener_id"`
	BindTarget      string    `json:"bind_target"`
	CreatedAt       time.Time `json:"created_at"`
	// Withheld is true when the running data plane does not serve the entry
	// (its source is disabled or its bridge address is missing).
	Withheld bool `json:"withheld"`
}

type Service struct {
	repository Repository
	listeners  Listeners
	settings   SettingsReader
	converger  SharedConverger
	bridges    func() ([]Bridge, error)
	lan        func() []string
	portFree   func(host string, port int) bool
	now        func() time.Time
	// withheld reports whether the running data plane leaves out a companion
	// entry; nil means "served" (no data plane wired, as in tests).
	withheld func(string) bool
	// mu serialises Apply/Revoke: two confirmations for one service must not
	// create two companions, and port allocation must not race itself.
	mu sync.Mutex
}

// Option customises the host-facing probes; tests replace them.
type Option func(*Service)

// WithBridges replaces the host bridge probe. The probe reports its failure
// instead of returning an empty list, so the assessment can tell "no Docker
// bridge here" apart from "the host link table could not be read".
func WithBridges(source func() ([]Bridge, error)) Option {
	return func(s *Service) { s.bridges = source }
}

func WithLAN(source func() []string) Option { return func(s *Service) { s.lan = source } }

// WithWithheld wires the data plane's view of which companion entries the
// running document leaves out, so Assess never reports one as ready.
func WithWithheld(withheld func(string) bool) Option {
	return func(s *Service) { s.withheld = withheld }
}
func WithPortProbe(probe func(string, int) bool) Option {
	return func(s *Service) { s.portFree = probe }
}

func NewService(repository Repository, listeners Listeners, settings SettingsReader, converger SharedConverger, options ...Option) (*Service, error) {
	if repository == nil || listeners == nil || settings == nil || converger == nil {
		return nil, errors.New("docker access requires a repository, listeners, settings and a shared-entry converger")
	}
	service := &Service{
		repository: repository, listeners: listeners, settings: settings, converger: converger,
		bridges: DiscoverBridges, lan: DiscoverLAN, portFree: tcpPortFree, now: time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// Assess returns the current answer for one service.
func (s *Service) Assess(ctx context.Context, listenerID, gateway string) (Assessment, error) {
	in, err := s.input(ctx, listenerID, gateway)
	if err != nil {
		return Assessment{}, err
	}
	return Assess(in), nil
}

// Links lists every source -> companion link.
func (s *Service) Links(ctx context.Context) ([]Link, error) {
	records, err := s.repository.ListDockerAccessEntries(ctx)
	if err != nil {
		return nil, err
	}
	links := make([]Link, 0, len(records))
	for _, record := range records {
		link := Link{ListenerID: record.ListenerID, EntryListenerID: record.EntryListenerID, BindTarget: record.BindTarget, CreatedAt: record.CreatedAt}
		if s.withheld != nil {
			link.Withheld = s.withheld(record.EntryListenerID)
		}
		links = append(links, link)
	}
	return links, nil
}

// Apply creates the companion entry described by a previewed plan.
func (s *Service) Apply(ctx context.Context, listenerID string, request ApplyRequest) (Assessment, error) {
	if !request.Confirm {
		return Assessment{}, fmt.Errorf("%w: confirm must be true after reviewing the preview", listener.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := s.input(ctx, listenerID, request.Gateway)
	if err != nil {
		return Assessment{}, err
	}
	current := Assess(in)
	if current.State != StateNeedsEntry || current.Plan == nil {
		return Assessment{}, fmt.Errorf("%w: this service needs no Docker entry (state %s)", listener.ErrConflict, current.State)
	}
	plan := current.Plan
	if strings.TrimSpace(request.PlanID) != plan.ID {
		return Assessment{}, fmt.Errorf("%w: the preview is out of date; review the new plan and confirm again", listener.ErrConflict)
	}
	username, password, err := newCredentials()
	if err != nil {
		return Assessment{}, err
	}
	enabled := true
	create := listener.CreateRequest{
		Name: plan.Name, Kind: plan.Kind, BindAddress: plan.BindAddress, Port: plan.Port,
		ProxyGroupID: in.Source.ProxyGroupID, NodeID: in.Source.NodeID,
		Auth: &listener.Auth{Username: username, Password: password}, Enabled: &enabled,
	}
	if plan.Placement == PlacementShared {
		create.SharedInbound = listener.SharedInboundStandardOwner
	}
	entry, err := s.listeners.Create(ctx, create)
	if err != nil {
		return Assessment{}, err
	}
	if plan.Placement == PlacementShared {
		if err := s.converger.ConvergeSharedInbound(ctx); err != nil {
			return Assessment{}, errors.Join(err, s.removeEntry(ctx, entry, true))
		}
	}
	target := plan.BindAddress
	if plan.Placement == PlacementShared {
		target = PlacementShared
	}
	link := store.DockerAccessEntryRecord{ListenerID: in.Source.ID, EntryListenerID: entry.ID, BindTarget: target, CreatedAt: s.now()}
	if err := s.repository.CreateDockerAccessEntry(ctx, link); err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = fmt.Errorf("%w: a Docker entry already exists for this service", listener.ErrConflict)
		}
		return Assessment{}, errors.Join(err, s.removeEntry(ctx, entry, plan.Placement == PlacementShared))
	}
	in.Entry = &entry
	return Assess(in), nil
}

// Revoke deletes the companion entry of a service. It is the one-click undo
// of Apply and is idempotent.
func (s *Service) Revoke(ctx context.Context, listenerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.entryFor(ctx, listenerID)
	if err != nil || entry == nil {
		return err
	}
	return s.removeEntry(ctx, *entry, entry.SharedInbound != "")
}

func (s *Service) removeEntry(ctx context.Context, entry listener.Listener, shared bool) error {
	if err := s.listeners.Delete(ctx, entry.ID, entry.Version); err != nil && !errors.Is(err, listener.ErrNotFound) {
		return err
	}
	// The cascade already drops the link in SQLite; deleting it explicitly
	// keeps the contract independent of the storage engine.
	if err := s.repository.DeleteDockerAccessEntryFor(ctx, entry.ID); err != nil {
		return err
	}
	if shared {
		return s.converger.ConvergeSharedInbound(ctx)
	}
	return nil
}

func (s *Service) entryFor(ctx context.Context, listenerID string) (*listener.Listener, error) {
	links, err := s.repository.ListDockerAccessEntries(ctx)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.ListenerID != listenerID {
			continue
		}
		entry, err := s.listeners.Get(ctx, link.EntryListenerID)
		if errors.Is(err, listener.ErrNotFound) {
			return nil, s.repository.DeleteDockerAccessEntry(ctx, listenerID)
		}
		if err != nil {
			return nil, err
		}
		return &entry, nil
	}
	return nil, nil
}

func (s *Service) input(ctx context.Context, listenerID, gateway string) (Input, error) {
	source, err := s.listeners.Get(ctx, strings.TrimSpace(listenerID))
	if err != nil {
		return Input{}, err
	}
	settings, err := s.settings.Get(ctx)
	if err != nil {
		return Input{}, fmt.Errorf("read global settings: %w", err)
	}
	residential, err := s.isResidential(ctx, source.ID)
	if err != nil {
		return Input{}, err
	}
	entry, err := s.entryFor(ctx, source.ID)
	if err != nil {
		return Input{}, err
	}
	all, err := s.listeners.List(ctx)
	if err != nil {
		return Input{}, err
	}
	taken := make(map[string]struct{}, len(all))
	usedPorts := map[int]struct{}{settings.SharedInbound.MixedPort: {}, settings.SharedInbound.WSPort: {}}
	for _, item := range all {
		taken[item.Name] = struct{}{}
		usedPorts[item.Port] = struct{}{}
	}
	bridges, bridgeErr := s.bridges()
	in := Input{
		Source: source, Residential: residential, Entry: entry, Bridges: bridges, BridgeScanFailed: bridgeErr != nil, LAN: s.lan(), Gateway: gateway,
		Shared: SharedEntry{
			Enabled: settings.SharedInbound.Enabled(), Bind: settings.SharedInbound.MixedBindAddress, Port: settings.SharedInbound.MixedPort,
		},
		TakenNames: taken,
	}
	if entry != nil && s.withheld != nil {
		in.EntryWithheld = s.withheld(entry.ID)
	}
	if selected := selectGateway(in.Bridges, gateway); selected != "" {
		in.AllocatedPort = s.allocatePort(selected, usedPorts)
	}
	return in, nil
}

func (s *Service) isResidential(ctx context.Context, listenerID string) (bool, error) {
	channels, err := s.repository.ListResidentialChannels(ctx)
	if err != nil {
		return false, err
	}
	for _, channel := range channels {
		if channel.ListenerID == listenerID || channel.DirectListenerID == listenerID {
			return true, nil
		}
	}
	return false, nil
}

// allocatePort returns the first port in the companion window that no
// listener row uses and that the host can currently bind on the gateway.
func (s *Service) allocatePort(host string, used map[int]struct{}) int {
	for port := bridgePortFirst; port <= bridgePortLast; port++ {
		if _, taken := used[port]; taken {
			continue
		}
		if s.portFree(host, port) {
			return port
		}
	}
	return 0
}

// tcpPortFree checks both TCP and UDP: a Mixed listener also serves UDP
// (SOCKS5 UDP ASSOCIATE), so a port taken on either protocol fails the apply.
func tcpPortFree(host string, port int) bool {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	stream, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	defer func() { _ = stream.Close() }()
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		return false
	}
	_ = packet.Close()
	return true
}

// newCredentials mints a username that is unique in practice (the shared
// entry routes by username) and a high-entropy password.
func newCredentials() (string, string, error) {
	user := make([]byte, 4)
	secret := make([]byte, 18)
	if _, err := rand.Read(user); err != nil {
		return "", "", fmt.Errorf("generate docker entry username: %w", err)
	}
	if _, err := rand.Read(secret); err != nil {
		return "", "", fmt.Errorf("generate docker entry password: %w", err)
	}
	return "docker-" + hex.EncodeToString(user), base64.RawURLEncoding.EncodeToString(secret), nil
}
