package slot

import (
	"errors"
	"testing"
	"time"
)

type fakeCatalog struct {
	nodes map[string]Node
	order []string
}

func (c *fakeCatalog) Lookup(id string) (Node, bool) {
	node, ok := c.nodes[id]
	return node, ok
}

func (c *fakeCatalog) Pick(request Request, exclude map[string]bool) (Node, error) {
	for _, id := range c.order {
		node, ok := c.nodes[id]
		if !ok || !node.Alive || exclude[id] || node.Pool != request.Pool {
			continue
		}
		if request.Country != "" && node.Country != request.Country {
			continue
		}
		return node, nil
	}
	return Node{}, ErrNoCandidate
}

func newFixture() (*fakeCatalog, *Table, *time.Time) {
	now := time.Unix(1_000_000, 0)
	catalog := &fakeCatalog{
		nodes: map[string]Node{
			"a": {ID: "a", Pool: "sub", Country: "US", Alive: true},
			"b": {ID: "b", Pool: "sub", Country: "US", Alive: true},
			"c": {ID: "c", Pool: "sub", Country: "JP", Alive: true},
		},
		order: []string{"a", "b", "c"},
	}
	return catalog, NewTable(func() time.Time { return now }), &now
}

func request(onLoss string) Request {
	return Request{Tenant: "t", Slot: "s1", Pool: "sub", Country: "US", TTL: 10 * time.Minute, OnLoss: onLoss, Grace: 2 * time.Minute}
}

func TestResolveKeepsHealthyBinding(t *testing.T) {
	t.Parallel()
	catalog, table, now := newFixture()
	first, err := table.Resolve(catalog, request(Rebind))
	if err != nil || first.NodeID != "a" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	*now = now.Add(5 * time.Minute)
	second, _ := table.Resolve(catalog, request(Rebind))
	if second.NodeID != "a" || !second.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("second = %+v", second)
	}
}

func TestResolveRebindsWithinConstraints(t *testing.T) {
	t.Parallel()
	catalog, table, _ := newFixture()
	_, _ = table.Resolve(catalog, request(Rebind))
	delete(catalog.nodes, "a")
	got, err := table.Resolve(catalog, request(Rebind))
	if err != nil || got.NodeID != "b" || got.Rebinds != 1 {
		t.Fatalf("got = %+v, %v", got, err)
	}
}

func TestResolveHoldsThroughGrace(t *testing.T) {
	t.Parallel()
	catalog, table, now := newFixture()
	_, _ = table.Resolve(catalog, request(Hold))
	dead := catalog.nodes["a"]
	dead.Alive = false
	catalog.nodes["a"] = dead
	held, _ := table.Resolve(catalog, request(Hold))
	if held.NodeID != "a" {
		t.Fatalf("held = %+v", held)
	}
	*now = now.Add(3 * time.Minute)
	moved, _ := table.Resolve(catalog, request(Hold))
	if moved.NodeID != "b" {
		t.Fatalf("moved = %+v", moved)
	}
}

func TestResolveFailPolicy(t *testing.T) {
	t.Parallel()
	catalog, table, _ := newFixture()
	_, _ = table.Resolve(catalog, request(Fail))
	delete(catalog.nodes, "a")
	if _, err := table.Resolve(catalog, request(Fail)); !errors.Is(err, ErrNodeLost) {
		t.Fatalf("err = %v, want ErrNodeLost", err)
	}
}

func TestResolveNoCandidate(t *testing.T) {
	t.Parallel()
	catalog, table, _ := newFixture()
	req := request(Rebind)
	req.Country = "DE"
	if _, err := table.Resolve(catalog, req); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("err = %v, want ErrNoCandidate", err)
	}
}

func TestSweepAndRelease(t *testing.T) {
	t.Parallel()
	catalog, table, now := newFixture()
	_, _ = table.Resolve(catalog, request(Rebind))
	table.Release("t", "s1")
	_, _ = table.Resolve(catalog, request(Rebind))
	*now = now.Add(11 * time.Minute)
	if removed := table.Sweep(); removed != 1 {
		t.Fatalf("Sweep() = %d, want 1", removed)
	}
}
