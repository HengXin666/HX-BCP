// Package slot keeps the stable logical exits clients name, separate from the
// physical nodes behind them. A slot id never changes, the node it is bound to
// may, and the loss policy decides what happens when that node goes away.
package slot

import (
	"errors"
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
	ID      string
	Pool    string
	Country string
	ASN     int
	Alive   bool
}

// Request names a slot and the constraints its node must meet.
type Request struct {
	Tenant  string
	Slot    string
	Pool    string
	Country string
	ASN     int
	TTL     time.Duration
	OnLoss  string
	Grace   time.Duration
}

// Binding is the current node of one slot.
type Binding struct {
	NodeID    string
	BoundAt   time.Time
	ExpiresAt time.Time
	LostAt    time.Time
	Rebinds   int
}

// Catalog answers which nodes exist now and picks one for a request.
type Catalog interface {
	Lookup(nodeID string) (Node, bool)
	Pick(request Request, exclude map[string]bool) (Node, error)
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
	if !exists || now.After(current.ExpiresAt) {
		return t.bind(catalog, request, slotKey, Binding{}, now)
	}
	node, found := catalog.Lookup(current.NodeID)
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

// bind attaches a slot to a newly picked node, excluding the node it just lost.
func (t *Table) bind(catalog Catalog, request Request, slotKey key, previous Binding, now time.Time) (Binding, error) {
	exclude := map[string]bool{}
	if previous.NodeID != "" {
		exclude[previous.NodeID] = true
	}
	node, err := catalog.Pick(request, exclude)
	if err != nil {
		return Binding{}, err
	}
	next := Binding{NodeID: node.ID, BoundAt: now, ExpiresAt: now.Add(request.TTL)}
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
