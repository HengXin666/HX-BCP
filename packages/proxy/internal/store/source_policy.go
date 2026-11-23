package store

import (
	"fmt"
	"slices"
	"time"
)

// SourceClass is the explicit operator-declared taxonomy of a node source.
//
// See .agents/notes/implemented/feature/2026-09-26-source-class-drives-probe-policy.md
//
// 20260926 user decision: the probe state machine used to apply one failure
// rule to every source (`consecutive_probe_failures + 1 >= 3`), which is only
// correct for a source whose members are interchangeable. The three classes
// below have materially different replacement costs, so the threshold, the
// retirement rule and the re-probe schedule are per-class values instead of
// constants.
//
// The classification is a property of the SOURCE, not of the node: the same
// endpoint string can be a throwaway pool member in one source and the only
// way out in another. A node that belongs to several sources therefore gets
// the union of their policies (see NodePolicyFor) — the most protective one
// wins, because retiring a node that one source cannot replace is the
// irreversible mistake.
type SourceClass string

const (
	// SourceClassPool is a mass, disposable pool: hundreds to thousands of
	// members, usually free or quota-limited, where a failed member is gone
	// for good and worth nothing more. Replacing one costs nothing, so the
	// policy retires early and stops probing: continuing to probe an exhausted
	// pool member is pure waste.
	SourceClassPool SourceClass = "pool"
	// SourceClassSubscription is a long-lived commercial or curated
	// subscription: tens of members, each with real (paid or hand-built)
	// value. A failure is more likely a transient upstream hiccup than a dead
	// endpoint, so the policy retries with backoff and retires late.
	SourceClassSubscription SourceClass = "subscription"
	// SourceClassResidential is one to a handful of irreplaceable exits held
	// by the operator directly. Losing one disables the whole source, so the
	// policy never retires automatically and never stops probing: a
	// quarantined residential node is "do not route through it right now",
	// never "it is gone".
	SourceClassResidential SourceClass = "residential"
)

// sourceClassOrder fixes the published order of the vocabulary. It is the list
// the API and the capability catalog republish, so a new class cannot ship
// undocumented. This is the display order, not a precedence order.
func sourceClassOrder() []SourceClass {
	return []SourceClass{SourceClassPool, SourceClassSubscription, SourceClassResidential}
}

// SupportedSourceClasses is the closed vocabulary of source classes, as the
// API publishes it.
func SupportedSourceClasses() []string {
	order := sourceClassOrder()
	values := make([]string, 0, len(order))
	for _, class := range order {
		values = append(values, string(class))
	}
	return values
}

// SourceClassPolicy is the per-class half of the probe state machine: the
// thresholds an operator cannot sensibly tune per installation, only per
// source class. The timing knobs that ARE per-installation (check interval,
// probe concurrency, batch size) stay in systemsettings.
type SourceClassPolicy struct {
	// QuarantineAfter is the number of CONSECUTIVE failures that moves a node
	// into `quarantined`. Zero or less means the class never quarantines.
	QuarantineAfter int
	// ProbeWhileQuarantined keeps a quarantined node in the due set so a
	// recovery is noticed. False means a quarantined member is treated as
	// spent and is no longer probed.
	ProbeWhileQuarantined bool
	// RetireAfter is the number of consecutive failures that retires a node
	// outright. Zero or less means the class never retires automatically.
	RetireAfter int
	// BackoffBase and BackoffCap bound the re-probe delay: the delay before
	// the Nth consecutive failure is retried is
	//
	//     BackoffBase * 2^(N-1), capped at BackoffCap
	//
	// so a node that keeps failing is polled exponentially less often. This is
	// what stops the observed "one dead node probed 249 times" behaviour: a
	// 6h cap turns a per-10-minute probe into ~4 probes a day.
	BackoffBase time.Duration
	BackoffCap  time.Duration
}

