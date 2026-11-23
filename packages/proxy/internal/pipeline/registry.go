package pipeline

import (
	"encoding/json"
	"fmt"
)

// RuleSpec selects a registered rule by name with an optional JSON config.
type RuleSpec struct {
	Use    string          `json:"use"`
	Config json.RawMessage `json:"config,omitempty"`
}

// LimitSpec bounds the final member list so a group configuration cannot
// grow without limit.
type LimitSpec struct {
	Total     int `json:"total,omitempty"`
	PerBucket int `json:"per_bucket,omitempty"`
}

// Spec is the JSON-serializable description of a full pipeline. It is stored
// in proxy_groups.rule_pipeline_json.
type Spec struct {
	Normalize  []RuleSpec `json:"normalize,omitempty"`
	Enrich     []RuleSpec `json:"enrich,omitempty"`
	Predicates []RuleSpec `json:"predicates,omitempty"`
	Score      *ScoreSpec `json:"score,omitempty"`
	Bucket     *RuleSpec  `json:"bucket,omitempty"`
	Sort       []string   `json:"sort,omitempty"`
	Limit      LimitSpec  `json:"limit,omitempty"`
	// Retain keeps previous members between Sort and Limit; see retain.go.
	Retain *RetainSpec `json:"retain,omitempty"`
}

type (
	NormalizerFactory func(json.RawMessage) (Normalizer, error)
	EnricherFactory   func(json.RawMessage) (Enricher, error)
	PredicateFactory  func(json.RawMessage) (Predicate, error)
	BucketerFactory   func(json.RawMessage) (Bucketer, error)
)

// Registry maps rule names to factories. Rules are registered explicitly at
// process start; there is no reflection or dynamic plugin loading.
type Registry struct {
	normalizers map[string]NormalizerFactory
	enrichers   map[string]EnricherFactory
	predicates  map[string]PredicateFactory
	bucketers   map[string]BucketerFactory
}

func NewRegistry() *Registry {
	return &Registry{
		normalizers: make(map[string]NormalizerFactory),
		enrichers:   make(map[string]EnricherFactory),
		predicates:  make(map[string]PredicateFactory),
		bucketers:   make(map[string]BucketerFactory),
	}
}

func (r *Registry) RegisterNormalizer(name string, factory NormalizerFactory) {
	r.normalizers[name] = factory
}

func (r *Registry) RegisterEnricher(name string, factory EnricherFactory) {
	r.enrichers[name] = factory
}

func (r *Registry) RegisterPredicate(name string, factory PredicateFactory) {
	r.predicates[name] = factory
}

func (r *Registry) RegisterBucketer(name string, factory BucketerFactory) {
	r.bucketers[name] = factory
}

// Build resolves a Spec against the registry and returns a runnable pipeline.
// Unknown rule names and invalid configs fail the build; a stored spec must
// never half-build.
func (r *Registry) Build(spec Spec) (*Pipeline, error) {
	pipeline := &Pipeline{limit: spec.Limit}
	if spec.Limit.Total < 0 || spec.Limit.PerBucket < 0 {
		return nil, fmt.Errorf("pipeline limit must not be negative")
	}
	for _, ruleSpec := range spec.Normalize {
		factory, exists := r.normalizers[ruleSpec.Use]
		if !exists {
			return nil, fmt.Errorf("unknown normalizer %q", ruleSpec.Use)
		}
		rule, err := factory(ruleSpec.Config)
		if err != nil {
			return nil, fmt.Errorf("build normalizer %q: %w", ruleSpec.Use, err)
		}
		pipeline.normalizers = append(pipeline.normalizers, namedRule[Normalizer]{ruleSpec.Use, rule})
	}
	for _, ruleSpec := range spec.Enrich {
		factory, exists := r.enrichers[ruleSpec.Use]
		if !exists {
			return nil, fmt.Errorf("unknown enricher %q", ruleSpec.Use)
		}
		rule, err := factory(ruleSpec.Config)
		if err != nil {
			return nil, fmt.Errorf("build enricher %q: %w", ruleSpec.Use, err)
		}
		pipeline.enrichers = append(pipeline.enrichers, namedRule[Enricher]{ruleSpec.Use, rule})
	}
	for _, ruleSpec := range spec.Predicates {
		factory, exists := r.predicates[ruleSpec.Use]
		if !exists {
			return nil, fmt.Errorf("unknown predicate %q", ruleSpec.Use)
		}
		rule, err := factory(ruleSpec.Config)
		if err != nil {
			return nil, fmt.Errorf("build predicate %q: %w", ruleSpec.Use, err)
		}
		pipeline.predicates = append(pipeline.predicates, namedRule[Predicate]{ruleSpec.Use, rule})
	}
	if spec.Score != nil {
		scorer, err := newWeightedScorer(*spec.Score)
		if err != nil {
			return nil, fmt.Errorf("build scorer: %w", err)
		}
		pipeline.scorer = namedRule[Scorer]{"weighted", scorer}
	}
	if spec.Bucket != nil {
		factory, exists := r.bucketers[spec.Bucket.Use]
		if !exists {
			return nil, fmt.Errorf("unknown bucketer %q", spec.Bucket.Use)
		}
		rule, err := factory(spec.Bucket.Config)
		if err != nil {
			return nil, fmt.Errorf("build bucketer %q: %w", spec.Bucket.Use, err)
		}
		pipeline.bucketer = namedRule[Bucketer]{spec.Bucket.Use, rule}
	}
	for _, key := range spec.Sort {
		switch key {
		case "score", "latency", "name":
		default:
			return nil, fmt.Errorf("unknown sort key %q", key)
		}
	}
	pipeline.sortKeys = spec.Sort
	if spec.Retain != nil {
		if err := spec.Retain.Validate(); err != nil {
			return nil, err
		}
		retain := *spec.Retain
		pipeline.retain = &retain
	}
	return pipeline, nil
}

// ParseSpec decodes a stored pipeline spec. Empty input means "no rules".
func ParseSpec(raw string) (Spec, error) {
	var spec Spec
	if raw == "" || raw == "{}" {
		return spec, nil
	}
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return Spec{}, fmt.Errorf("decode pipeline spec: %w", err)
	}
	return spec, nil
}
