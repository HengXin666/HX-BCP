package routingrules

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
	"gopkg.in/yaml.v3"
)

// DefaultProxy, DefaultDirect and DefaultReject are the only YAML defaults.
// None of them is a Mihomo rule by itself. The compiler reads Config.Default
// and places the inbound catch-all, or the reject lines, around TUN and the
// residential fallback. See
// .agents/notes/implemented/feature/2026-10-18-yaml-routing-intent.md.
const (
	DefaultProxy  = "proxy"
	DefaultDirect = "direct"
	DefaultReject = "reject"
)

// SupportedDefaults is the closed set Validate and TranslateYAML accept.
// The capability catalog publishes this list and nothing else.
func SupportedDefaults() []string {
	return []string{DefaultProxy, DefaultDirect, DefaultReject}
}

func knownDefault(value string) bool {
	for _, candidate := range SupportedDefaults() {
		if value == candidate {
			return true
		}
	}
	return false
}

// yamlDocument is the intent layer from issue #3. It is not a Mihomo document:
// Translate turns it into the routing Config that Update already validates,
// saves and applies.
type yamlDocument struct {
	Version   int        `yaml:"version"`
	Name      string     `yaml:"name"`
	Default   string     `yaml:"default"`
	AppliesTo []string   `yaml:"applies_to"`
	Rules     []yamlRule `yaml:"rules"`
}

type yamlRule struct {
	Match  map[string]any `yaml:"match"`
	Action string         `yaml:"action"`
	Group  string         `yaml:"group"`
}

// TranslateYAML turns one intent document into a whole routing Config.
// Applying it replaces the stored document; it does not merge with it.
// A second YAML document, or trailing syntax, is rejected rather than ignored.
func TranslateYAML(raw string, groups []store.ProxyGroupRecord) (Config, error) {
	var document yamlDocument
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return Config{}, fmt.Errorf("%w: yaml: %v", ErrInvalid, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("%w: yaml must contain exactly one document", ErrInvalid)
	}
	if document.Version != 1 {
		return Config{}, fmt.Errorf("%w: yaml version must be 1", ErrInvalid)
	}
	name := strings.TrimSpace(document.Name)
	if name == "" {
		name = "yaml"
	}
	if len(name) > 40 {
		return Config{}, fmt.Errorf("%w: yaml name is too long", ErrInvalid)
	}
	fallback, err := translateDefault(document.Default)
	if err != nil {
		return Config{}, err
	}
	appliesTo, err := translateAppliesTo(document.AppliesTo, groups)
	if err != nil {
		return Config{}, err
	}
	config := Config{Default: fallback, AppliesTo: appliesTo, RuleSets: make([]RuleSet, 0, len(document.Rules))}
	for index, rule := range document.Rules {
		set, err := translateRule(index, name, rule, groups)
		if err != nil {
			return Config{}, err
		}
		config.RuleSets = append(config.RuleSets, set)
	}
	return config, nil
}

func translateRule(index int, documentName string, rule yamlRule, groups []store.ProxyGroupRecord) (RuleSet, error) {
	if len(rule.Match) != 1 {
		return RuleSet{}, fmt.Errorf("%w: rule %d must have exactly one match key", ErrInvalid, index+1)
	}
	var matchType string
	var matchValue any
	for key, value := range rule.Match {
		matchType, matchValue = key, value
	}
	rules, err := matchRules(matchType, matchValue)
	if err != nil {
		return RuleSet{}, fmt.Errorf("%w: rule %d: %v", ErrInvalid, index+1, err)
	}
	action, err := translateAction(rule.Action, rule.Group, groups)
	if err != nil {
		return RuleSet{}, fmt.Errorf("%w: rule %d: %v", ErrInvalid, index+1, err)
	}
	id := fmt.Sprintf("yaml-%02d", index+1)
	setName := documentName
	if len(setName) > 60 {
		setName = setName[:60]
	}
	return RuleSet{
		ID:       id,
		Name:     setName,
		Enabled:  true,
		Priority: (index + 1) * 10,
		Action:   action,
		Rules:    rules,
	}, nil
}

