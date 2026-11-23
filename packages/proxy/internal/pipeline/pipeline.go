// Package pipeline implements the fixed-stage node rule pipeline:
// Normalize -> Enrich -> Predicate -> Score -> Bucket -> Sort -> [Retain] -> Limit.
//
// Every stage is a small interface resolved through an explicit registry.
// Each rule emits traces (hit / miss / modified / excluded / error) so the UI
// can explain why a node was kept, changed, scored or dropped. A rule failure
// only affects the current node and never aborts the whole pipeline.
package pipeline

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	StageNormalize = "normalize"
	StageEnrich    = "enrich"
	StagePredicate = "predicate"
	StageScore     = "score"
	StageBucket    = "bucket"
	StageSort      = "sort"
	StageLimit     = "limit"
)

const (
	OutcomeHit      = "hit"
	OutcomeMiss     = "miss"
	OutcomeModified = "modified"
	OutcomeExcluded = "excluded"
	OutcomeError    = "error"
)

// Node is the pipeline view of a proxy node. It is intentionally decoupled
// from storage records and Mihomo configuration types.
type Node struct {
	ID              string
	Fingerprint     string
	DisplayName     string
	Protocol        string
	LifecycleState  string
	SubscriptionIDs []string
	LatencyMS       *int
	Labels          map[string]string
}

func (n *Node) Label(key string) string {
	if n.Labels == nil {
		return ""
	}
	return n.Labels[key]
}

func (n *Node) SetLabel(key, value string) {
	if n.Labels == nil {
		n.Labels = make(map[string]string, 4)
	}
	n.Labels[key] = value
}

// ScoreBreakdown maps component name to its weighted contribution so a total
// score is always explainable.
type ScoreBreakdown map[string]float64

// NodeContext carries one node through all stages.
type NodeContext struct {
	Node      *Node
	Score     float64
	Breakdown ScoreBreakdown
	Bucket    string
}

