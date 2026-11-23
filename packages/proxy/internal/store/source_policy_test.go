package store

import (
	"testing"
	"time"
)

// TestClassPoliciesEncodeReplacementCost pins the numbers that express the
// whole point of the classification: a source whose members cannot be replaced
// must never be auto-retired, and a source whose members are disposable must
// not be probed forever.
//
// These are not arbitrary constants. Each case below is a property that, if it
// flips, reintroduces the production incident this feature exists to fix.
func TestClassPoliciesEncodeReplacementCost(t *testing.T) {
	t.Parallel()
	policies := ClassPolicies()

	pool := policies[SourceClassPool]
	residential := policies[SourceClassResidential]
	subscription := policies[SourceClassSubscription]

	// The irreplaceable class must never retire or quarantine automatically.
	// ioa-vm / outlook-ip / outlook-resi are single-member sources: retiring
	// one deletes the operator's only exit, with no automatic way back.
	if residential.RetireAfter != 0 {
		t.Errorf("residential RetireAfter = %d, want 0 (never auto-retire)", residential.RetireAfter)
	}
	if residential.QuarantineAfter != 0 {
		t.Errorf("residential QuarantineAfter = %d, want 0 (never auto-quarantine)", residential.QuarantineAfter)
	}
	if !residential.KeepsQuarantinedProbe() {
		t.Error("residential must keep probing: a recovery is the only way it comes back")
	}

	// The disposable class must give up: both thresholds finite and ordered.
	if pool.QuarantineAfter <= 0 || pool.RetireAfter <= pool.QuarantineAfter {
		t.Errorf("pool thresholds out of order: quarantine=%d retire=%d",
			pool.QuarantineAfter, pool.RetireAfter)
	}
	if pool.KeepsQuarantinedProbe() {
		t.Error("pool must stop probing a quarantined member: re-probing an exhausted pool member is the 249-retry waste")
	}

	// Ordering across classes is the actual claim being made: the more
	// replaceable the source, the faster it gives up.
	if !(pool.QuarantineAfter < subscription.QuarantineAfter) {
		t.Errorf("pool must quarantine sooner than subscription: %d vs %d",
			pool.QuarantineAfter, subscription.QuarantineAfter)
	}
	if !(subscription.RetireAfter < 0 || subscription.RetireAfter > pool.RetireAfter) {
		t.Errorf("subscription must retire later than pool: %d vs %d",
			subscription.RetireAfter, pool.RetireAfter)
	}
	if !subscription.KeepsQuarantinedProbe() {
		t.Error("subscription must keep probing: a paid node's failure is often transient")
	}

	// The default must be the promotion-safe class, never the cheap one.
	if got := DefaultSourceClassPolicy(); got.RetireAfter != subscription.RetireAfter {
		t.Errorf("default policy = %+v, want the subscription (safest) policy", got)
	}
}

// TestProbeBackoffIsExponentialAndCapped is the anti-249 regression test. The
// observed failure was a dead node probed ~250 times at the steady interval,
// because nothing backed the retries off. The delay is derived from the failure
// count and must stay bounded.
func TestProbeBackoffIsExponentialAndCapped(t *testing.T) {
	t.Parallel()
	policy := PolicyFor(SourceClassSubscription)

	// First failure waits exactly one base interval, and a zero count (which
	// only a success produces) waits nothing.
	if got := policy.ProbeBackoff(0); got != 0 {
		t.Errorf("ProbeBackoff(0) = %v, want 0", got)
	}
	if got := policy.ProbeBackoff(1); got != policy.BackoffBase {
		t.Errorf("ProbeBackoff(1) = %v, want %v", got, policy.BackoffBase)
	}
	// Strictly increasing until the cap, then pinned at the cap forever. The
	// cap is what bounds total probe volume for a permanently dead node.
	previous := time.Duration(0)
	for failures := 1; failures <= 40; failures++ {
		delay := policy.ProbeBackoff(failures)
		if delay < previous {
			t.Fatalf("ProbeBackoff(%d) = %v decreased from %v", failures, delay, previous)
		}
		if delay > policy.BackoffCap {
			t.Fatalf("ProbeBackoff(%d) = %v exceeds cap %v", failures, delay, policy.BackoffCap)
		}
		previous = delay
	}
	if got := policy.ProbeBackoff(40); got != policy.BackoffCap {
		t.Errorf("ProbeBackoff(40) = %v, want the cap %v", got, policy.BackoffCap)
	}
	// A node at the cap is probed at most ~4 times a day at a 6h cap, versus
	// the ~144 the steady 10-minute interval produced.
	dailyAtCap := time.Duration(24) * time.Hour / policy.BackoffCap
	if dailyAtCap > 8 {
		t.Errorf("capped backoff still probes %v times/day, want few", dailyAtCap)
	}
	// A large count must not overflow into a negative or zero delay.
	if got := policy.ProbeBackoff(100000); got <= 0 || got > policy.BackoffCap {
		t.Errorf("ProbeBackoff(100000) = %v, want a positive delay within the cap", got)
	}
}

