package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func intPointer(value int) *int { return &value }

func testNodes() []*Node {
	return []*Node{
		{ID: "node-a", DisplayName: "HK 香港 01", Protocol: "vless", LifecycleState: "healthy", SubscriptionIDs: []string{"sub-1"}, LatencyMS: intPointer(120)},
		{ID: "node-b", DisplayName: "JP Tokyo 02", Protocol: "trojan", LifecycleState: "degraded", SubscriptionIDs: []string{"sub-1"}, LatencyMS: intPointer(80)},
		{ID: "node-c", DisplayName: "US Los Angeles", Protocol: "vless", LifecycleState: "healthy", SubscriptionIDs: []string{"sub-2"}, LatencyMS: nil},
		{ID: "node-d", DisplayName: "HK 香港 02", Protocol: "vmess", LifecycleState: "quarantined", SubscriptionIDs: []string{"sub-2"}, LatencyMS: intPointer(400)},
	}
}

func buildOrFatal(t *testing.T, spec Spec) *Pipeline {
	t.Helper()
	built, err := DefaultRegistry().Build(spec)
	if err != nil {
		t.Fatalf("build pipeline: %v", err)
	}
	return built
}

func runOrFatal(t *testing.T, built *Pipeline, nodes []*Node) Result {
	t.Helper()
	result, err := built.Run(context.Background(), nodes)
	if err != nil {
		t.Fatalf("run pipeline: %v", err)
	}
	return result
}

func resultIDs(result Result) []string {
	ids := make([]string, 0, len(result.Nodes))
	for _, nodeCtx := range result.Nodes {
		ids = append(ids, nodeCtx.Node.ID)
	}
	return ids
}

func TestBuildRejectsUnknownRulesAndConfigs(t *testing.T) {
	registry := DefaultRegistry()
	cases := []Spec{
		{Predicates: []RuleSpec{{Use: "no-such-rule"}}},
		{Predicates: []RuleSpec{{Use: "protocols"}}},
		{Predicates: []RuleSpec{{Use: "max-latency", Config: json.RawMessage(`{"max_ms":0}`)}}},
		{Sort: []string{"unknown-key"}},
		{Bucket: &RuleSpec{Use: "no-such-bucket"}},
		{Score: &ScoreSpec{Weights: map[string]float64{"speed": 1}}},
		{Score: &ScoreSpec{Missing: "guess"}},
		{Limit: LimitSpec{Total: -1}},
	}
	for index, spec := range cases {
		if _, err := registry.Build(spec); err == nil {
			t.Fatalf("case %d: expected build error", index)
		}
	}
}

func TestPredicateFilteringTracesAndExclusions(t *testing.T) {
	built := buildOrFatal(t, Spec{
		Predicates: []RuleSpec{
			{Use: "protocols", Config: json.RawMessage(`{"values":["vless"]}`)},
		},
		Sort: []string{"latency"},
	})
	result := runOrFatal(t, built, testNodes())
	ids := resultIDs(result)
	if len(ids) != 2 || ids[0] != "node-a" || ids[1] != "node-c" {
		t.Fatalf("unexpected kept nodes: %v", ids)
	}
	if len(result.Excluded) != 2 {
		t.Fatalf("expected 2 exclusions, got %d", len(result.Excluded))
	}
	for _, exclusion := range result.Excluded {
		if exclusion.Reason == "" || exclusion.Rule != "protocols" {
			t.Fatalf("exclusion must carry rule and reason: %+v", exclusion)
		}
	}
	var hits, misses int
	for _, trace := range result.Traces {
		switch trace.Outcome {
		case OutcomeHit:
			hits++
		case OutcomeMiss:
			misses++
		}
	}
	if hits != 2 || misses != 2 {
		t.Fatalf("expected 2 hits and 2 misses, got %d/%d", hits, misses)
	}
}

type failingPredicate struct{}

func (failingPredicate) Match(context.Context, *NodeContext) (bool, string, error) {
	return false, "", errors.New("rule backend unavailable")
}

func TestPredicateErrorFailsOpen(t *testing.T) {
	registry := DefaultRegistry()
	registry.RegisterPredicate("always-error", func(json.RawMessage) (Predicate, error) {
		return failingPredicate{}, nil
	})
	built, err := registry.Build(Spec{Predicates: []RuleSpec{{Use: "always-error"}}})
	if err != nil {
		t.Fatalf("build pipeline: %v", err)
	}
	result := runOrFatal(t, built, testNodes())
	if len(result.Nodes) != len(testNodes()) {
		t.Fatalf("a failing rule must not drop nodes, kept %d", len(result.Nodes))
	}
	var errorTraces int
	for _, trace := range result.Traces {
		if trace.Outcome == OutcomeError && trace.Rule == "always-error" {
			errorTraces++
		}
	}
	if errorTraces != len(testNodes()) {
		t.Fatalf("expected one error trace per node, got %d", errorTraces)
	}
}

