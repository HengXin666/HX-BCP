// Package slot keeps the stable logical exits clients name, separate from the
// physical nodes behind them. A slot id never changes, the node it is bound to
// may, and the loss policy decides what happens when that node goes away.
package slot

import (
	"errors"
	"strconv"
	"sync"
	"time"
)

// ErrNoCandidate reports that no node satisfies a slot's constraints.
var ErrNoCandidate = errors.New("no node matches the slot")

// ErrNodeLost reports a fail-policy slot whose node is gone.
var ErrNodeLost = errors.New("slot node is gone")

// Loss policies, spelled as the entry dialect spells them.
const (
	Rebind = "rebind"
	Hold   = "hold"
	Fail   = "fail"
)

// Node is the part of a physical node a slot binding needs.
type Node struct {
	ID string
	// Identity names the endpoint behind the node, stable across renames.
	Identity string
	Pool     string
	Country  string
	ASN      int
	Alive    bool
}

// Request names a slot and the constraints its node must meet.
type Request struct {
	Tenant  string
	Slot    string
	Pool    string
	Country string
	City    string
	ASN     int
	TTL     time.Duration
	OnLoss  string
	Grace   time.Duration
	// MaxAge, when set, ends a binding that long after it was made however
	// often it is used.
	MaxAge time.Duration
}

// Binding is the current node of one slot.
type Binding struct {
	NodeID string
	// Identity and Constraint are what the slot was bound with: the node's
	// endpoint identity and the request constraints it had to meet.
	Identity   string
	Constraint string
	BoundAt    time.Time
	ExpiresAt  time.Time
	LostAt     time.Time
	Rebinds    int
}

// Catalog answers which nodes exist now and picks one for a request. Lookup
// reports a node that no longer meets the request as not alive.
type Catalog interface {
	Lookup(nodeID string, request Request) (Node, bool)
	Pick(request Request, previous Binding) (Node, error)
}

// Constraint is the part of a request a binding must keep meeting.
func (r Request) Constraint() string {
	return r.Pool + "|" + r.Country + "|" + r.City + "|" + strconv.Itoa(r.ASN)
}

type key struct{ tenant, slot string }

// Table holds every live slot binding. It is safe for concurrent use.
type Table struct {
	mutex    sync.Mutex
	bindings map[key]Binding
	clock    func() time.Time
}

// NewTable builds an empty table, clock may be nil for time.Now.
func NewTable(clock func() time.Time) *Table {
	if clock == nil {
		clock = time.Now
	}
	return &Table{bindings: make(map[key]Binding), clock: clock}
}

// Resolve returns the node a slot should dial now. An expired binding picks a
// fresh node, a healthy one is kept and its expiry extended. A binding whose node
// vanished or died follows the request's loss policy: rebind picks the closest
// match, hold keeps the old node through the grace period then rebinds, fail
// returns ErrNodeLost until the binding expires.
func (t *Table) Resolve(catalog Catalog, request Request) (Binding, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	now := t.clock()
	slotKey := key{request.Tenant, request.Slot}
	current, exists := t.bindings[slotKey]
	if !exists || now.After(current.ExpiresAt) || (request.MaxAge > 0 && now.Sub(current.BoundAt) >= request.MaxAge) {
		return t.bind(catalog, request, slotKey, Binding{}, now)
	}
	if current.Constraint != request.Constraint() {
		return t.bind(catalog, request, slotKey, current, now)
	}
	node, found := catalog.Lookup(current.NodeID, request)
	if found && node.Alive {
		current.LostAt = time.Time{}
		current.ExpiresAt = now.Add(request.TTL)
		t.bindings[slotKey] = current
		return current, nil
	}
	if current.LostAt.IsZero() {
		current.LostAt = now
	}
	switch request.OnLoss {
	case Fail:
		t.bindings[slotKey] = current
		return Binding{}, ErrNodeLost
	case Hold:
		if found && now.Sub(current.LostAt) < request.Grace {
			t.bindings[slotKey] = current
			return current, nil
		}
	}
	return t.bind(catalog, request, slotKey, current, now)
}

// bind attaches a slot to a newly picked node. The catalog gets the binding the
// slot just lost, never to pick its node again but to prefer its successor.
func (t *Table) bind(catalog Catalog, request Request, slotKey key, previous Binding, now time.Time) (Binding, error) {
	node, err := catalog.Pick(request, previous)
	if err != nil {
		return Binding{}, err
	}
	next := Binding{NodeID: node.ID, Identity: node.Identity, Constraint: request.Constraint(), BoundAt: now, ExpiresAt: now.Add(request.TTL)}
	if previous.NodeID != "" {
		next.Rebinds = previous.Rebinds + 1
	}
	t.bindings[slotKey] = next
	return next, nil
}

// Release drops a slot so its next resolve picks a fresh node.
func (t *Table) Release(tenant, slot string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	delete(t.bindings, key{tenant, slot})
}

// ReleaseTenant drops every slot of one tenant.
func (t *Table) ReleaseTenant(tenant string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for slotKey := range t.bindings {
		if slotKey.tenant == tenant {
			delete(t.bindings, slotKey)
		}
	}
}

// Sweep removes expired bindings and returns how many it removed.
func (t *Table) Sweep() int {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	now := t.clock()
	removed := 0
	for slotKey, binding := range t.bindings {
		if now.After(binding.ExpiresAt) {
			delete(t.bindings, slotKey)
			removed++
		}
	}
	return removed
}

// Entry is one slot binding as listed for operators.
type Entry struct {
	Tenant  string  `json:"tenant"`
	Slot    string  `json:"slot"`
	Binding Binding `json:"binding"`
}

// List returns every live binding, optionally for one tenant only.
func (t *Table) List(tenant string) []Entry {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	items := make([]Entry, 0, len(t.bindings))
	for slotKey, binding := range t.bindings {
		if tenant != "" && slotKey.tenant != tenant {
			continue
		}
		items = append(items, Entry{Tenant: slotKey.tenant, Slot: slotKey.slot, Binding: binding})
	}
	return items
}
