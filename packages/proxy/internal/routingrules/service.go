package routingrules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/HengXin666/hx-pxy/internal/store"
)

const MetadataKey = "routing_rule_sets"

var ErrInvalid = errors.New("invalid routing rules")

type Repository interface {
	GetMetadata(context.Context, string) (string, error)
	SetMetadata(context.Context, string, string) error
	ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error)
}

type Reader interface {
	GetMetadata(context.Context, string) (string, error)
}

type Applier interface {
	Apply(context.Context) error
}

type Rule struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type Action struct {
	Type         string `json:"type"`
	ProxyGroupID string `json:"proxy_group_id,omitempty"`
}

// GroupRoute assigns one reusable site alias to one proxy group's inbound
// traffic. The action belongs to the group assignment, not the global alias,
// so the same site group can be DIRECT in one proxy group and rejected or
// forwarded to another proxy group elsewhere.
type GroupRoute struct {
	ProxyGroupID string `json:"proxy_group_id"`
	Action       Action `json:"action"`
}

type RuleSet struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Enabled  bool         `json:"enabled"`
	Priority int          `json:"priority"`
	Routes   []GroupRoute `json:"routes,omitempty"`
	// AppliedGroupIDs and Action are retained for reading v1 configurations.
	// New clients use Routes so actions can vary per proxy group.
	AppliedGroupIDs []string `json:"applied_group_ids,omitempty"`
	Action          Action   `json:"action,omitempty"`
	Rules           []Rule   `json:"rules"`
}

type Config struct {
	// Default and AppliesTo are set by a YAML apply. Empty Default is a legacy
	// JSON document. AppliesTo is the proxy group ids whose unmatched traffic
	// follows Default; empty means "do not retarget other inbounds".
	// See .agents/notes/implemented/feature/2026-10-18-yaml-routing-intent.md.
	Default   string    `json:"default,omitempty"`
	AppliesTo []string  `json:"applies_to,omitempty"`
	RuleSets  []RuleSet `json:"rule_sets"`
}

func Default() Config {
	return Config{RuleSets: []RuleSet{}}
}

func Load(ctx context.Context, reader Reader) (Config, error) {
	value, err := reader.GetMetadata(ctx, MetadataKey)
	if errors.Is(err, store.ErrNotFound) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return Config{}, fmt.Errorf("decode routing rules: %w", err)
	}
	if config.RuleSets == nil {
		config.RuleSets = []RuleSet{}
	}
	return config, nil
}

type Service struct {
	// mu covers save, apply and rollback. Overlapping updates let the slower
	// rollback write the older document back over the newer one.
	mu         sync.Mutex
	repository Repository
	applier    Applier
}

func NewService(repository Repository, applier Applier) (*Service, error) {
	if repository == nil {
		return nil, errors.New("routing rules repository is required")
	}
	return &Service{repository: repository, applier: applier}, nil
}

func (s *Service) Get(ctx context.Context) (Config, error) {
	return Load(ctx, s.repository)
}

func (s *Service) Update(ctx context.Context, config Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persist(ctx, config)
}

// UpdateRuleSets replaces rule sets and keeps the stored YAML default and
// applies_to. The read and the write share this lock, so a concurrent
// ApplyYAML cannot be overwritten by a default the caller loaded earlier.
// An applies_to id whose group was deleted is dropped. A disabled group stays,
// so enabling it again keeps the scope. A new YAML document still rejects a
// disabled, missing or ambiguous group in TranslateYAML.
func (s *Service) UpdateRuleSets(ctx context.Context, ruleSets []RuleSet) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := Load(ctx, s.repository)
	if err != nil {
		return Config{}, err
	}
	current.RuleSets = ruleSets
	return s.persist(ctx, current)
}

func (s *Service) persist(ctx context.Context, config Config) (Config, error) {
	normalize(&config)
	groups, err := s.repository.ListProxyGroups(ctx)
	if err != nil {
		return Config{}, err
	}
	config.AppliesTo = knownAppliesTo(config.AppliesTo, groups)
	if err := Validate(config, groups); err != nil {
		return Config{}, err
	}
	previous, err := Load(ctx, s.repository)
	if err != nil {
		return Config{}, err
	}
	if err := save(ctx, s.repository, config); err != nil {
		return Config{}, err
	}
	if s.applier == nil {
		return config, nil
	}
	if err := s.applier.Apply(ctx); err != nil {
		rollbackErr := save(ctx, s.repository, previous)
		if rollbackErr == nil {
			rollbackErr = s.applier.Apply(ctx)
		}
		return Config{}, fmt.Errorf("apply routing rules: %w; rollback: %v", err, rollbackErr)
	}
	return config, nil
}

