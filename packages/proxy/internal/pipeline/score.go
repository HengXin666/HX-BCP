package pipeline

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// Missing metric policies. A metric without a sample is never silently
// treated as zero or as a perfect value; the policy must be explicit.
const (
	MissingSkip    = "skip"    // drop the component and renormalize weights
	MissingNeutral = "neutral" // assume 0.5 on the normalized scale
	MissingZero    = "zero"    // assume the worst value
)

// MaxScoreWeight bounds a component weight. Weights are relative, so a larger
// bound adds nothing but the risk of overflowing the weighted sum.
const MaxScoreWeight = 1e6

const (
	defaultLatencyCeilingMS = 2000
	componentLatency        = "latency"
	componentAvailability   = "availability"
)

// ScoreSpec configures the default weighted scoring model:
//
//	score = sum(weight_i * normalized_component_i) / sum(weight_i)
//
// Components are normalized to [0, 1] before weighting. Weights are not
// hard-coded; groups can override them per pipeline spec.
type ScoreSpec struct {
	// Weights maps component name to weight. Supported components:
	// "latency" and "availability". Empty means the default model.
	Weights map[string]float64 `json:"weights,omitempty"`
	// LatencyCeilingMS is the latency mapped to score 0. Defaults to 2000.
	LatencyCeilingMS int `json:"latency_ceiling_ms,omitempty"`
	// Missing selects the missing-metric policy. Defaults to "skip".
	Missing string `json:"missing,omitempty"`
}

type weightedScorer struct {
	weights        map[string]float64
	latencyCeiling float64
	missing        string
}

func newWeightedScorer(spec ScoreSpec) (*weightedScorer, error) {
	weights := spec.Weights
	if len(weights) == 0 {
		weights = map[string]float64{componentLatency: 0.6, componentAvailability: 0.4}
	}
	for name, weight := range weights {
		if name != componentLatency && name != componentAvailability {
			return nil, fmt.Errorf("unknown score component %q", name)
		}
		// A bounded, finite weight keeps the weighted sum finite, so Retain
		// never compares NaN or infinite scores.
		if math.IsNaN(weight) || weight < 0 || weight > MaxScoreWeight {
			return nil, fmt.Errorf("score component %q weight must be between 0 and %g", name, float64(MaxScoreWeight))
		}
	}
	ceiling := spec.LatencyCeilingMS
	if ceiling == 0 {
		ceiling = defaultLatencyCeilingMS
	}
	if ceiling < 1 || ceiling > 60000 {
		return nil, fmt.Errorf("latency_ceiling_ms must be between 1 and 60000")
	}
	missing := spec.Missing
	if missing == "" {
		missing = MissingSkip
	}
	switch missing {
	case MissingSkip, MissingNeutral, MissingZero:
	default:
		return nil, fmt.Errorf("unknown missing metric policy %q", missing)
	}
	return &weightedScorer{
		weights:        weights,
		latencyCeiling: float64(ceiling),
		missing:        missing,
	}, nil
}

func (s *weightedScorer) Score(_ context.Context, nodeCtx *NodeContext) (float64, ScoreBreakdown, error) {
	breakdown := make(ScoreBreakdown, len(s.weights))
	var weightedSum, weightTotal float64
	names := make([]string, 0, len(s.weights))
	for name := range s.weights {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		weight := s.weights[name]
		if weight == 0 {
			continue
		}
		value, present := s.component(name, nodeCtx.Node)
		if !present {
			switch s.missing {
			case MissingSkip:
				continue
			case MissingNeutral:
				value = 0.5
			case MissingZero:
				value = 0
			}
		}
		breakdown[name] = weight * value
		weightedSum += weight * value
		weightTotal += weight
	}
	if weightTotal == 0 {
		return 0, breakdown, nil
	}
	return weightedSum / weightTotal, breakdown, nil
}

// component returns the normalized [0, 1] value of one metric and whether a
// sample exists.
func (s *weightedScorer) component(name string, node *Node) (float64, bool) {
	switch name {
	case componentLatency:
		if node.LatencyMS == nil {
			return 0, false
		}
		latency := float64(*node.LatencyMS)
		if latency >= s.latencyCeiling {
			return 0, true
		}
		if latency < 0 {
			latency = 0
		}
		return 1 - latency/s.latencyCeiling, true
	case componentAvailability:
		switch node.LifecycleState {
		case "healthy":
			return 1, true
		case "degraded":
			return 0.4, true
		case "quarantined":
			return 0, true
		case "candidate":
			// A candidate has not completed probing yet: no sample.
			return 0, false
		default:
			return 0, false
		}
	default:
		return 0, false
	}
}
