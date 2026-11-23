package proxygroup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/HengXin666/hx-pxy/internal/pipeline"
	"github.com/HengXin666/hx-pxy/internal/store"
)

var defaultRegistry = pipeline.DefaultRegistry()

// DecodeSourceSpec parses a stored source_spec_json value.
func DecodeSourceSpec(raw string) (SourceSpec, error) {
	var spec SourceSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return SourceSpec{}, fmt.Errorf("decode source spec: %w", err)
	}
	return spec, nil
}

// Resolution is the explainable output of member resolution.
type Resolution struct {
	NodeIDs  []string
	Excluded []pipeline.Exclusion
	Traces   []pipeline.Trace
}

// ResolveNodeIDs resolves group members without traces. It keeps the
// historical behavior: explicitly pinned node IDs always come first and skip
// filtering; dynamic selection only runs when subscriptions are referenced.
func ResolveNodeIDs(spec SourceSpec, candidates []store.GroupNodeCandidate) []string {
	return ResolveNodeIDsRetaining(spec, candidates, pipeline.Incumbents{})
}

// ResolveNodeIDsRetaining is ResolveNodeIDs with the group's previous
// membership available to the Retain stage (spec.Retain). Without incumbents
// or without a retain spec it is identical to ResolveNodeIDs.
func ResolveNodeIDsRetaining(spec SourceSpec, candidates []store.GroupNodeCandidate, incumbents pipeline.Incumbents) []string {
	resolution, err := resolveMembers(spec, "", candidates, incumbents)
	if err != nil {
		// A SourceSpec already validated by the service cannot produce an
		// invalid derived pipeline; keep pinned nodes if it somehow does.
		return deduplicateIDs(spec.NodeIDs)
	}
	return resolution.NodeIDs
}

// ResolveMembers resolves group members through the rule pipeline and returns
// the full trace so the UI can explain every inclusion and exclusion.
// rulePipelineJSON optionally extends the pipeline derived from the source
// spec with stored custom rules (proxy_groups.rule_pipeline_json).
func ResolveMembers(spec SourceSpec, rulePipelineJSON string, candidates []store.GroupNodeCandidate) (Resolution, error) {
	return resolveMembers(spec, rulePipelineJSON, candidates, pipeline.Incumbents{})
}

func resolveMembers(spec SourceSpec, rulePipelineJSON string, candidates []store.GroupNodeCandidate, incumbents pipeline.Incumbents) (Resolution, error) {
	resolved := deduplicateIDs(spec.NodeIDs)
	if len(spec.SubscriptionIDs) == 0 {
		return Resolution{NodeIDs: resolved}, nil
	}
	pipelineSpec, err := derivePipelineSpec(spec, rulePipelineJSON)
	if err != nil {
		return Resolution{}, err
	}
	built, err := defaultRegistry.Build(pipelineSpec)
	if err != nil {
		return Resolution{}, fmt.Errorf("build group pipeline: %w", err)
	}
	nodes := make([]*pipeline.Node, 0, len(candidates))
	for _, candidate := range candidates {
		nodes = append(nodes, candidateToNode(candidate))
	}
	result, err := built.RunRetaining(context.Background(), nodes, incumbents)
	if err != nil {
		return Resolution{}, err
	}
	seen := make(map[string]struct{}, len(resolved))
	for _, id := range resolved {
		seen[id] = struct{}{}
	}
	for _, nodeCtx := range result.Nodes {
		if _, exists := seen[nodeCtx.Node.ID]; exists {
			continue
		}
		seen[nodeCtx.Node.ID] = struct{}{}
		resolved = append(resolved, nodeCtx.Node.ID)
	}
	return Resolution{NodeIDs: resolved, Excluded: result.Excluded, Traces: result.Traces}, nil
}

// derivePipelineSpec translates the structured SourceSpec filters into
// pipeline rules, then merges optional stored custom rules on top.
func derivePipelineSpec(spec SourceSpec, rulePipelineJSON string) (pipeline.Spec, error) {
	derived := pipeline.Spec{
		Enrich: []pipeline.RuleSpec{{Use: "name-region"}},
		Predicates: []pipeline.RuleSpec{
			mustRule("subscriptions", map[string]any{"values": spec.SubscriptionIDs}),
		},
		Limit:  pipeline.LimitSpec{Total: spec.Limit},
		Retain: spec.Retain,
	}
	if len(spec.Protocols) > 0 {
		derived.Predicates = append(derived.Predicates, mustRule("protocols", map[string]any{"values": spec.Protocols}))
	}
	if len(spec.States) > 0 {
		derived.Predicates = append(derived.Predicates, mustRule("states", map[string]any{"values": spec.States}))
	}
	if len(spec.NameKeywords) > 0 {
		derived.Predicates = append(derived.Predicates, mustRule("name-keywords", map[string]any{"values": spec.NameKeywords}))
	}
	if len(spec.Regions) > 0 {
		derived.Predicates = append(derived.Predicates, mustRule("regions", map[string]any{"values": spec.Regions}))
	}
	if spec.MaxLatencyMS > 0 {
		derived.Predicates = append(derived.Predicates, mustRule("max-latency", map[string]any{"max_ms": spec.MaxLatencyMS}))
	}
	if spec.SortBy == "name" {
		derived.Sort = []string{"name"}
	} else {
		derived.Sort = []string{"latency"}
	}
	custom, err := pipeline.ParseSpec(rulePipelineJSON)
	if err != nil {
		return pipeline.Spec{}, err
	}
	derived.Normalize = append(derived.Normalize, custom.Normalize...)
	derived.Enrich = append(derived.Enrich, custom.Enrich...)
	derived.Predicates = append(derived.Predicates, custom.Predicates...)
	if custom.Score != nil {
		derived.Score = custom.Score
		derived.Sort = []string{"score"}
	}
	if custom.Bucket != nil {
		derived.Bucket = custom.Bucket
	}
	if len(custom.Sort) > 0 {
		derived.Sort = custom.Sort
	}
	if custom.Limit.Total > 0 {
		derived.Limit.Total = custom.Limit.Total
	}
	if custom.Retain != nil {
		derived.Retain = custom.Retain
	}
	if custom.Limit.PerBucket > 0 {
		derived.Limit.PerBucket = custom.Limit.PerBucket
	}
	return derived, nil
}

func candidateToNode(candidate store.GroupNodeCandidate) *pipeline.Node {
	var latency *int
	if candidate.LastLatencyMS != nil {
		value := *candidate.LastLatencyMS
		latency = &value
	}
	return &pipeline.Node{
		ID:              candidate.ID,
		Fingerprint:     candidate.Fingerprint,
		DisplayName:     candidate.DisplayName,
		Protocol:        candidate.Protocol,
		LifecycleState:  candidate.LifecycleState,
		SubscriptionIDs: append([]string(nil), candidate.SubscriptionIDs...),
		LatencyMS:       latency,
	}
}

func mustRule(name string, config map[string]any) pipeline.RuleSpec {
	encoded, err := json.Marshal(config)
	if err != nil {
		panic(fmt.Sprintf("encode pipeline rule %s config: %v", name, err))
	}
	return pipeline.RuleSpec{Use: name, Config: encoded}
}