func TestWeightedScorerMissingPolicies(t *testing.T) {
	node := &Node{ID: "node-x", LifecycleState: "healthy"} // no latency sample
	for policy, expected := range map[string]float64{
		MissingSkip:    1.0,           // latency dropped, availability=1 renormalized
		MissingNeutral: 0.6*0.5 + 0.4, // latency assumed 0.5
		MissingZero:    0.4,           // latency assumed worst
	} {
		scorer, err := newWeightedScorer(ScoreSpec{Missing: policy})
		if err != nil {
			t.Fatalf("policy %s: %v", policy, err)
		}
		score, breakdown, err := scorer.Score(context.Background(), &NodeContext{Node: node})
		if err != nil {
			t.Fatalf("policy %s: %v", policy, err)
		}
		if diff := score - expected; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("policy %s: expected score %.3f, got %.3f", policy, expected, score)
		}
		if policy != MissingSkip {
			if _, exists := breakdown[componentLatency]; !exists {
				t.Fatalf("policy %s: breakdown must contain the latency component", policy)
			}
		}
	}
}

func TestWeightedScorerLatencyNormalization(t *testing.T) {
	scorer, err := newWeightedScorer(ScoreSpec{
		Weights:          map[string]float64{componentLatency: 1},
		LatencyCeilingMS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	for latency, expected := range map[int]float64{0: 1, 500: 0.5, 1000: 0, 5000: 0} {
		node := &Node{ID: "node", LatencyMS: intPointer(latency)}
		score, _, err := scorer.Score(context.Background(), &NodeContext{Node: node})
		if err != nil {
			t.Fatal(err)
		}
		if diff := score - expected; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("latency %d: expected %.2f, got %.2f", latency, expected, score)
		}
	}
}

func TestScoreSortAndBreakdownTrace(t *testing.T) {
	built := buildOrFatal(t, Spec{
		Score: &ScoreSpec{Missing: MissingZero},
		Sort:  []string{"score"},
	})
	result := runOrFatal(t, built, testNodes())
	ids := resultIDs(result)
	// node-a: healthy + 120ms beats node-b: degraded + 80ms under default weights.
	if ids[0] != "node-a" {
		t.Fatalf("expected node-a first by score, got %v", ids)
	}
	var found bool
	for _, trace := range result.Traces {
		if trace.Stage == StageScore && trace.NodeID == "node-a" {
			found = true
			if !strings.Contains(trace.Detail, "latency=") || !strings.Contains(trace.Detail, "availability=") {
				t.Fatalf("score trace must expose the breakdown, got %q", trace.Detail)
			}
		}
	}
	if !found {
		t.Fatal("missing score trace for node-a")
	}
}

func TestRegionEnricherAndBucketLimits(t *testing.T) {
	built := buildOrFatal(t, Spec{
		Enrich: []RuleSpec{{Use: "name-region"}},
		Bucket: &RuleSpec{Use: "region"},
		Sort:   []string{"latency"},
		Limit:  LimitSpec{PerBucket: 1},
	})
	result := runOrFatal(t, built, testNodes())
	buckets := make(map[string]int)
	for _, nodeCtx := range result.Nodes {
		buckets[nodeCtx.Bucket]++
	}
	for bucket, count := range buckets {
		if count > 1 {
			t.Fatalf("bucket %q exceeds per-bucket limit: %d", bucket, count)
		}
	}
	if buckets["hk"] != 1 || buckets["jp"] != 1 || buckets["us"] != 1 {
		t.Fatalf("unexpected buckets: %v", buckets)
	}
	var limited bool
	for _, exclusion := range result.Excluded {
		if exclusion.Rule == "per-bucket-limit" && exclusion.NodeID == "node-d" {
			limited = true
		}
	}
	if !limited {
		t.Fatal("node-d should be excluded by the per-bucket limit")
	}
}

func TestTotalLimitRecordsExclusions(t *testing.T) {
	built := buildOrFatal(t, Spec{Sort: []string{"latency"}, Limit: LimitSpec{Total: 2}})
	result := runOrFatal(t, built, testNodes())
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}
	if len(result.Excluded) != 2 {
		t.Fatalf("expected 2 limit exclusions, got %d", len(result.Excluded))
	}
}

func TestDeterministicOrdering(t *testing.T) {
	built := buildOrFatal(t, Spec{Sort: []string{"latency"}})
	first := resultIDs(runOrFatal(t, built, testNodes()))
	second := resultIDs(runOrFatal(t, built, testNodes()))
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("ordering must be deterministic: %v vs %v", first, second)
	}
	expected := "node-b,node-a,node-d,node-c" // 80, 120, 400, missing sample last
	if strings.Join(first, ",") != expected {
		t.Fatalf("expected %s, got %s", expected, strings.Join(first, ","))
	}
}

func TestNormalizerEmitsBeforeAfterTrace(t *testing.T) {
	built := buildOrFatal(t, Spec{Normalize: []RuleSpec{{Use: "trim-display-name"}}})
	nodes := []*Node{{ID: "node-a", DisplayName: "  HK   01  "}}
	result := runOrFatal(t, built, nodes)
	if nodes[0].DisplayName != "HK 01" {
		t.Fatalf("display name not normalized: %q", nodes[0].DisplayName)
	}
	var found bool
	for _, trace := range result.Traces {
		if trace.Stage == StageNormalize && trace.Outcome == OutcomeModified {
			found = true
			if !strings.Contains(trace.Detail, "HK 01") {
				t.Fatalf("modification trace must include the new value: %q", trace.Detail)
			}
		}
	}
	if !found {
		t.Fatal("missing normalize trace")
	}
}