// TestNodePolicyForPrefersTheProtectiveDimension pins the multi-source rule.
// A node shared by a pool and an irreplaceable source must not inherit the
// pool's willingness to retire it.
func TestNodePolicyForPrefersTheProtectiveDimension(t *testing.T) {
	t.Parallel()

	if got := NodePolicyFor(nil); got.RetireAfter != DefaultSourceClassPolicy().RetireAfter {
		t.Errorf("NodePolicyFor(nil) = %+v, want the default policy", got)
	}

	shared := NodePolicyFor([]SourceClass{SourceClassPool, SourceClassResidential})
	if shared.RetireAfter != 0 {
		t.Errorf("pool+residential RetireAfter = %d, want 0: residential cannot be replaced", shared.RetireAfter)
	}
	if shared.QuarantineAfter != 0 {
		t.Errorf("pool+residential QuarantineAfter = %d, want 0", shared.QuarantineAfter)
	}
	if !shared.KeepsQuarantinedProbe() {
		t.Error("pool+residential must keep probing: the residential member may recover")
	}

	mixed := NodePolicyFor([]SourceClass{SourceClassPool, SourceClassSubscription})
	pool := PolicyFor(SourceClassPool)
	if mixed.QuarantineAfter != PolicyFor(SourceClassSubscription).QuarantineAfter {
		t.Errorf("pool+subscription QuarantineAfter = %d, want the more patient value", mixed.QuarantineAfter)
	}
	if mixed.QuarantineAfter <= pool.QuarantineAfter {
		t.Error("a shared node must not quarantine at the pool threshold")
	}
}

// TestShouldQuarantineAndRetire covers the boundary semantics, including the
// "0 means never" convention that makes residential safe.
func TestShouldQuarantineAndRetire(t *testing.T) {
	t.Parallel()

	residential := PolicyFor(SourceClassResidential)
	for _, failures := range []int{0, 1, 3, 100, 1_000_000} {
		if residential.ShouldQuarantine(failures) {
			t.Errorf("residential ShouldQuarantine(%d) = true, want false always", failures)
		}
		if residential.ShouldRetire(failures) {
			t.Errorf("residential ShouldRetire(%d) = true, want false always", failures)
		}
	}

	pool := PolicyFor(SourceClassPool)
	if pool.ShouldQuarantine(pool.QuarantineAfter - 1) {
		t.Error("pool quarantined one failure early")
	}
	if !pool.ShouldQuarantine(pool.QuarantineAfter) {
		t.Error("pool did not quarantine at its threshold")
	}
	if pool.ShouldRetire(pool.RetireAfter - 1) {
		t.Error("pool retired one failure early")
	}
	if !pool.ShouldRetire(pool.RetireAfter) {
		t.Error("pool did not retire at its threshold")
	}
}

func TestParseSourceClassRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"pool", "subscription", "residential"} {
		parsed, err := ParseSourceClass(valid)
		if err != nil || string(parsed) != valid {
			t.Errorf("ParseSourceClass(%q) = (%q, %v), want (%q, nil)", valid, parsed, err, valid)
		}
	}
	for _, invalid := range []string{"", "POOL", "residental", "default", "0"} {
		if _, err := ParseSourceClass(invalid); err == nil {
			t.Errorf("ParseSourceClass(%q) accepted an unknown class", invalid)
		}
	}
}

// TestSupportedSourceClassesMatchesPolicyMap keeps the published vocabulary and
// the policy table from drifting: a class an operator can set must have a
// policy, and a class with a policy must be settable.
func TestSupportedSourceClassesMatchesPolicyMap(t *testing.T) {
	t.Parallel()

	supported := SupportedSourceClasses()
	if len(supported) != 3 {
		t.Fatalf("SupportedSourceClasses() = %v, want three classes", supported)
	}
	policies := ClassPolicies()
	for _, value := range supported {
		if _, exists := policies[SourceClass(value)]; !exists {
			t.Errorf("published class %q has no policy", value)
		}
	}
	for class := range policies {
		found := false
		for _, value := range supported {
			if SourceClass(value) == class {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("policy for %q is not published in SupportedSourceClasses()", class)
		}
	}
}

// TestNormalizeSourceClassFillsOnlyTheEmptyValue documents the deliberate
// asymmetry: an empty value takes the default, an unknown one is passed
// through so the database CHECK rejects it loudly instead of silently applying
// the wrong failure policy.
func TestNormalizeSourceClassFillsOnlyTheEmptyValue(t *testing.T) {
	t.Parallel()

	if got := NormalizeSourceClass(""); got != string(SourceClassSubscription) {
		t.Errorf("NormalizeSourceClass(\"\") = %q, want the default", got)
	}
	if got := NormalizeSourceClass("pool"); got != "pool" {
		t.Errorf("NormalizeSourceClass(pool) = %q, want pool", got)
	}
	if got := NormalizeSourceClass("nonsense"); got != "nonsense" {
		t.Errorf("NormalizeSourceClass(nonsense) = %q, want it passed through to fail the CHECK", got)
	}
}