func matchRules(matchType string, value any) ([]Rule, error) {
	switch matchType {
	case "domain", "domain_suffix", "domain_keyword", "geosite", "geoip", "ip_cidr", "dst_port", "network":
	default:
		return nil, fmt.Errorf("match key %q is unsupported", matchType)
	}
	values, err := matchValues(value)
	if err != nil {
		return nil, err
	}
	rules := make([]Rule, 0, len(values))
	for _, item := range values {
		item = normalizeMatch(matchType, item)
		if err := validateRule(Rule{Type: matchType, Value: item}); err != nil {
			return nil, err
		}
		rules = append(rules, Rule{Type: matchType, Value: item})
	}
	return rules, nil
}

func matchValues(value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, fmt.Errorf("match value is empty")
		}
		return []string{typed}, nil
	case []any:
		if len(typed) == 0 {
			return nil, fmt.Errorf("match list is empty")
		}
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("match list values must be strings")
			}
			values = append(values, text)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("match value must be a string or a list of strings")
	}
}

func normalizeMatch(matchType, value string) string {
	value = strings.TrimSpace(value)
	switch matchType {
	case "domain_suffix":
		return strings.TrimPrefix(value, ".")
	case "geoip":
		return strings.ToUpper(value)
	case "network":
		return strings.ToLower(value)
	default:
		return value
	}
}

func translateAction(action, group string, groups []store.ProxyGroupRecord) (Action, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	group = strings.TrimSpace(group)
	switch action {
	case "direct", "reject":
		if group != "" {
			return Action{}, fmt.Errorf("action %q does not take a group", action)
		}
		return Action{Type: action}, nil
	case "proxy":
		if group == "" {
			return Action{}, fmt.Errorf("action proxy requires a group")
		}
		resolved, err := resolveGroup(group, groups)
		if err != nil {
			return Action{}, err
		}
		return Action{Type: "proxy_group", ProxyGroupID: resolved.ID}, nil
	default:
		return Action{}, fmt.Errorf("action %q is unsupported", action)
	}
}

func resolveGroup(wanted string, groups []store.ProxyGroupRecord) (store.ProxyGroupRecord, error) {
	var byID *store.ProxyGroupRecord
	named := make([]store.ProxyGroupRecord, 0, 1)
	for index := range groups {
		group := groups[index]
		if group.ID == wanted {
			byID = &groups[index]
		}
		if group.Name == wanted {
			named = append(named, group)
		}
	}
	if byID != nil {
		if !byID.Enabled {
			return store.ProxyGroupRecord{}, fmt.Errorf("proxy group %q is disabled", wanted)
		}
		return *byID, nil
	}
	if len(named) == 1 {
		if !named[0].Enabled {
			return store.ProxyGroupRecord{}, fmt.Errorf("proxy group %q is disabled", wanted)
		}
		return named[0], nil
	}
	if len(named) > 1 {
		return store.ProxyGroupRecord{}, fmt.Errorf("proxy group name %q is ambiguous", wanted)
	}
	return store.ProxyGroupRecord{}, fmt.Errorf("proxy group %q was not found", wanted)
}

func translateAppliesTo(values []string, groups []store.ProxyGroupRecord) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	resolved := make([]string, 0, len(values))
	for _, value := range values {
		group, err := resolveGroup(strings.TrimSpace(value), groups)
		if err != nil {
			return nil, fmt.Errorf("%w: applies_to: %v", ErrInvalid, err)
		}
		resolved = append(resolved, group.ID)
	}
	return resolved, nil
}

func translateDefault(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = DefaultProxy
	}
	if !knownDefault(value) {
		return "", fmt.Errorf("%w: default must be proxy, direct or reject", ErrInvalid)
	}
	return value, nil
}

// ApplyYAML translates a document and replaces the stored routing config
// through Update, so a failed apply still rolls back.
func (s *Service) ApplyYAML(ctx context.Context, raw string) (Config, error) {
	groups, err := s.repository.ListProxyGroups(ctx)
	if err != nil {
		return Config{}, err
	}
	config, err := TranslateYAML(raw, groups)
	if err != nil {
		return Config{}, err
	}
	return s.Update(ctx, config)
}
