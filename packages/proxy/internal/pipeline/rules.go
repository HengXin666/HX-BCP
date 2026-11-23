package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultRegistry returns a registry with every built-in rule registered.
func DefaultRegistry() *Registry {
	registry := NewRegistry()
	registry.RegisterNormalizer("trim-display-name", newTrimDisplayName)
	registry.RegisterNormalizer("lowercase-protocol", newLowercaseProtocol)
	registry.RegisterEnricher("name-region", newNameRegionEnricher)
	registry.RegisterPredicate("subscriptions", newSubscriptionsPredicate)
	registry.RegisterPredicate("protocols", newValuesPredicate("protocol", func(node *Node) string { return node.Protocol }))
	registry.RegisterPredicate("states", newValuesPredicate("lifecycle state", func(node *Node) string { return node.LifecycleState }))
	registry.RegisterPredicate("name-keywords", newNameKeywordsPredicate)
	registry.RegisterPredicate("regions", newRegionsPredicate)
	registry.RegisterPredicate("max-latency", newMaxLatencyPredicate)
	registry.RegisterBucketer("region", newLabelBucketer("region"))
	registry.RegisterBucketer("protocol", newProtocolBucketer)
	registry.RegisterBucketer("subscription", newSubscriptionBucketer)
	return registry
}

type valuesConfig struct {
	Values []string `json:"values"`
}

func decodeValues(raw json.RawMessage) ([]string, error) {
	var config valuesConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
	}
	values := make([]string, 0, len(config.Values))
	for _, value := range config.Values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("values must not be empty")
	}
	return values, nil
}

// --- normalizers ---

type trimDisplayName struct{}

func newTrimDisplayName(json.RawMessage) (Normalizer, error) { return trimDisplayName{}, nil }

func (trimDisplayName) Normalize(_ context.Context, nodeCtx *NodeContext) (string, error) {
	trimmed := strings.Join(strings.Fields(nodeCtx.Node.DisplayName), " ")
	if trimmed == nodeCtx.Node.DisplayName {
		return "", nil
	}
	before := nodeCtx.Node.DisplayName
	nodeCtx.Node.DisplayName = trimmed
	return fmt.Sprintf("display name %q -> %q", before, trimmed), nil
}

type lowercaseProtocol struct{}

func newLowercaseProtocol(json.RawMessage) (Normalizer, error) { return lowercaseProtocol{}, nil }

func (lowercaseProtocol) Normalize(_ context.Context, nodeCtx *NodeContext) (string, error) {
	lowered := strings.ToLower(strings.TrimSpace(nodeCtx.Node.Protocol))
	if lowered == nodeCtx.Node.Protocol {
		return "", nil
	}
	before := nodeCtx.Node.Protocol
	nodeCtx.Node.Protocol = lowered
	return fmt.Sprintf("protocol %q -> %q", before, lowered), nil
}

// --- enrichers ---

// nameRegionEnricher derives a coarse region label from the display name.
// It is a stop-gap until a structured Geo enricher with sampled-at metadata
// replaces name-based matching.
type nameRegionEnricher struct{}

func newNameRegionEnricher(json.RawMessage) (Enricher, error) { return nameRegionEnricher{}, nil }

func (nameRegionEnricher) Enrich(_ context.Context, nodeCtx *NodeContext) error {
	if nodeCtx.Node.Label("region") != "" {
		return nil
	}
	name := strings.ToLower(nodeCtx.Node.DisplayName)
	for _, region := range knownRegions {
		for _, alias := range RegionAliases(region) {
			if strings.Contains(name, alias) {
				nodeCtx.Node.SetLabel("region", region)
				return nil
			}
		}
	}
	return nil
}

// --- predicates ---

type subscriptionsPredicate struct{ values []string }

func newSubscriptionsPredicate(raw json.RawMessage) (Predicate, error) {
	values, err := decodeValues(raw)
	if err != nil {
		return nil, err
	}
	return subscriptionsPredicate{values: values}, nil
}

func (p subscriptionsPredicate) Match(_ context.Context, nodeCtx *NodeContext) (bool, string, error) {
	for _, id := range nodeCtx.Node.SubscriptionIDs {
		for _, wanted := range p.values {
			if strings.EqualFold(id, wanted) {
				return true, fmt.Sprintf("subscription %s selected", id), nil
			}
		}
	}
	return false, "node does not belong to any selected subscription", nil
}

type valuesPredicate struct {
	field   string
	values  []string
	extract func(*Node) string
}

func newValuesPredicate(field string, extract func(*Node) string) PredicateFactory {
	return func(raw json.RawMessage) (Predicate, error) {
		values, err := decodeValues(raw)
		if err != nil {
			return nil, err
		}
		return valuesPredicate{field: field, values: values, extract: extract}, nil
	}
}

func (p valuesPredicate) Match(_ context.Context, nodeCtx *NodeContext) (bool, string, error) {
	actual := p.extract(nodeCtx.Node)
	for _, value := range p.values {
		if strings.EqualFold(actual, value) {
			return true, fmt.Sprintf("%s %q selected", p.field, actual), nil
		}
	}
	return false, fmt.Sprintf("%s %q is not in the allowed set", p.field, actual), nil
}