// ClassPolicies returns the policy for each class. The values are the whole
// point of the classification, so the reasoning for each one is stated here
// rather than in a table elsewhere.
//
// pool — quarantine after 3 consecutive failures, retire at 5, and STOP
// probing once quarantined. A 5000-member free pool of webshare/colo endpoints
// churns constantly; a member that failed three times in a row is dead and the
// next refresh will bring a fresh one. Probing it forever is what produced
// ~250 wasted probe rounds per dead member in production. The backoff is short
// (15m) because the probe cost matters less than noticing a genuine revival
// during the short window before retirement.
//
// subscription — quarantine after 5 consecutive failures and KEEP probing, with
// retirement only after 60 consecutive failures. A paid subscription's node is
// worth money and hand-configuration; a 1-hour upstream blip must not cost it.
// 5 failures at the default 10-minute interval is ~50 minutes of sustained
// failure before it leaves the routing set, which is longer than a typical
// provider-side restart and shorter than an outage an operator would want
// flagged. 60 failures (~10 hours) is the "this subscription node is truly gone
// and holding it only confuses the routing table" point.
//
// residential — never quarantines, never retires, never stops probing, and
// backs off up to 24h. These nodes cannot be replaced: ioa-vm, outlook-ip and
// outlook-resi are single-member sources. Applying the subscription policy to
// them would still eventually retire the only exit an operator has, so the
// class exists precisely to make that impossible. Probing a dead residential
// node is not waste — it is the only way to learn the exit came back — but it
// does back off so a permanently dead one costs a probe a day, not 144.
func ClassPolicies() map[SourceClass]SourceClassPolicy {
	return map[SourceClass]SourceClassPolicy{
		SourceClassPool: {
			QuarantineAfter:       3,
			ProbeWhileQuarantined: false,
			RetireAfter:           5,
			BackoffBase:           15 * time.Minute,
			BackoffCap:            6 * time.Hour,
		},
		SourceClassSubscription: {
			QuarantineAfter:       5,
			ProbeWhileQuarantined: true,
			RetireAfter:           60,
			BackoffBase:           10 * time.Minute,
			BackoffCap:            6 * time.Hour,
		},
		SourceClassResidential: {
			QuarantineAfter:       0,
			ProbeWhileQuarantined: true,
			RetireAfter:           0,
			BackoffBase:           5 * time.Minute,
			BackoffCap:            24 * time.Hour,
		},
	}
}

// DefaultSourceClassPolicy is applied to a source row that predates the
// source_class column, or that carries a value this build does not know.
//
// It is deliberately the PROMOTION-SAFE class, not the cheap one: the cost of
// mistaking a pool member for a subscription member is a few wasted probes,
// while the cost of mistaking an irreplaceable exit for a pool member is
// deleting the operator's only way out. Migration 39 backfills every real row,
// so this fallback only matters for a hand-edited database.
func DefaultSourceClassPolicy() SourceClassPolicy {
	return ClassPolicies()[SourceClassSubscription]
}

// PolicyFor returns the policy for one class. An unknown or empty value falls
// back to DefaultSourceClassPolicy rather than erroring, so a database from a
// newer build cannot stop an older build from starting.
func PolicyFor(class SourceClass) SourceClassPolicy {
	if policy, ok := ClassPolicies()[class]; ok {
		return policy
	}
	return DefaultSourceClassPolicy()
}

// NodePolicyFor returns the effective policy for a node that belongs to the
// given source classes.
//
// The rule is "the most protective class wins", evaluated per dimension, not
// per class: a node shared by a pool and a residential source never quarantines
// (residential wins), and if it were somehow in a pool and a subscription it
// would take the higher failure threshold. Picking a whole class by precedence
// would be wrong — the protective choice differs per dimension.
//
// An empty class list yields the default policy. In production every node has
// exactly one source, so this only matters for a node deliberately shared
// across sources.
func NodePolicyFor(classes []SourceClass) SourceClassPolicy {
	if len(classes) == 0 {
		return DefaultSourceClassPolicy()
	}
	effective := PolicyFor(classes[0])
	for _, class := range classes[1:] {
		candidate := PolicyFor(class)
		effective.QuarantineAfter = moreProtectiveThreshold(effective.QuarantineAfter, candidate.QuarantineAfter)
		effective.RetireAfter = moreProtectiveThreshold(effective.RetireAfter, candidate.RetireAfter)
		effective.ProbeWhileQuarantined = effective.ProbeWhileQuarantined || candidate.ProbeWhileQuarantined
		effective.BackoffBase = maxDuration(effective.BackoffBase, candidate.BackoffBase)
		effective.BackoffCap = maxDuration(effective.BackoffCap, candidate.BackoffCap)
	}
	return effective
}

