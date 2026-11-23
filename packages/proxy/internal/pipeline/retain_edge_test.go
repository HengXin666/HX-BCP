package pipeline

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestRetainChallengerReplacesWithinItsOwnFullBucket(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry := DefaultRegistry()
	registry.RegisterBucketer("fixed", func(json.RawMessage) (Bucketer, error) {
		return fixedBucket{"new": "x", "a": "x", "b": "y"}, nil
	})
	built, err := registry.Build(Spec{Sort: []string{"latency"}, Bucket: &RuleSpec{Use: "fixed"}, Limit: LimitSpec{Total: 2, PerBucket: 1}, Retain: &RetainSpec{Margin: 50}})
	if err != nil {
		t.Fatal(err)
	}
	// Both buckets are full. The globally weakest incumbent is b (bucket y),
	// but the challenger belongs to x and clearly beats a, the x incumbent.
	nodes := []*Node{latencyNode("new", 10), latencyNode("a", 200), latencyNode("b", 400)}
	incumbents := Incumbents{JoinedAt: map[string]time.Time{"fp-a": now.Add(-time.Hour), "fp-b": now.Add(-time.Hour)}, Now: now}
	result, err := built.RunRetaining(context.Background(), nodes, incumbents)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(result); got != "new,b" {
		t.Fatalf("members = %s, want new,b", got)
	}
}

func TestRetainNeverReplacesOnMissingLatencyOnBothSides(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	built := buildRetaining(t, 1, RetainSpec{Margin: 0})
	old := &Node{ID: "old", Fingerprint: "fp-old", DisplayName: "old", LifecycleState: "healthy"}
	challenger := &Node{ID: "new", Fingerprint: "fp-new", DisplayName: "new", LifecycleState: "healthy"}
	incumbents := Incumbents{JoinedAt: map[string]time.Time{"fp-old": now.Add(-time.Hour)}, Now: now}
	result, err := built.RunRetaining(context.Background(), []*Node{challenger, old}, incumbents)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(result); got != "old" {
		t.Fatalf("members = %s, want old (-Inf minus -Inf is NaN, never better)", got)
	}
}

func TestScoreRejectsNonFiniteOrHugeWeights(t *testing.T) {
	for _, weight := range []float64{math.NaN(), math.Inf(1), MaxScoreWeight * 10} {
		if _, err := newWeightedScorer(ScoreSpec{Weights: map[string]float64{componentLatency: weight}}); err == nil {
			t.Fatalf("weight %v accepted", weight)
		}
	}
	if _, err := newWeightedScorer(ScoreSpec{Weights: map[string]float64{componentLatency: MaxScoreWeight}}); err != nil {
		t.Fatalf("max weight rejected: %v", err)
	}
}