func knownAppliesTo(ids []string, groups []store.ProxyGroupRecord) []string {
	known := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		known[group.ID] = struct{}{}
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := known[id]; ok {
			kept = append(kept, id)
		}
	}
	return kept
}

func save(ctx context.Context, repository Repository, config Config) error {
	encoded, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode routing rules: %w", err)
	}
	return repository.SetMetadata(ctx, MetadataKey, string(encoded))
}

func Validate(config Config, groups []store.ProxyGroupRecord) error {
	if config.Default != "" && !knownDefault(config.Default) {
		return fmt.Errorf("%w: default must be proxy, direct or reject", ErrInvalid)
	}
	if config.Default == "" && len(config.AppliesTo) > 0 {
		return fmt.Errorf("%w: applies_to requires a default of proxy, direct or reject", ErrInvalid)
	}
	groupIDs := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		groupIDs[group.ID] = struct{}{}
	}
	// Disabled groups are allowed. The compiler skips them, and TranslateYAML
	// is what rejects a new document that names one. Deleting the group is a
	// different problem: knownAppliesTo drops that id before this check, so a
	// rules-page save does not 422.
	for _, groupID := range config.AppliesTo {
		if _, exists := groupIDs[groupID]; !exists {
			return fmt.Errorf("%w: applies_to references a missing group", ErrInvalid)
		}
	}
	if len(config.RuleSets) > 100 {
		return fmt.Errorf("%w: at most 100 rule sets are allowed", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(config.RuleSets))
	for _, set := range config.RuleSets {
		if !validID(set.ID) || len(set.Name) < 1 || len(set.Name) > 60 || set.Priority < 0 || set.Priority > 10000 {
			return fmt.Errorf("%w: rule set id, name, or priority is invalid", ErrInvalid)
		}
		if _, duplicate := seen[set.ID]; duplicate {
			return fmt.Errorf("%w: duplicate rule set id %q", ErrInvalid, set.ID)
		}
		seen[set.ID] = struct{}{}
		if len(set.Rules) < 1 || len(set.Rules) > 1000 {
			return fmt.Errorf("%w: rule set %q must contain 1-1000 rules", ErrInvalid, set.ID)
		}
		for _, groupID := range set.AppliedGroupIDs {
			if _, exists := groupIDs[groupID]; !exists {
				return fmt.Errorf("%w: rule set %q references missing applied group", ErrInvalid, set.ID)
			}
		}
		if len(set.Routes) == 0 && set.Action.Type != "" {
			if err := validateAction(set.ID, set.Action, groupIDs); err != nil {
				return err
			}
		}
		routeGroups := make(map[string]struct{}, len(set.Routes))
		for _, route := range set.Routes {
			if _, exists := groupIDs[route.ProxyGroupID]; !exists {
				return fmt.Errorf("%w: rule set %q references missing proxy group", ErrInvalid, set.ID)
			}
			if _, duplicate := routeGroups[route.ProxyGroupID]; duplicate {
				return fmt.Errorf("%w: rule set %q has duplicate proxy group routes", ErrInvalid, set.ID)
			}
			routeGroups[route.ProxyGroupID] = struct{}{}
			if err := validateAction(set.ID, route.Action, groupIDs); err != nil {
				return err
			}
		}
		for _, rule := range set.Rules {
			if err := validateRule(rule); err != nil {
				return fmt.Errorf("%w: rule set %q: %v", ErrInvalid, set.ID, err)
			}
		}
	}
	return nil
}

func validateAction(setID string, action Action, groups map[string]struct{}) error {
	if action.Type != "reject" && action.Type != "direct" && action.Type != "proxy_group" {
		return fmt.Errorf("%w: rule set %q action is invalid", ErrInvalid, setID)
	}
	if action.Type == "proxy_group" {
		if _, exists := groups[action.ProxyGroupID]; !exists {
			return fmt.Errorf("%w: rule set %q target group is missing", ErrInvalid, setID)
		}
	}
	return nil
}