// moreProtectiveThreshold merges two failure thresholds under the "0 means
// never" convention these policies use.
//
// A plain max would be wrong: 0 encodes "never", which is MORE protective than
// any finite count, yet it is also numerically the smallest. Taking max would
// let a pool's "quarantine at 3" override a residential source's "never",
// which is exactly the combination that must not happen — the shared node would
// be retired even though one of its sources cannot replace it.
func moreProtectiveThreshold(left, right int) int {
	if left == 0 || right == 0 {
		return 0
	}
	return max(left, right)
}

// ProbeBackoff returns how long to wait before probing a node again after the
// given number of consecutive failures. failures is the count AFTER the result
// being recorded, so a first failure (failures = 1) waits BackoffBase.
//
// The delay is applied through nodes.next_probe_at rather than by sleeping, so
// the due-node query — not the probe loop — enforces it. A single slow node
// therefore cannot stall a batch.
func (p SourceClassPolicy) ProbeBackoff(failures int) time.Duration {
	if failures <= 0 || p.BackoffBase <= 0 {
		return 0
	}
	// Shift rather than pow to keep the arithmetic in integers; cap the
	// exponent first so a large failure count cannot overflow.
	exponent := failures - 1
	if exponent > 62 {
		exponent = 62
	}
	delay := p.BackoffBase
	for range exponent {
		if delay >= p.BackoffCap && p.BackoffCap > 0 {
			return p.BackoffCap
		}
		delay *= 2
	}
	if p.BackoffCap > 0 && delay > p.BackoffCap {
		return p.BackoffCap
	}
	return delay
}

// NextProbeAt returns the instant after which a node with the given failure
// count should be probed again, given the time of the check that produced that
// count. A zero failure count returns the check time itself, i.e. "due now".
func (p SourceClassPolicy) NextProbeAt(checkedAt time.Time, failures int) time.Time {
	backoff := p.ProbeBackoff(failures)
	if backoff <= 0 {
		return checkedAt
	}
	return checkedAt.Add(backoff)
}

// ShouldQuarantine reports whether the given consecutive failure count reaches
// the class threshold.
func (p SourceClassPolicy) ShouldQuarantine(failures int) bool {
	return p.QuarantineAfter > 0 && failures >= p.QuarantineAfter
}

// ShouldRetire reports whether the given consecutive failure count reaches the
// class retirement threshold. A class that never retires always returns false.
func (p SourceClassPolicy) ShouldRetire(failures int) bool {
	return p.RetireAfter > 0 && failures >= p.RetireAfter
}

// KeepsQuarantinedProbe answers "do we keep probing this quarantined node".
func (p SourceClassPolicy) KeepsQuarantinedProbe() bool {
	return p.ProbeWhileQuarantined
}

// NormalizeSourceClass maps an empty class onto the default. It is what keeps an
// older internal caller (or a hand-written INSERT) working now that the column
// carries a CHECK constraint: the store guarantees a persistable value, while
// the subscription service is the layer that rejects an operator's typo.
//
// An unknown non-empty value is passed through unchanged on purpose: the
// database CHECK rejects it loudly, which is the correct outcome for a value
// this build does not implement. Silently rewriting it to the default would
// hide the mismatch and apply the wrong failure policy.
func NormalizeSourceClass(value string) string {
	if value == "" {
		return string(SourceClassSubscription)
	}
	return value
}

// ParseSourceClass validates one class value from a request.
func ParseSourceClass(value string) (SourceClass, error) {
	class := SourceClass(value)
	if slices.Contains(sourceClassOrder(), class) {
		return class, nil
	}
	return "", fmt.Errorf("unknown source class %q", value)
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}
