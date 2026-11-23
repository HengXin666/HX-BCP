package pipeline

import (
	"context"
	"fmt"
	"math"
	"time"
)

// StageRetain sits between Sort and Limit. It exists because re-ranking a
// large pool on every probe round reshuffles a limited group's members on
// every apply: a node 1 ms faster than an incumbent evicts it, consumers lose
// their sticky exit, and the data plane reloads for no real gain. Retain keeps
// the previous members that still pass every predicate and are not
// quarantined, and only lets a newcomer take a seat when it is better by more
// than Margin and the incumbent has held the seat for at least MinDwell.
//
// See .agents/notes/implemented/feature/2026-10-16-group-member-retain.md.
const StageRetain = "retain"

// RetainSpec configures the Retain stage. It only has an effect together with
// a total limit: without a limit every passing node is a member anyway.
type RetainSpec struct {
	// Margin is how much better a newcomer must be, in the unit of the first
	// sort key: score points for "score", milliseconds for "latency". "name"
	// ordering has no notion of better, so incumbents simply keep their seat.
	Margin float64 `json:"margin,omitempty"`
	// MinDwellSeconds protects a member from replacement until it has been a
	// member this long, however much better the newcomer is.
	MinDwellSeconds int `json:"min_dwell_seconds,omitempty"`
}

// MaxRetainDwell bounds MinDwellSeconds so a typo cannot freeze a group.
const MaxRetainDwell = 7 * 24 * time.Hour

// Incumbents describes the previous membership of a group: node fingerprint ->
// time it joined. Fingerprints, not node ids, are the identity because a
// subscription refresh may re-create the row of the same physical node.
type Incumbents struct {
	JoinedAt map[string]time.Time
	Now      time.Time
}

// Validate rejects values that cannot be meant.
func (r RetainSpec) Validate() error {
	if r.Margin < 0 || math.IsNaN(r.Margin) || math.IsInf(r.Margin, 0) {
		return fmt.Errorf("retain margin must be a finite number >= 0")
	}
	// Compare in seconds: multiplying first can overflow and wrap negative.
	if r.MinDwellSeconds < 0 || int64(r.MinDwellSeconds) > int64(MaxRetainDwell/time.Second) {
		return fmt.Errorf("retain min_dwell_seconds must be between 0 and %d", int(MaxRetainDwell/time.Second))
	}
	return nil
}

// RunRetaining is Run with the previous membership available to the Retain
// stage. A pipeline without a Retain spec, or with no total limit, behaves
// exactly like Run.
func (p *Pipeline) RunRetaining(ctx context.Context, nodes []*Node, incumbents Incumbents) (Result, error) {
	if p.retain == nil || p.limit.Total <= 0 || len(incumbents.JoinedAt) == 0 {
		return p.Run(ctx, nodes)
	}
	// Run every stage except Limit (both the total and the per-bucket cap),
	// then let Retain choose the seats under the same caps. Applying the
	// per-bucket cap first would drop an incumbent before Retain could keep
	// it. The pipeline itself is not mutated.
	result, err := p.run(ctx, nodes, LimitSpec{})
	if err != nil {
		return result, err
	}
	p.applyRetain(&result, incumbents)
	return result, nil
}

type retainSeat struct {
	ctx       *NodeContext
	incumbent bool
	protected bool
	chosen    bool
}