func Compile(config Config, groups []store.ProxyGroupRecord, listeners []store.ListenerRecord, listenerName func(string) string) []string {
	groupNames := make(map[string]string, len(groups))
	for _, group := range groups {
		if group.Enabled {
			groupNames[group.ID] = group.Name
		}
	}
	sort.Slice(config.RuleSets, func(left, right int) bool {
		if config.RuleSets[left].Priority == config.RuleSets[right].Priority {
			return config.RuleSets[left].ID < config.RuleSets[right].ID
		}
		return config.RuleSets[left].Priority < config.RuleSets[right].Priority
	})
	compiled := make([]string, 0)
	for _, set := range config.RuleSets {
		if !set.Enabled {
			continue
		}
		if len(set.Routes) > 0 {
			for _, route := range set.Routes {
				action := actionName(route.Action, groupNames)
				for _, inbound := range inboundNames([]string{route.ProxyGroupID}, listeners, listenerName) {
					for _, rule := range set.Rules {
						compiled = append(compiled, "AND,((IN-NAME,"+inbound+"),("+matcherText(rule)+")),"+action)
					}
				}
			}
			continue
		}
		if set.Action.Type == "" {
			continue
		}
		action := actionName(set.Action, groupNames)
		inbounds := inboundNames(set.AppliedGroupIDs, listeners, listenerName)
		for _, rule := range set.Rules {
			matcher := matcherText(rule)
			if len(set.AppliedGroupIDs) == 0 {
				compiled = append(compiled, matcher+","+action)
				continue
			}
			for _, inbound := range inbounds {
				compiled = append(compiled, "AND,((IN-NAME,"+inbound+"),("+matcher+")),"+action)
			}
		}
	}
	return compiled
}

func actionName(action Action, groups map[string]string) string {
	switch action.Type {
	case "direct":
		return "DIRECT"
	case "proxy_group":
		if name := groups[action.ProxyGroupID]; name != "" {
			return name
		}
	}
	return "REJECT"
}

func inboundNames(groupIDs []string, listeners []store.ListenerRecord, name func(string) string) []string {
	selected := make(map[string]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		selected[id] = struct{}{}
	}
	result := make([]string, 0)
	for _, listener := range listeners {
		if listener.Enabled {
			if _, exists := selected[listener.ProxyGroupID]; exists {
				result = append(result, name(listener.ID))
			}
		}
	}
	sort.Strings(result)
	return result
}

func matcherText(rule Rule) string {
	return strings.ToUpper(strings.ReplaceAll(rule.Type, "_", "-")) + "," + rule.Value
}

func validateRule(rule Rule) error {
	value := strings.TrimSpace(rule.Value)
	if len(value) < 1 || len(value) > 253 || strings.ContainsAny(value, ",\r\n") {
		return errors.New("rule value is empty, too long, or contains a separator")
	}
	switch rule.Type {
	case "domain", "domain_suffix", "domain_keyword", "geosite", "geoip", "process_name":
		return nil
	case "ip_cidr":
		if _, _, err := net.ParseCIDR(value); err != nil {
			return errors.New("IP CIDR rule is invalid")
		}
		return nil
	case "network":
		if value != "tcp" && value != "udp" {
			return errors.New("network rule must be tcp or udp")
		}
		return nil
	case "dst_port":
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return errors.New("destination port is invalid")
		}
		return nil
	default:
		return errors.New("rule type is unsupported")
	}
}

func normalize(config *Config) {
	config.Default = strings.ToLower(strings.TrimSpace(config.Default))
	config.AppliesTo = uniqueSorted(config.AppliesTo)
	for setIndex := range config.RuleSets {
		set := &config.RuleSets[setIndex]
		set.ID = strings.ToLower(strings.TrimSpace(set.ID))
		set.Name = strings.TrimSpace(set.Name)
		set.Action.Type = strings.ToLower(strings.TrimSpace(set.Action.Type))
		set.Action.ProxyGroupID = strings.TrimSpace(set.Action.ProxyGroupID)
		set.AppliedGroupIDs = uniqueSorted(set.AppliedGroupIDs)
		for routeIndex := range set.Routes {
			set.Routes[routeIndex].ProxyGroupID = strings.TrimSpace(set.Routes[routeIndex].ProxyGroupID)
			set.Routes[routeIndex].Action.Type = strings.ToLower(strings.TrimSpace(set.Routes[routeIndex].Action.Type))
			set.Routes[routeIndex].Action.ProxyGroupID = strings.TrimSpace(set.Routes[routeIndex].Action.ProxyGroupID)
		}
		sort.Slice(set.Routes, func(left, right int) bool {
			return set.Routes[left].ProxyGroupID < set.Routes[right].ProxyGroupID
		})
		for ruleIndex := range set.Rules {
			set.Rules[ruleIndex].Type = strings.ToLower(strings.TrimSpace(set.Rules[ruleIndex].Type))
			set.Rules[ruleIndex].Value = strings.TrimSpace(set.Rules[ruleIndex].Value)
		}
	}
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validID(value string) bool {
	if len(value) < 1 || len(value) > 40 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}
