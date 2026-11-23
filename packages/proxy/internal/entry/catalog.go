package entry

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/pipeline"
	"github.com/HengXin666/hx-pxy/internal/slot"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// ProfileTTL is how long an exit measurement may steer routing.
const ProfileTTL = 72 * time.Hour

// Candidate is one subscription node the entry can route to.
type Candidate struct {
	mihomo.EntryNode
	Pools       []string `json:"pools"`
	Country     string   `json:"country"`
	City        string   `json:"city"`
	ASN         int      `json:"asn"`
	Alive       bool     `json:"alive"`
	Identity    string   `json:"identity"`
	ProfileAt   string   `json:"profile_at,omitempty"`
	Residential bool     `json:"residential"`
}

// Snapshot is one immutable view of everything the entry can dial.
type Snapshot struct {
	HubError   string
	HubAddress string
	HubSecret  string
	Nodes      []Candidate
	byID       map[string]int
	groups     map[string]mihomo.EntryGroup
	groupIDs   map[string]mihomo.EntryGroup
}

// BuildSnapshot classifies the compiled nodes into pools. A node joins subresi
// when its source is declared residential or a fresh exit measurement says the
// exit is a residential network, every subscription node is in sub. Nodes that
// no subscription holds are left out.
func BuildSnapshot(catalog mihomo.EntryCatalog, classes map[string][]store.SourceClass, profiles []store.NodeExitProfileRecord, now time.Time) *Snapshot {
	profileByID := make(map[string]store.NodeExitProfileRecord, len(profiles))
	for _, profile := range profiles {
		profileByID[profile.NodeID] = profile
	}
	snapshot := &Snapshot{
		HubError:   catalog.HubError,
		HubAddress: catalog.HubAddress,
		HubSecret:  catalog.HubSecret,
		byID:       make(map[string]int, len(catalog.Nodes)),
		groups:     make(map[string]mihomo.EntryGroup, len(catalog.Groups)),
		groupIDs:   make(map[string]mihomo.EntryGroup, len(catalog.Groups)),
	}
	for _, node := range catalog.Nodes {
		nodeClasses, held := classes[node.ID]
		if !held {
			continue
		}
		candidate := Candidate{
			EntryNode: node,
			Pools:     []string{PoolSubscription},
			Alive:     aliveState(node.LifecycleState),
			Identity:  strings.ToLower(node.Protocol + "|" + node.Server + "|" + strconv.Itoa(node.Port)),
			Country:   pipeline.RegionFromName(node.DisplayName),
		}
		declared := false
		for _, class := range nodeClasses {
			if class == store.SourceClassResidential {
				declared = true
			}
		}
		profile, measured := profileByID[node.ID]
		fresh := measured && profile.Success && now.Sub(profile.SampledAt) < ProfileTTL
		if fresh {
			candidate.Country = profile.CountryCode
			candidate.City = strings.ToLower(strings.ReplaceAll(profile.City, " ", "_"))
			candidate.ASN = profile.ASN
			candidate.ProfileAt = profile.SampledAt.UTC().Format(time.RFC3339)
		}
		candidate.Residential = declared || (fresh && profile.Residential)
		if candidate.Residential {
			candidate.Pools = append(candidate.Pools, PoolSubscriptionResidential)
		}
		snapshot.byID[node.ID] = len(snapshot.Nodes)
		snapshot.Nodes = append(snapshot.Nodes, candidate)
	}
	for _, group := range catalog.Groups {
		snapshot.groups[strings.ToLower(group.Name)] = group
		snapshot.groupIDs[group.ID] = group
	}
	return snapshot
}

// Group finds an enabled proxy group by name, case-insensitive.
func (s *Snapshot) Group(name string) (mihomo.EntryGroup, bool) {
	group, ok := s.groups[strings.ToLower(name)]
	return group, ok
}

// groupByID finds an enabled proxy group by id.
func (s *Snapshot) groupByID(id string) (mihomo.EntryGroup, bool) {
	group, ok := s.groupIDs[id]
	return group, ok
}

// Candidate returns one node by id.
func (s *Snapshot) Candidate(id string) (Candidate, bool) {
	index, ok := s.byID[id]
	if !ok {
		return Candidate{}, false
	}
	return s.Nodes[index], true
}

// aliveState reports whether a lifecycle state may carry traffic.
func aliveState(state string) bool {
	switch state {
	case "disabled", "quarantined", "retired":
		return false
	default:
		return true
	}
}

// snapshotCatalog adapts a Snapshot to the slot.Catalog a Table resolves with.
type snapshotCatalog struct {
	snapshot *Snapshot
	rotation uint64
}

// Lookup implements slot.Catalog.
func (c snapshotCatalog) Lookup(id string, request slot.Request) (slot.Node, bool) {
	candidate, ok := c.snapshot.Candidate(id)
	if !ok {
		return slot.Node{}, false
	}
	alive := candidate.Alive && candidate.matches(request)
	return slot.Node{ID: id, Identity: candidate.Identity, Alive: alive, Country: candidate.Country, ASN: candidate.ASN}, true
}

// matches reports whether a candidate meets a request's constraints.
func (c Candidate) matches(request slot.Request) bool {
	switch {
	case !containsString(c.Pools, request.Pool):
		return false
	case request.Country != "" && c.Country != request.Country:
		return false
	case request.City != "" && c.City != request.City:
		return false
	case request.ASN != 0 && c.ASN != request.ASN:
		return false
	}
	return true
}

// Pick implements slot.Catalog. A node that serves the same endpoint as the one
// just lost wins, so a subscription that only renamed or re-keyed a node keeps
// the same exit. Otherwise candidates are spread by a rotating offset.
func (c snapshotCatalog) Pick(request slot.Request, previous slot.Binding) (slot.Node, error) {
	matches := make([]Candidate, 0, 16)
	identity := previous.Identity
	if lost, ok := c.snapshot.Candidate(previous.NodeID); ok && identity == "" {
		identity = lost.Identity
	}
	for _, candidate := range c.snapshot.Nodes {
		if !candidate.Alive || candidate.ID == previous.NodeID || !candidate.matches(request) {
			continue
		}
		if identity != "" && candidate.Identity == identity {
			return slot.Node{ID: candidate.ID, Identity: candidate.Identity, Alive: true}, nil
		}
		matches = append(matches, candidate)
	}
	if len(matches) == 0 {
		return slot.Node{}, slot.ErrNoCandidate
	}
	chosen := matches[c.rotation%uint64(len(matches))]
	return slot.Node{ID: chosen.ID, Identity: chosen.Identity, Alive: true, Country: chosen.Country, ASN: chosen.ASN}, nil
}

// snapshotHolder keeps the current snapshot for lock-free reads.
type snapshotHolder struct{ value atomic.Pointer[Snapshot] }

func (h *snapshotHolder) load() *Snapshot {
	if snapshot := h.value.Load(); snapshot != nil {
		return snapshot
	}
	return &Snapshot{byID: map[string]int{}, groups: map[string]mihomo.EntryGroup{}, groupIDs: map[string]mihomo.EntryGroup{}}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