// Trace records a single rule decision for one node.
type Trace struct {
	Stage   string `json:"stage"`
	Rule    string `json:"rule"`
	NodeID  string `json:"node_id,omitempty"`
	Outcome string `json:"outcome"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Exclusion explains why a node left the pipeline.
type Exclusion struct {
	NodeID string `json:"node_id"`
	Stage  string `json:"stage"`
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// Result is the ordered pipeline output plus full decision history.
type Result struct {
	Nodes    []*NodeContext
	Excluded []Exclusion
	Traces   []Trace
}

// Stage interfaces. They mirror docs/V1_CORE.md section 4.4.

type Normalizer interface {
	// Normalize mutates the node in place and returns a human readable
	// change description, or "" when nothing changed.
	Normalize(context.Context, *NodeContext) (string, error)
}

type Enricher interface {
	Enrich(context.Context, *NodeContext) error
}

type Predicate interface {
	// Match returns whether the node passes and a reason usable in traces.
	Match(context.Context, *NodeContext) (bool, string, error)
}

type Scorer interface {
	Score(context.Context, *NodeContext) (float64, ScoreBreakdown, error)
}

type Bucketer interface {
	Bucket(context.Context, *NodeContext) (string, error)
}

type namedRule[T any] struct {
	name string
	rule T
}

// Pipeline is an immutable, built pipeline ready to run.
type Pipeline struct {
	normalizers []namedRule[Normalizer]
	enrichers   []namedRule[Enricher]
	predicates  []namedRule[Predicate]
	scorer      namedRule[Scorer]
	bucketer    namedRule[Bucketer]
	sortKeys    []string
	limit       LimitSpec
	retain      *RetainSpec
}

// Run executes all stages. Rule errors are recorded as traces and never abort
// the run: a failing normalizer/enricher/scorer keeps the node with its
// previous values, and a failing predicate keeps the node (fail-open) so a
// broken rule cannot silently empty the data plane configuration.
func (p *Pipeline) Run(ctx context.Context, nodes []*Node) (Result, error) {
	return p.run(ctx, nodes, p.limit)
}

func (p *Pipeline) run(ctx context.Context, nodes []*Node, limit LimitSpec) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result := Result{Nodes: make([]*NodeContext, 0, len(nodes))}
	for _, node := range nodes {
		if node == nil || node.ID == "" {
			continue
		}
		nodeCtx := &NodeContext{Node: node}
		p.runNormalize(ctx, nodeCtx, &result)
		p.runEnrich(ctx, nodeCtx, &result)
		if !p.runPredicates(ctx, nodeCtx, &result) {
			continue
		}
		p.runScore(ctx, nodeCtx, &result)
		p.runBucket(ctx, nodeCtx, &result)
		result.Nodes = append(result.Nodes, nodeCtx)
	}
	sortContexts(result.Nodes, p.sortKeys)
	applyLimit(&result, limit)
	return result, nil
}

func (p *Pipeline) runNormalize(ctx context.Context, nodeCtx *NodeContext, result *Result) {
	for _, rule := range p.normalizers {
		change, err := rule.rule.Normalize(ctx, nodeCtx)
		switch {
		case err != nil:
			result.Traces = append(result.Traces, errorTrace(StageNormalize, rule.name, nodeCtx.Node.ID, err))
		case change != "":
			result.Traces = append(result.Traces, Trace{
				Stage: StageNormalize, Rule: rule.name, NodeID: nodeCtx.Node.ID,
				Outcome: OutcomeModified, Detail: change,
			})
		}
	}
}

func (p *Pipeline) runEnrich(ctx context.Context, nodeCtx *NodeContext, result *Result) {
	for _, rule := range p.enrichers {
		if err := rule.rule.Enrich(ctx, nodeCtx); err != nil {
			result.Traces = append(result.Traces, errorTrace(StageEnrich, rule.name, nodeCtx.Node.ID, err))
		}
	}
}

func (p *Pipeline) runPredicates(ctx context.Context, nodeCtx *NodeContext, result *Result) bool {
	for _, rule := range p.predicates {
		matched, reason, err := rule.rule.Match(ctx, nodeCtx)
		if err != nil {
			result.Traces = append(result.Traces, errorTrace(StagePredicate, rule.name, nodeCtx.Node.ID, err))
			continue
		}
		if matched {
			result.Traces = append(result.Traces, Trace{
				Stage: StagePredicate, Rule: rule.name, NodeID: nodeCtx.Node.ID,
				Outcome: OutcomeHit, Detail: reason,
			})
			continue
		}
		result.Traces = append(result.Traces, Trace{
			Stage: StagePredicate, Rule: rule.name, NodeID: nodeCtx.Node.ID,
			Outcome: OutcomeMiss, Detail: reason,
		})
		result.Excluded = append(result.Excluded, Exclusion{
			NodeID: nodeCtx.Node.ID, Stage: StagePredicate, Rule: rule.name, Reason: reason,
		})
		return false
	}
	return true
}

func (p *Pipeline) runScore(ctx context.Context, nodeCtx *NodeContext, result *Result) {
	if p.scorer.rule == nil {
		return
	}
	score, breakdown, err := p.scorer.rule.Score(ctx, nodeCtx)
	if err != nil {
		result.Traces = append(result.Traces, errorTrace(StageScore, p.scorer.name, nodeCtx.Node.ID, err))
		return
	}
	nodeCtx.Score = score
	nodeCtx.Breakdown = breakdown
	result.Traces = append(result.Traces, Trace{
		Stage: StageScore, Rule: p.scorer.name, NodeID: nodeCtx.Node.ID,
		Outcome: OutcomeHit, Detail: formatBreakdown(score, breakdown),
	})
}

func (p *Pipeline) runBucket(ctx context.Context, nodeCtx *NodeContext, result *Result) {
	if p.bucketer.rule == nil {
		return
	}
	bucket, err := p.bucketer.rule.Bucket(ctx, nodeCtx)
	if err != nil {
		result.Traces = append(result.Traces, errorTrace(StageBucket, p.bucketer.name, nodeCtx.Node.ID, err))
		return
	}
	nodeCtx.Bucket = bucket
}

func applyLimit(result *Result, limit LimitSpec) {
	total := limit.Total
	if limit.PerBucket > 0 {
		counts := make(map[string]int)
		kept := result.Nodes[:0]
		for _, nodeCtx := range result.Nodes {
			counts[nodeCtx.Bucket]++
			if counts[nodeCtx.Bucket] > limit.PerBucket {
				result.Excluded = append(result.Excluded, Exclusion{
					NodeID: nodeCtx.Node.ID, Stage: StageLimit, Rule: "per-bucket-limit",
					Reason: fmt.Sprintf("bucket %q already has %d nodes", nodeCtx.Bucket, limit.PerBucket),
				})
				continue
			}
			kept = append(kept, nodeCtx)
		}
		result.Nodes = kept
	}
	if total > 0 && len(result.Nodes) > total {
		for _, nodeCtx := range result.Nodes[total:] {
			result.Excluded = append(result.Excluded, Exclusion{
				NodeID: nodeCtx.Node.ID, Stage: StageLimit, Rule: "total-limit",
				Reason: fmt.Sprintf("beyond the first %d nodes", total),
			})
		}
		result.Nodes = result.Nodes[:total]
	}
}

// sortContexts applies stable multi-key ordering. Supported keys:
// "score" (descending), "latency" (ascending, missing samples last) and
// "name" (case-insensitive ascending). Node ID is always the final
// tie-breaker so identical inputs produce identical output order.
func sortContexts(items []*NodeContext, keys []string) {
	sort.SliceStable(items, func(left, right int) bool {
		for _, key := range keys {
			switch key {
			case "score":
				if items[left].Score != items[right].Score {
					return items[left].Score > items[right].Score
				}
			case "latency":
				leftLatency, rightLatency := items[left].Node.LatencyMS, items[right].Node.LatencyMS
				if leftLatency == nil && rightLatency == nil {
					continue
				}
				if leftLatency == nil {
					return false
				}
				if rightLatency == nil {
					return true
				}
				if *leftLatency != *rightLatency {
					return *leftLatency < *rightLatency
				}
			case "name":
				leftName := strings.ToLower(items[left].Node.DisplayName)
				rightName := strings.ToLower(items[right].Node.DisplayName)
				if leftName != rightName {
					return leftName < rightName
				}
			}
		}
		return items[left].Node.ID < items[right].Node.ID
	})
}

func errorTrace(stage, rule, nodeID string, err error) Trace {
	return Trace{Stage: stage, Rule: rule, NodeID: nodeID, Outcome: OutcomeError, Detail: err.Error()}
}

func formatBreakdown(score float64, breakdown ScoreBreakdown) string {
	parts := make([]string, 0, len(breakdown))
	for name := range breakdown {
		parts = append(parts, name)
	}
	sort.Strings(parts)
	for index, name := range parts {
		parts[index] = fmt.Sprintf("%s=%.3f", name, breakdown[name])
	}
	return fmt.Sprintf("score=%.3f (%s)", score, strings.Join(parts, " "))
}
