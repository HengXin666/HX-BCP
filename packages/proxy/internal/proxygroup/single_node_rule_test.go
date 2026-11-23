package proxygroup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

// singleNodeHarness is a service over a real store with a handful of seeded
// nodes, so the rule is exercised through the same path an API caller takes:
// the check lives inside normalize, after node existence validation.
type singleNodeHarness struct {
	service *Service
	store   *store.Store
	ctx     context.Context
	nodes   []string
}

// candidateRepository lets a test control what a subscription resolves to
// without building a full snapshot. Only ListGroupNodeCandidates is overridden;
// every other store method is promoted from the real store.
type candidateRepository struct {
	*store.Store
	candidates []store.GroupNodeCandidate
}

func (r candidateRepository) ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error) {
	return r.candidates, nil
}

func newSingleNodeHarness(t *testing.T, nodeCount int) *singleNodeHarness {
	t.Helper()
	return newSingleNodeHarnessWithCandidates(t, nodeCount, nil)
}

func newSingleNodeHarnessWithCandidates(t *testing.T, nodeCount int, candidates []store.GroupNodeCandidate) *singleNodeHarness {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service, err := NewService(candidateRepository{Store: database, candidates: candidates}, &testReconciler{})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, nodeCount)
	for index := 0; index < nodeCount; index++ {
		id := "node-" + string(rune('a'+index))
		if _, err := database.UpsertResidentialSessionNode(ctx, "channel-1", store.ResidentialSessionNode{
			ID: id, Fingerprint: "fingerprint-" + id, DisplayName: "node " + id,
			Protocol: "http", CanonicalConfigEncrypted: []byte{0x01},
		}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return &singleNodeHarness{service: service, store: database, ctx: ctx, nodes: ids}
}

// TestCreateRejectsOneNodeGroup is the rule itself. The message has to name the
// remedy, not just the violation: whoever hits this is about to write the
// workaround by hand, and "invalid source_spec" would send it looking for a
// different field.
func TestCreateRejectsOneNodeGroup(t *testing.T) {
	harness := newSingleNodeHarness(t, 2)

	_, err := harness.service.Create(harness.ctx, CreateRequest{
		Name:       "solo-hk",
		Strategy:   "manual",
		SourceSpec: SourceSpec{NodeIDs: harness.nodes[:1]},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
	for _, want := range []string{"代理组节点必须 > 1", "listener.node_id", "禁止"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Create() error = %v, want it to mention %q", err, want)
		}
	}
}

// TestCreateRejectsOneNodeGroupAfterDeduplication closes the obvious loophole:
// the same node listed twice is still one member, and deduplication happens
// before the count is taken.
func TestCreateRejectsOneNodeGroupAfterDeduplication(t *testing.T) {
	harness := newSingleNodeHarness(t, 1)

	_, err := harness.service.Create(harness.ctx, CreateRequest{
		Name:       "solo-hk-twice",
		Strategy:   "manual",
		SourceSpec: SourceSpec{NodeIDs: []string{harness.nodes[0], harness.nodes[0]}},
	})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "代理组节点必须 > 1") {
		t.Fatalf("Create() error = %v, want the one-node rejection", err)
	}
}

// TestCreateRejectsResolvedSingleMemberShapes closes the paths that survived
// the static node_ids check: allow_empty did not make a one-node group dormant,
// a subscription can currently hold one node, and limit=1 over a pool asks for
// exactly one member. All three are measured 2026-10-18 against the running
// service, which is why the criterion is the resolved member count.
func TestCreateRejectsResolvedSingleMemberShapes(t *testing.T) {
	harness := newSingleNodeHarnessWithCandidates(t, 3, []store.GroupNodeCandidate{
		candidateNode("node-a", "sub-1", "HK 01"),
		candidateNode("node-b", "sub-1", "HK 02"),
		candidateNode("node-c", "sub-2", "HK 03"),
	})
	for _, id := range []string{"sub-1", "sub-2"} {
		if _, err := harness.store.CreateSubscription(harness.ctx, store.SubscriptionRecord{
			ID: id, Name: id, SourceType: "inline", SourceClass: "pool",
			SourceConfigEncrypted: []byte{0x01}, Enabled: true, Version: 1,
			RefreshIntervalSeconds: 3600,
			CreatedAt:              time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		spec SourceSpec
	}{
		{name: "allow-empty-plus-one-node", spec: SourceSpec{NodeIDs: harness.nodes[:1], AllowEmpty: true}},
		{name: "subscription-with-one-node", spec: SourceSpec{SubscriptionIDs: []string{"sub-2"}}},
		{name: "subscription-limit-one", spec: SourceSpec{SubscriptionIDs: []string{"sub-1"}, Limit: 1}},
		{name: "subscription-filtered-to-one", spec: SourceSpec{SubscriptionIDs: []string{"sub-1"}, NameKeywords: []string{"HK 01"}}},
	}
	for _, testCase := range cases {
		_, err := harness.service.Create(harness.ctx, CreateRequest{
			Name: "solo-" + testCase.name, Strategy: "manual", SourceSpec: testCase.spec,
		})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "代理组节点必须 > 1") {
			t.Fatalf("%s: Create() error = %v, want the one-member rejection", testCase.name, err)
		}
	}
	records, err := harness.store.ListProxyGroups(harness.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("rejected creates left %d group(s) behind", len(records))
	}
}

// TestCreateAllowsResolvedMultiMemberShapes is the negative control for the
// resolved count: a two-node subscription, a one-member subscription plus an
// explicit node, and an unrefreshed subscription must all stay creatable.
func TestCreateAllowsResolvedMultiMemberShapes(t *testing.T) {
	harness := newSingleNodeHarnessWithCandidates(t, 3, []store.GroupNodeCandidate{
		candidateNode("node-a", "sub-1", "HK 01"),
		candidateNode("node-b", "sub-1", "HK 02"),
	})
	for _, id := range []string{"sub-1", "sub-empty"} {
		if _, err := harness.store.CreateSubscription(harness.ctx, store.SubscriptionRecord{
			ID: id, Name: id, SourceType: "inline", SourceClass: "pool",
			SourceConfigEncrypted: []byte{0x01}, Enabled: true, Version: 1,
			RefreshIntervalSeconds: 3600,
			CreatedAt:              time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		spec SourceSpec
	}{
		{name: "two-node-subscription", spec: SourceSpec{SubscriptionIDs: []string{"sub-1"}}},
		{name: "node-plus-two-node-subscription", spec: SourceSpec{NodeIDs: harness.nodes[:1], SubscriptionIDs: []string{"sub-1"}}},
		{name: "unrefreshed-subscription", spec: SourceSpec{SubscriptionIDs: []string{"sub-empty"}}},
	}
	for _, testCase := range cases {
		if _, err := harness.service.Create(harness.ctx, CreateRequest{
			Name: "pool-" + testCase.name, Strategy: "manual", SourceSpec: testCase.spec,
		}); err != nil {
			t.Fatalf("%s: Create() error = %v, want success", testCase.name, err)
		}
	}
}

func candidateNode(id, subscriptionID, displayName string) store.GroupNodeCandidate {
	return store.GroupNodeCandidate{
		NodeConfigRecord: store.NodeConfigRecord{
			ID: id, Fingerprint: "fingerprint-" + id, DisplayName: displayName,
			Protocol: "http", LifecycleState: "healthy",
			CanonicalConfigEncrypted: []byte{0x01},
		},
		SubscriptionIDs: []string{subscriptionID},
	}
}

// TestCreateAcceptsEveryOtherSingleSourceShape is the negative control. Without
// it, "reject everything" would pass the test above. Each case is a shape that
// is genuinely not a node handle.
func TestCreateAcceptsEveryOtherSingleSourceShape(t *testing.T) {
	harness := newSingleNodeHarness(t, 2)
	if _, err := harness.store.CreateSubscription(harness.ctx, store.SubscriptionRecord{
		ID: "sub-1", Name: "sub-1", SourceType: "inline", SourceClass: "pool",
		SourceConfigEncrypted: []byte{0x01}, Enabled: true, Version: 1,
		RefreshIntervalSeconds: 3600,
		CreatedAt:              time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	anchor, err := harness.service.Create(harness.ctx, CreateRequest{
		Name: "anchor", Strategy: "manual", SourceSpec: SourceSpec{NodeIDs: harness.nodes},
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		spec   SourceSpec
		reason string
	}{
		{name: "two-nodes", spec: SourceSpec{NodeIDs: harness.nodes}, reason: "the group is a real selection"},
		{name: "node-plus-direct", spec: SourceSpec{NodeIDs: harness.nodes[:1], IncludeDirect: true}, reason: "DIRECT is a second member"},
		{name: "direct-only", spec: SourceSpec{IncludeDirect: true}, reason: "server direct is a real single-member policy and the anchor for pinned listeners"},
		{name: "dormant", spec: SourceSpec{AllowEmpty: true}, reason: "zero members until it fills in later"},
		{name: "unrefreshed-subscription", spec: SourceSpec{SubscriptionIDs: []string{"sub-1"}}, reason: "zero resolved members until the snapshot arrives"},
		{name: "single-group-composition", spec: SourceSpec{GroupIDs: []string{anchor.ID}}, reason: "a composition of one group is a policy reference, not a node handle"},
	}
	for _, testCase := range cases {
		_, err := harness.service.Create(harness.ctx, CreateRequest{
			Name:       "group-" + testCase.name,
			Strategy:   "manual",
			SourceSpec: testCase.spec,
		})
		if err != nil {
			t.Fatalf("%s: Create() error = %v, want success (%s)", testCase.name, err, testCase.reason)
		}
	}
}

// TestCreateAllowsSingleNodeForManagedGroups pins the escape hatch used by
// residential channels: their member list is a pool of vendor sessions, and a
// passthrough channel legitimately materialises exactly one.
func TestCreateAllowsSingleNodeForManagedGroups(t *testing.T) {
	harness := newSingleNodeHarness(t, 1)

	if _, err := harness.service.Create(harness.ctx, CreateRequest{
		Name:            "residential-channel",
		Strategy:        "manual",
		SourceSpec:      SourceSpec{NodeIDs: harness.nodes},
		AllowSingleNode: true,
	}); err != nil {
		t.Fatalf("Create() with AllowSingleNode error = %v, want success", err)
	}
}

// TestUpdateCannotShrinkAGroupToOneNode stops the rule from being a two-call
// suggestion: build a legal two-node group, then edit it down to one.
func TestUpdateCannotShrinkAGroupToOneNode(t *testing.T) {
	harness := newSingleNodeHarness(t, 2)
	created, err := harness.service.Create(harness.ctx, CreateRequest{
		Name: "pool", Strategy: "manual", SourceSpec: SourceSpec{NodeIDs: harness.nodes},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = harness.service.Update(harness.ctx, created.ID, UpdateRequest{
		Version: created.Version, Name: created.Name, Strategy: created.Strategy,
		SourceSpec: SourceSpec{NodeIDs: harness.nodes[:1]}, Enabled: true,
	})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "代理组节点必须 > 1") {
		t.Fatalf("Update() error = %v, want the one-node rejection", err)
	}
}

// TestUpdateStillRenamesALegacyOneNodeGroup is the compatibility boundary. Rows
// that already have the shape must stay editable, or the operator cannot even
// rename or disable them to converge; only *reaching* the shape is refused.
func TestUpdateStillRenamesALegacyOneNodeGroup(t *testing.T) {
	harness := newSingleNodeHarness(t, 1)
	legacy, err := harness.service.Create(harness.ctx, CreateRequest{
		Name: "legacy-solo", Strategy: "manual",
		SourceSpec: SourceSpec{NodeIDs: harness.nodes}, AllowSingleNode: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	renamed, err := harness.service.Update(harness.ctx, legacy.ID, UpdateRequest{
		Version: legacy.Version, Name: "legacy-solo-renamed", Strategy: "manual",
		SourceSpec: SourceSpec{NodeIDs: harness.nodes}, Enabled: false,
	})
	if err != nil {
		t.Fatalf("Update() of an existing one-node group error = %v, want success", err)
	}
	if renamed.Name != "legacy-solo-renamed" || renamed.Enabled {
		t.Fatalf("Update() = %+v, want the rename and disable applied", renamed)
	}
}

// TestUpdateStillRenamesALegacyOneMemberSubscriptionGroup covers the legacy row
// whose one member comes from a subscription that currently holds one node.
// The stored shape is already one member; freezing it would make the row
// impossible to rename or disable, so the exemption is based on the stored
// shape, not on the candidate carrying an explicit node_ids list.
func TestUpdateStillRenamesALegacyOneMemberSubscriptionGroup(t *testing.T) {
	harness := newSingleNodeHarnessWithCandidates(t, 1, []store.GroupNodeCandidate{
		candidateNode("node-a", "sub-1", "HK 01"),
	})
	if _, err := harness.store.CreateSubscription(harness.ctx, store.SubscriptionRecord{
		ID: "sub-1", Name: "sub-1", SourceType: "inline", SourceClass: "pool",
		SourceConfigEncrypted: []byte{0x01}, Enabled: true, Version: 1,
		RefreshIntervalSeconds: 3600,
		CreatedAt:              time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := harness.service.Create(harness.ctx, CreateRequest{
		Name: "legacy-sub-solo", Strategy: "manual",
		SourceSpec:      SourceSpec{SubscriptionIDs: []string{"sub-1"}},
		AllowSingleNode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := harness.service.Update(harness.ctx, legacy.ID, UpdateRequest{
		Version: legacy.Version, Name: "legacy-sub-solo-renamed", Strategy: "manual",
		SourceSpec: SourceSpec{SubscriptionIDs: []string{"sub-1"}}, Enabled: false,
	})
	if err != nil {
		t.Fatalf("Update() of an existing one-member subscription group error = %v, want success", err)
	}
	if renamed.Name != "legacy-sub-solo-renamed" || renamed.Enabled {
		t.Fatalf("Update() = %+v, want the rename and disable applied", renamed)
	}
}

// TestMisplacedReferencesAreNamedAsCategoryErrors pins the diagnosis an agent
// actually needs. Measured 2026-10-18: a group id in node_ids was answered with
// "a group needs more than one node", whose remedy (add another node) makes the
// mistake worse. The ids carry their kind in the prefix, so the error can say
// which field the value belongs in.
func TestMisplacedReferencesAreNamedAsCategoryErrors(t *testing.T) {
	harness := newSingleNodeHarness(t, 2)

	cases := []struct {
		name    string
		spec    SourceSpec
		want    []string
		mustNot string
	}{
		{
			name:    "group id in node_ids",
			spec:    SourceSpec{NodeIDs: []string{"group-abc123"}},
			want:    []string{"group-abc123", "group_ids", "代理组", "节点"},
			mustNot: "代理组节点必须 > 1",
		},
		{
			name:    "node id in group_ids",
			spec:    SourceSpec{NodeIDs: harness.nodes, GroupIDs: []string{harness.nodes[0]}},
			want:    []string{harness.nodes[0], "node_ids"},
			mustNot: "",
		},
		{
			name:    "node id in subscription_ids",
			spec:    SourceSpec{SubscriptionIDs: []string{harness.nodes[0]}},
			want:    []string{harness.nodes[0], "subscription_ids"},
			mustNot: "",
		},
		{
			name:    "subscription id in node_ids",
			spec:    SourceSpec{NodeIDs: []string{"sub-abc123"}},
			want:    []string{"sub-abc123", "subscription_ids", "订阅"},
			mustNot: "代理组节点必须 > 1",
		},
	}
	for _, testCase := range cases {
		_, err := harness.service.Create(harness.ctx, CreateRequest{
			Name:     "mixed-up-" + strings.ReplaceAll(testCase.name, " ", "-"),
			Strategy: "manual", SourceSpec: testCase.spec,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: error = %v, want ErrInvalid", testCase.name, err)
		}
		for _, want := range testCase.want {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: error = %v, want it to mention %q", testCase.name, err, want)
			}
		}
		if testCase.mustNot != "" && strings.Contains(err.Error(), testCase.mustNot) {
			t.Fatalf("%s: error = %v, must not be reported as a size problem", testCase.name, err)
		}
	}
}

// TestUnknownNodeIDNamesTheID is the other half of the same diagnosis: an id
// with a valid prefix but no row behind it. "one or more nodes do not exist"
// leaves the caller unable to tell which of its ids was wrong.
func TestUnknownNodeIDNamesTheID(t *testing.T) {
	harness := newSingleNodeHarness(t, 2)

	_, err := harness.service.Create(harness.ctx, CreateRequest{
		Name: "stale", Strategy: "manual",
		SourceSpec: SourceSpec{NodeIDs: []string{harness.nodes[0], "node-does-not-exist"}},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "node-does-not-exist") {
		t.Fatalf("Create() error = %v, want it to name the missing id", err)
		if strings.Contains(err.Error(), "代理组节点必须 > 1") {
			t.Fatalf("Create() error = %v, must not answer a missing node with the size rule", err)
		}
	}
}