// applyRetain picks at most limit.Total seats, at most limit.PerBucket per
// bucket. Cost is O(n log n + swaps * seats): challengers are visited best
// first and the scan stops at the first one that cannot beat the weakest
// replaceable incumbent by the margin, because every later challenger ranks
// no better on the first sort key.
func (p *Pipeline) applyRetain(result *Result, incumbents Incumbents) {
	seats, perBucket := p.limit.Total, p.limit.PerBucket
	metric := retainMetric(p.sortKeys)
	dwell := time.Duration(p.retain.MinDwellSeconds) * time.Second
	all := make([]*retainSeat, 0, len(result.Nodes))
	for _, nodeCtx := range result.Nodes {
		joined, was := incumbents.JoinedAt[nodeCtx.Node.Fingerprint]
		incumbent := was && nodeCtx.Node.Fingerprint != "" && retainable(nodeCtx.Node.LifecycleState)
		protected := incumbent && dwell > 0 && incumbents.Now.Sub(joined) < dwell
		all = append(all, &retainSeat{ctx: nodeCtx, incumbent: incumbent, protected: protected})
	}
	used, buckets := 0, map[string]int{}
	fits := func(item *retainSeat) bool {
		return perBucket <= 0 || buckets[item.ctx.Bucket] < perBucket
	}
	take := func(item *retainSeat) {
		item.chosen = true
		used++
		buckets[item.ctx.Bucket]++
	}
	// Incumbents first (best ranked first), then the best newcomers.
	for _, pass := range []bool{true, false} {
		for _, item := range all {
			if used >= seats {
				break
			}
			if !item.chosen && item.incumbent == pass && fits(item) {
				take(item)
			}
		}
	}
	if metric != nil {
		// beats is written positively so a NaN difference (both metrics -Inf
		// for nodes without a latency sample, or a degenerate score) never
		// counts as better.
		beats := func(challenger, victim *retainSeat) bool {
			return metric(challenger.ctx)-metric(victim.ctx) > p.retain.Margin
		}
		// weakest returns the lowest ranked replaceable incumbent. With
		// bucket set it only considers that bucket: a challenger whose bucket
		// is full can only take a seat from its own bucket.
		weakest := func(bucket *string) *retainSeat {
			var found *retainSeat
			for _, item := range all {
				if item.chosen && item.incumbent && !item.protected && (bucket == nil || item.ctx.Bucket == *bucket) {
					found = item // all is rank ordered, so the last one is the weakest
				}
			}
			return found
		}
		for _, challenger := range all {
			if challenger.chosen || challenger.incumbent {
				continue
			}
			global := weakest(nil)
			if global == nil || !beats(challenger, global) {
				// No later challenger ranks better on the first sort key, and
				// every constrained victim ranks no worse than the global one.
				break
			}
			victim := global
			if !fits(challenger) && challenger.ctx.Bucket != global.ctx.Bucket {
				bucket := challenger.ctx.Bucket
				victim = weakest(&bucket)
				if victim == nil || !beats(challenger, victim) {
					continue
				}
			}
			victim.chosen = false
			used--
			buckets[victim.ctx.Bucket]--
			take(challenger)
			result.Traces = append(result.Traces, Trace{
				Stage: StageRetain, Rule: "retain", NodeID: challenger.ctx.Node.ID, Outcome: OutcomeHit,
				Detail: fmt.Sprintf("replaced %s: better by more than %.3f", victim.ctx.Node.ID, p.retain.Margin),
			})
		}
	}
	kept := make([]*NodeContext, 0, seats)
	for _, item := range all {
		if item.chosen {
			kept = append(kept, item.ctx)
			if item.incumbent {
				detail := "kept: previous member still passes every predicate"
				if item.protected {
					detail = "kept: within min dwell"
				}
				result.Traces = append(result.Traces, Trace{Stage: StageRetain, Rule: "retain", NodeID: item.ctx.Node.ID, Outcome: OutcomeHit, Detail: detail})
			}
			continue
		}
		reason := fmt.Sprintf("beyond the first %d nodes", seats)
		switch {
		case perBucket > 0 && buckets[item.ctx.Bucket] >= perBucket:
			reason = fmt.Sprintf("bucket %q already has %d nodes", item.ctx.Bucket, perBucket)
		case item.incumbent:
			reason = fmt.Sprintf("replaced by a node better by more than %.3f", p.retain.Margin)
		case metric != nil:
			reason = fmt.Sprintf("seats held by previous members (margin %.3f)", p.retain.Margin)
		}
		result.Excluded = append(result.Excluded, Exclusion{NodeID: item.ctx.Node.ID, Stage: StageRetain, Rule: "retain", Reason: reason})
	}
	result.Nodes = kept
}

// retainMetric returns "higher is better" for the first meaningful sort key,
// or nil when the order has no notion of better (name).
func retainMetric(keys []string) func(*NodeContext) float64 {
	for _, key := range keys {
		switch key {
		case "score":
			return func(item *NodeContext) float64 { return item.Score }
		case "latency":
			return func(item *NodeContext) float64 {
				if item.Node.LatencyMS == nil {
					return math.Inf(-1)
				}
				return -float64(*item.Node.LatencyMS)
			}
		case "name":
			return nil
		}
	}
	return nil
}

// retainable reports whether a previous member may keep its seat. A
// quarantined, disabled or retired node never does, even if a custom predicate
// let it through: retention is a tie-breaker, not a health override.
func retainable(state string) bool {
	switch state {
	case "quarantined", "disabled", "retired":
		return false
	default:
		return true
	}
}