type nameKeywordsPredicate struct{ keywords []string }

func newNameKeywordsPredicate(raw json.RawMessage) (Predicate, error) {
	values, err := decodeValues(raw)
	if err != nil {
		return nil, err
	}
	return nameKeywordsPredicate{keywords: values}, nil
}

func (p nameKeywordsPredicate) Match(_ context.Context, nodeCtx *NodeContext) (bool, string, error) {
	name := strings.ToLower(nodeCtx.Node.DisplayName)
	for _, keyword := range p.keywords {
		if strings.Contains(name, keyword) {
			return true, fmt.Sprintf("name contains %q", keyword), nil
		}
	}
	return false, "name matches no configured keyword", nil
}

type regionsPredicate struct{ regions []string }

func newRegionsPredicate(raw json.RawMessage) (Predicate, error) {
	values, err := decodeValues(raw)
	if err != nil {
		return nil, err
	}
	return regionsPredicate{regions: values}, nil
}

func (p regionsPredicate) Match(_ context.Context, nodeCtx *NodeContext) (bool, string, error) {
	name := strings.ToLower(nodeCtx.Node.DisplayName)
	label := nodeCtx.Node.Label("region")
	for _, region := range p.regions {
		if label != "" && strings.EqualFold(label, region) {
			return true, fmt.Sprintf("region label %q selected", label), nil
		}
		for _, alias := range RegionAliases(region) {
			if strings.Contains(name, alias) {
				return true, fmt.Sprintf("name matches region %q alias %q", region, alias), nil
			}
		}
	}
	return false, "node matches none of the requested regions", nil
}

type maxLatencyPredicate struct{ maxMS int }

func newMaxLatencyPredicate(raw json.RawMessage) (Predicate, error) {
	var config struct {
		MaxMS int `json:"max_ms"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
	}
	if config.MaxMS <= 0 {
		return nil, fmt.Errorf("max_ms must be positive")
	}
	return maxLatencyPredicate{maxMS: config.MaxMS}, nil
}

func (p maxLatencyPredicate) Match(_ context.Context, nodeCtx *NodeContext) (bool, string, error) {
	latency := nodeCtx.Node.LatencyMS
	if latency == nil {
		return false, "node has no latency sample yet", nil
	}
	if *latency > p.maxMS {
		return false, fmt.Sprintf("latency %dms exceeds limit %dms", *latency, p.maxMS), nil
	}
	return true, fmt.Sprintf("latency %dms within limit %dms", *latency, p.maxMS), nil
}

// --- bucketers ---

const unknownBucket = "UNKNOWN"

func newLabelBucketer(label string) BucketerFactory {
	return func(json.RawMessage) (Bucketer, error) {
		return labelBucketer{label: label}, nil
	}
}

type labelBucketer struct{ label string }

func (b labelBucketer) Bucket(_ context.Context, nodeCtx *NodeContext) (string, error) {
	if value := nodeCtx.Node.Label(b.label); value != "" {
		return value, nil
	}
	return unknownBucket, nil
}

type protocolBucketer struct{}

func newProtocolBucketer(json.RawMessage) (Bucketer, error) { return protocolBucketer{}, nil }

func (protocolBucketer) Bucket(_ context.Context, nodeCtx *NodeContext) (string, error) {
	if nodeCtx.Node.Protocol == "" {
		return unknownBucket, nil
	}
	return strings.ToLower(nodeCtx.Node.Protocol), nil
}

type subscriptionBucketer struct{}

func newSubscriptionBucketer(json.RawMessage) (Bucketer, error) { return subscriptionBucketer{}, nil }

func (subscriptionBucketer) Bucket(_ context.Context, nodeCtx *NodeContext) (string, error) {
	if len(nodeCtx.Node.SubscriptionIDs) == 0 {
		return unknownBucket, nil
	}
	return nodeCtx.Node.SubscriptionIDs[0], nil
}

// --- regions ---

var knownRegions = []string{"jp", "hk", "tw", "sg", "us", "kr"}

// RegionAliases maps a region request to lowercase name fragments. The table
// intentionally matches the historical proxygroup behavior so existing group
// definitions keep selecting the same nodes.
func RegionAliases(region string) []string {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "jp", "japan", "日本":
		return []string{"jp", "japan", "日本", "东京", "大阪", "tokyo", "osaka"}
	case "hk", "hong kong", "香港":
		return []string{"hk", "hong kong", "香港"}
	case "tw", "taiwan", "台湾", "台灣":
		return []string{"tw", "taiwan", "台湾", "台灣", "台北", "taipei"}
	case "sg", "singapore", "新加坡":
		return []string{"sg", "singapore", "新加坡", "狮城"}
	case "us", "usa", "united states", "美国", "美國":
		return []string{"us", "usa", "united states", "美国", "美國", "洛杉矶", "los angeles", "san jose"}
	case "kr", "korea", "韩国", "韓國":
		return []string{"kr", "korea", "韩国", "韓國", "首尔", "seoul"}
	default:
		return []string{strings.ToLower(strings.TrimSpace(region))}
	}
}
