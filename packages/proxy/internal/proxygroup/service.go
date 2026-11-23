package proxygroup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/HengXin666/hx-pxy/internal/pipeline"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

var (
	ErrNotFound    = errors.New("proxy group not found")
	ErrConflict    = errors.New("proxy group conflict")
	ErrInvalid     = errors.New("invalid proxy group")
	ErrApplyFailed = errors.New("proxy group apply failed")
	// ErrReferenced reports that a group cannot be deleted because listeners
	// still resolve their egress through it. It is distinct from ErrConflict,
	// which means "another group names this one": the two have different
	// remedies (repoint the listeners vs. edit the group's members), and the
	// front end needs to tell them apart without reading a message string —
	// this repository has already been bitten by judging logic off error text.
	ErrReferenced = errors.New("proxy group still has listeners attached")
)

const defaultTestURL = "http://cp.cloudflare.com/generate_204"

type Repository interface {
	CreateProxyGroup(context.Context, store.ProxyGroupRecord) (store.ProxyGroupRecord, error)
	GetProxyGroup(context.Context, string) (store.ProxyGroupRecord, error)
	ListProxyGroups(context.Context) ([]store.ProxyGroupRecord, error)
	UpdateProxyGroup(context.Context, store.ProxyGroupRecord, int) (store.ProxyGroupRecord, error)
	DeleteProxyGroup(context.Context, string, int) error
	ListNodeConfigs(context.Context, []string) ([]store.NodeConfigRecord, error)
	ListGroupNodeCandidates(context.Context) ([]store.GroupNodeCandidate, error)
	GetSubscription(context.Context, string) (store.SubscriptionRecord, error)
	// ListListeners answers "is this group still carrying traffic?", which is a
	// different question from referencedBy's "does another group name it?".
	// Both must be asked before a delete; see Service.Delete.
	ListListeners(context.Context) ([]store.ListenerRecord, error)
}

type Reconciler interface {
	Apply(context.Context) error
}

type SourceSpec struct {
	// AllowEmpty permits a deliberately dormant fail-closed group.
	AllowEmpty bool     `json:"allow_empty,omitempty"`
	NodeIDs    []string `json:"node_ids"`
	// GroupIDs reference other proxy groups as ordered members, enabling
	// serial (fallback chain) and parallel (url-test / load-balance)
	// composition. References must stay acyclic.
	GroupIDs        []string `json:"group_ids,omitempty"`
	SubscriptionIDs []string `json:"subscription_ids,omitempty"`
	NameKeywords    []string `json:"name_keywords,omitempty"`
	Regions         []string `json:"regions,omitempty"`
	Protocols       []string `json:"protocols,omitempty"`
	States          []string `json:"states,omitempty"`
	MaxLatencyMS    int      `json:"max_latency_ms,omitempty"`
	SortBy          string   `json:"sort_by,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	IncludeDirect   bool     `json:"include_direct"`
	TestURL         string   `json:"test_url,omitempty"`
	IntervalSeconds int      `json:"interval_seconds,omitempty"`
	// Retain keeps last apply's members between Sort and Limit; only
	// meaningful with Limit > 0. See retain.go in this package.
	Retain *pipeline.RetainSpec `json:"retain,omitempty"`
}

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Type is residential, subscription or custom; see group_type.go.
	Type          string     `json:"type"`
	Strategy      string     `json:"strategy"`
	SourceSpec    SourceSpec `json:"source_spec"`
	Enabled       bool       `json:"enabled"`
	EmptyBehavior string     `json:"empty_behavior"`
	// FallbackTargetID is retained for compatibility but is not compiled into
	// the data plane; see the Agent Note on egress chaining.
	FallbackTargetID string `json:"fallback_target_id,omitempty"`
	// DialerProxyGroupID tunnels this group's egress through another group.
	// Every member this group selects is dialled through that group, at any
	// depth. Empty means the group dials out directly.
	DialerProxyGroupID string    `json:"dialer_proxy_group_id,omitempty"`
	Version            int       `json:"version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type CreateRequest struct {
	Name           string     `json:"name"`
	Strategy       string     `json:"strategy"`
	SourceSpec     SourceSpec `json:"source_spec"`
	Enabled        *bool      `json:"enabled,omitempty"`
	EmptyBehavior  string     `json:"empty_behavior,omitempty"`
	FallbackTarget string     `json:"fallback_target_id,omitempty"`
	// DialerProxyGroup is the group id whose exit this group's traffic uses.
	DialerProxyGroup string `json:"dialer_proxy_group_id,omitempty"`
	// AllowSingleNode exempts a create from the "a group is not a handle for one
	// node" rule in rejectSingleNodeGroup.
	//
	// It is deliberately outside the HTTP contract (json:"-"): a caller that could
	// send it would simply send it, and the rule would be advisory. The only
	// legitimate user is a managed group whose membership is decided by another
	// aggregate — a residential channel publishes a pool of vendor sessions and
	// may legitimately hold exactly one (see internal/residential/channel.go).
	// That is not an operator wrapping a node into a group.
	AllowSingleNode bool `json:"-"`
}

type UpdateRequest struct {
	Version        int        `json:"version"`
	Name           string     `json:"name"`
	Strategy       string     `json:"strategy"`
	SourceSpec     SourceSpec `json:"source_spec"`
	Enabled        bool       `json:"enabled"`
	EmptyBehavior  string     `json:"empty_behavior"`
	FallbackTarget string     `json:"fallback_target_id,omitempty"`
	// DialerProxyGroup is the group id whose exit this group's traffic uses.
	DialerProxyGroup string `json:"dialer_proxy_group_id,omitempty"`
	// AllowSingleNode is CreateRequest.AllowSingleNode for an update. An update can
	// shrink a group to one member, which is the same shape the create rule
	// refuses; leaving updates open would make the rule a two-call suggestion.
	// See single_node_rule.go.
	AllowSingleNode bool `json:"-"`
}

type Service struct {
	repository Repository
	reconciler Reconciler
	now        func() time.Time
}

func NewService(repository Repository, reconciler Reconciler) (*Service, error) {
	if repository == nil {
		return nil, errors.New("proxy group repository is required")
	}
	if reconciler == nil {
		return nil, errors.New("proxy group reconciler is required")
	}
	return &Service{repository: repository, reconciler: reconciler, now: time.Now}, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (Group, error) {
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	id, err := newID("group")
	if err != nil {
		return Group{}, err
	}
	normalized, err := s.normalize(ctx, id, request.Name, request.Strategy, request.SourceSpec, enabled, request.EmptyBehavior, request.FallbackTarget, request.DialerProxyGroup, request.AllowSingleNode)
	if err != nil {
		return Group{}, err
	}
	now := s.now().UTC()
	record := store.ProxyGroupRecord{
		ID:                 id,
		Name:               normalized.Name,
		Strategy:           normalized.Strategy,
		SourceSpecJSON:     normalized.SourceSpecJSON,
		RulePipelineJSON:   "{}",
		Enabled:            normalized.Enabled,
		EmptyBehavior:      normalized.EmptyBehavior,
		FallbackTargetID:   normalized.FallbackTargetID,
		DialerProxyGroupID: normalized.DialerProxyGroupID,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	created, err := s.repository.CreateProxyGroup(ctx, record)
	if err != nil {
		return Group{}, mapStoreError(err)
	}
	if err := s.reconciler.Apply(ctx); err != nil {
		return fromRecord(created), fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}
	return fromRecord(created), nil
}

// MembersView is a group's resolved membership, as the data plane would see it.
type MembersView struct {
	GroupID string `json:"group_id"`
	Name    string `json:"name"`
	// Selectable mirrors the data plane rule: only a `manual` group compiles to
	// a Mihomo Selector, which is the only type where an explicit member choice
	// sticks. url-test re-picks by latency, load-balance rotates.
	Selectable bool   `json:"selectable"`
	Strategy   string `json:"strategy"`
	Count      int    `json:"count"`
	// Members are the Mihomo proxy names (`hx-node-<fingerprint prefix>`), i.e.
	// exactly the strings the control API accepts as a selection target.
	Members []string `json:"members"`
	// Now is the member currently selected, read from the **live data plane**.
	// Empty when the data plane is unreachable — callers must treat empty as
	// "unknown", not as "nothing selected".
	Now string `json:"now,omitempty"`
}

// nodeProxyNameOf mirrors the data plane's proxy naming so a member returned
// here can be handed straight back as a selection target. Kept in sync with
// mihomo.nodeProxyName (unexported there); the value is a published contract,
// not an internal detail.
func nodeProxyNameOf(fingerprint string) string {
	fingerprint = strings.TrimSpace(fingerprint)
	if len(fingerprint) > 16 {
		fingerprint = fingerprint[:16]
	}
	return "hx-node-" + fingerprint
}

// Members resolves a group's actual member list from its source spec.
//
// Why this exists: `Get` returns only the *spec* (subscriptions + filters), so a
// caller cannot tell which nodes the group really holds. A consumer that sees
// one member rejected then has no way to know the group still has 150 more, and
// treats the whole group as spent. Resolution is the same code path the compiler
// uses, so this answer cannot drift from what the data plane actually runs.
func (s *Service) Members(ctx context.Context, id string) (MembersView, error) {
	record, err := s.repository.GetProxyGroup(ctx, id)
	if err != nil {
		return MembersView{}, mapStoreError(err)
	}
	group := fromRecord(record)
	spec, err := DecodeSourceSpec(record.SourceSpecJSON)
	if err != nil {
		return MembersView{}, err
	}
	candidates, err := s.repository.ListGroupNodeCandidates(ctx)
	if err != nil {
		return MembersView{}, err
	}
	incumbents, err := s.incumbents(ctx, record.ID)
	if err != nil {
		return MembersView{}, err
	}
	resolution, err := resolveMembers(spec, record.RulePipelineJSON, candidates, incumbents)
	if err != nil {
		return MembersView{}, err
	}
	names := make([]string, 0, len(resolution.NodeIDs))
	for _, nodeID := range resolution.NodeIDs {
		configs, cfgErr := s.repository.ListNodeConfigs(ctx, []string{nodeID})
		if cfgErr != nil || len(configs) == 0 {
			// Keep the ID so the list still reflects the resolved membership;
			// a missing config is a data-plane concern, not a resolution one.
			names = append(names, nodeID)
			continue
		}
		names = append(names, nodeProxyNameOf(configs[0].Fingerprint))
	}
	return MembersView{
		GroupID: group.ID,
		Name:    group.Name,
		// Only a manual (Selector) group honours an explicit selection; url-test
		// re-picks by latency and load-balance rotates, so a chosen member there
		// would be silently undone. Mirrors the data plane's own rule.
		Selectable: group.Strategy == "manual",
		Strategy:   group.Strategy,
		Count:      len(names),
		Members:    names,
		// Now is filled in by the API layer from the live data plane: the stored
		// record is *configuration*, while `now` is runtime state. Keeping them
		// separate is why this field is not read here.
		Now: "",
	}, nil
}

func (s *Service) Get(ctx context.Context, id string) (Group, error) {
	record, err := s.repository.GetProxyGroup(ctx, id)
	if err != nil {
		return Group{}, mapStoreError(err)
	}
	return s.classifyOne(ctx, fromRecord(record))
}

func (s *Service) List(ctx context.Context) ([]Group, error) {
	records, err := s.repository.ListProxyGroups(ctx)
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(records))
	for _, record := range records {
		groups = append(groups, fromRecord(record))
	}
	if err := s.classify(ctx, groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (s *Service) Update(ctx context.Context, id string, request UpdateRequest) (Group, error) {
	if request.Version < 1 {
		return Group{}, fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	existing, err := s.repository.GetProxyGroup(ctx, id)
	if err != nil {
		return Group{}, mapStoreError(err)
	}
	// Existing one-member rows must stay editable even when their membership no
	// longer comes from an explicit node_ids list (a legacy subscription group,
	// or a row created through the old allow_empty hole). legacySingleMember
	// checks the stored shape, not the candidate, so it cannot be used to reach
	// a one-member shape from a larger one.
	allowSingleNode := updateMayKeepSingleNode(request, existing.SourceSpecJSON) ||
		s.legacySingleMember(ctx, existing.SourceSpecJSON)
	normalized, err := s.normalize(ctx, id, request.Name, request.Strategy, request.SourceSpec, request.Enabled, request.EmptyBehavior, request.FallbackTarget, request.DialerProxyGroup, allowSingleNode)
	if err != nil {
		return Group{}, err
	}
	existing.Name = normalized.Name
	existing.Strategy = normalized.Strategy
	existing.SourceSpecJSON = normalized.SourceSpecJSON
	existing.Enabled = normalized.Enabled
	existing.EmptyBehavior = normalized.EmptyBehavior
	existing.FallbackTargetID = normalized.FallbackTargetID
	existing.DialerProxyGroupID = normalized.DialerProxyGroupID
	existing.UpdatedAt = s.now().UTC()
	updated, err := s.repository.UpdateProxyGroup(ctx, existing, request.Version)
	if err != nil {
		return Group{}, mapStoreError(err)
	}
	if err := s.reconciler.Apply(ctx); err != nil {
		return fromRecord(updated), fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}
	return s.classifyOne(ctx, fromRecord(updated))
}

func (s *Service) Delete(ctx context.Context, id string, version int) error {
	if version < 1 {
		return fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	records, err := s.repository.ListProxyGroups(ctx)
	if err != nil {
		return err
	}
	if owners := referencedBy(records, id); len(owners) > 0 {
		names := make([]string, 0, len(owners))
		for _, record := range records {
			for _, owner := range owners {
				if record.ID == owner {
					names = append(names, record.Name)
				}
			}
		}
		return fmt.Errorf("%w: group is referenced by %s", ErrConflict, strings.Join(names, ", "))
	}
	// Being named by another group is not the only way a group can still be in
	// use: a listener points at one directly. That reference is invisible to
	// referencedBy, which only walks group-to-group edges, so a delete used to
	// reach the database and be refused there by the RESTRICT foreign key —
	// correct, but reported as an unmapped SQLite error the API turned into a
	// generic 500. Asking the question here instead keeps the refusal
	// answerable: the caller learns *what* still uses the group.
	//
	// Measured 2026-10-01: the front end's service list offered to delete a
	// service, then deleted the group it found on the listener. Because a
	// pinned listener's proxy_group_id is only a schema anchor shared by every
	// pinned listener, that group was the operator's main subscription group.
	// The foreign key saved it; nothing in this service did.
	listeners, err := s.repository.ListListeners(ctx)
	if err != nil {
		return err
	}
	attached := make([]string, 0, 1)
	for _, record := range listeners {
		if record.ProxyGroupID == id {
			attached = append(attached, record.Name)
		}
	}
	if len(attached) > 0 {
		sort.Strings(attached)
		return fmt.Errorf("%w: %d listener(s) still resolve their egress through it: %s",
			ErrReferenced, len(attached), strings.Join(attached, ", "))
	}
	if err := s.repository.DeleteProxyGroup(ctx, id, version); err != nil {
		return mapStoreError(err)
	}
	if err := s.reconciler.Apply(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrApplyFailed, err)
	}
	return nil
}

type normalizedGroup struct {
	Name               string
	Strategy           string
	SourceSpecJSON     string
	Enabled            bool
	EmptyBehavior      string
	FallbackTargetID   string
	DialerProxyGroupID string
}

func (s *Service) normalize(
	ctx context.Context,
	selfID string,
	name string,
	strategy string,
	spec SourceSpec,
	enabled bool,
	emptyBehavior string,
	fallbackTarget string,
	dialerProxyGroup string,
	allowSingleNode bool,
) (normalizedGroup, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 128 {
		return normalizedGroup{}, fmt.Errorf("%w: name must contain 1 to 128 characters", ErrInvalid)
	}
	for _, reserved := range []string{"DIRECT", "REJECT", "GLOBAL", "COMPATIBLE"} {
		if strings.EqualFold(name, reserved) {
			return normalizedGroup{}, fmt.Errorf("%w: name %q is reserved", ErrInvalid, name)
		}
	}
	// The compiler names a chained group's derived proxies "<node>|via|<group id>".
	// The separator must stay impossible in an operator-supplied name, or a group
	// could be named to collide with a derived proxy and Mihomo would reject the
	// whole document as a duplicate name.
	if strings.Contains(name, derivedProxySeparator()) {
		return normalizedGroup{}, fmt.Errorf("%w: name must not contain %q", ErrInvalid, derivedProxySeparator())
	}
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	if !validStrategy(strategy) {
		return normalizedGroup{}, fmt.Errorf("%w: unsupported strategy %q", ErrInvalid, strategy)
	}
	spec.NodeIDs = deduplicateIDs(spec.NodeIDs)
	spec.GroupIDs = deduplicateIDs(spec.GroupIDs)
	spec.SubscriptionIDs = deduplicateIDs(spec.SubscriptionIDs)
	// Validated after normalisation: a blank subscription id must not satisfy
	// "subscription-sourced" and then vanish.
	if err := validateRetain(spec); err != nil {
		return normalizedGroup{}, err
	}
	if len(spec.NodeIDs) == 0 && len(spec.GroupIDs) == 0 && len(spec.SubscriptionIDs) == 0 && !spec.IncludeDirect && !spec.AllowEmpty {
		return normalizedGroup{}, fmt.Errorf("%w: at least one node, group, subscription, or DIRECT is required", ErrInvalid)
	}
	// A group id in node_ids is a category error, not a membership problem, and
	// it has to be reported as one before the count is judged: "a group needs
	// more than one node" would send the caller looking for a second node.
	// See reference_kind.go.
	if err := rejectMisplacedReferences(spec); err != nil {
		return normalizedGroup{}, err
	}
	// Node existence is checked before the size rule: "this id is not a node"
	// and "this group has one member" are different answers, and the size rule
	// would otherwise mask a stale or invented id behind "add another node".
	if len(spec.NodeIDs) > 0 {
		nodes, err := s.repository.ListNodeConfigs(ctx, spec.NodeIDs)
		if err != nil {
			return normalizedGroup{}, err
		}
		if len(nodes) != len(spec.NodeIDs) {
			// Name the ones that are missing: a caller that pasted a stale or
			// invented id cannot act on "one or more".
			found := make(map[string]bool, len(nodes))
			for _, node := range nodes {
				found[node.ID] = true
			}
			missing := make([]string, 0, len(spec.NodeIDs))
			for _, id := range spec.NodeIDs {
				if !found[id] {
					missing = append(missing, id)
				}
			}
			return normalizedGroup{}, fmt.Errorf(
				"%w: node_ids 里有 %d 个节点不存在或已停用（node_ids 只接受 GET /api/v1/nodes 返回的节点 id）: %s",
				ErrInvalid, len(missing), strings.Join(missing, ", "),
			)
		}
	}
	// Checked after NodeIDs are deduplicated and after subscription members are
	// resolved, so ["n1","n1"], one subscription holding one node, and
	// limit=1 over a pool all count as the one-member group they really are.
	// See single_node_rule.go.
	if err := s.rejectSingleMemberGroup(ctx, spec, allowSingleNode); err != nil {
		return normalizedGroup{}, err
	}
	// Reject a chained group that also offers DIRECT: DIRECT is a builtin and
	// cannot carry a dialer, so that member would genuinely bypass the chain.
	// Silently honouring it would leak traffic outside the path the operator
	// asked for.
	dialerProxyGroup = strings.TrimSpace(dialerProxyGroup)
	if dialerProxyGroup != "" && spec.IncludeDirect {
		return normalizedGroup{}, fmt.Errorf("%w: a chained group cannot also include DIRECT, which would bypass the chain", ErrInvalid)
	}
	// The dialer edge is a reference like any member edge, so it must be
	// validated and cycle-checked in the same pass. A group with only node_ids
	// plus a dialer would otherwise skip validation entirely.
	if len(spec.GroupIDs) > 0 || dialerProxyGroup != "" {
		records, err := s.repository.ListProxyGroups(ctx)
		if err != nil {
			return normalizedGroup{}, err
		}
		known := make(map[string]store.ProxyGroupRecord, len(records))
		for _, record := range records {
			known[record.ID] = record
		}
		for _, groupID := range spec.GroupIDs {
			if groupID == selfID {
				return normalizedGroup{}, fmt.Errorf("%w: a group cannot reference itself", ErrInvalid)
			}
			referenced, exists := known[groupID]
			if !exists {
				return normalizedGroup{}, fmt.Errorf("%w: referenced group %q does not exist", ErrInvalid, groupID)
			}
			if !referenced.Enabled {
				return normalizedGroup{}, fmt.Errorf("%w: referenced group %q is disabled", ErrInvalid, referenced.Name)
			}
		}
		if dialerProxyGroup != "" {
			if dialerProxyGroup == selfID {
				return normalizedGroup{}, fmt.Errorf("%w: a group cannot use itself as its dialer", ErrInvalid)
			}
			referenced, exists := known[dialerProxyGroup]
			if !exists {
				return normalizedGroup{}, fmt.Errorf("%w: dialer group %q does not exist", ErrInvalid, dialerProxyGroup)
			}
			if !referenced.Enabled {
				return normalizedGroup{}, fmt.Errorf("%w: dialer group %q is disabled", ErrInvalid, referenced.Name)
			}
		}
		if cycle := findCycle(groupEdges(records, selfID, spec, dialerProxyGroup), selfID); cycle != nil {
			names := make([]string, 0, len(cycle))
			for _, member := range cycle {
				if record, exists := known[member]; exists {
					names = append(names, record.Name)
				} else if member == selfID {
					names = append(names, name)
				} else {
					names = append(names, member)
				}
			}
			return normalizedGroup{}, fmt.Errorf("%w: group references form a cycle: %s", ErrInvalid, strings.Join(names, " -> "))
		}
	}
	for _, subscriptionID := range spec.SubscriptionIDs {
		record, err := s.repository.GetSubscription(ctx, subscriptionID)
		if errors.Is(err, store.ErrNotFound) {
			return normalizedGroup{}, fmt.Errorf("%w: subscription %q does not exist", ErrInvalid, subscriptionID)
		}
		if err != nil {
			return normalizedGroup{}, err
		}
		if !record.Enabled {
			return normalizedGroup{}, fmt.Errorf("%w: subscription %q is disabled", ErrInvalid, subscriptionID)
		}
	}
	spec.NameKeywords = normalizeValues(spec.NameKeywords, false)
	spec.Regions = normalizeValues(spec.Regions, true)
	spec.Protocols = normalizeValues(spec.Protocols, true)
	spec.States = normalizeValues(spec.States, true)
	for _, state := range spec.States {
		if !validNodeState(state) {
			return normalizedGroup{}, fmt.Errorf("%w: unsupported node state %q", ErrInvalid, state)
		}
	}
	if spec.MaxLatencyMS < 0 || spec.MaxLatencyMS > 60000 {
		return normalizedGroup{}, fmt.Errorf("%w: max_latency_ms must be between 0 and 60000", ErrInvalid)
	}
	spec.SortBy = strings.ToLower(strings.TrimSpace(spec.SortBy))
	if spec.SortBy == "" && len(spec.SubscriptionIDs) > 0 {
		spec.SortBy = "latency"
	}
	if spec.SortBy != "" && !slices.Contains(SupportedSortOrders(), spec.SortBy) {
		return normalizedGroup{}, fmt.Errorf("%w: sort_by must be one of %s", ErrInvalid, strings.Join(SupportedSortOrders(), ", "))
	}
	if spec.Limit < 0 || spec.Limit > 500 {
		return normalizedGroup{}, fmt.Errorf("%w: limit must be between 0 and 500", ErrInvalid)
	}
	if strategy != "manual" {
		spec.TestURL = strings.TrimSpace(spec.TestURL)
		if spec.TestURL == "" {
			spec.TestURL = defaultTestURL
		}
		if !strings.HasPrefix(spec.TestURL, "http://") && !strings.HasPrefix(spec.TestURL, "https://") {
			return normalizedGroup{}, fmt.Errorf("%w: test_url must use HTTP or HTTPS", ErrInvalid)
		}
		if spec.IntervalSeconds == 0 {
			spec.IntervalSeconds = 300
		}
		if spec.IntervalSeconds < 30 || spec.IntervalSeconds > 86400 {
			return normalizedGroup{}, fmt.Errorf("%w: interval_seconds must be between 30 and 86400", ErrInvalid)
		}
	} else {
		spec.TestURL = ""
		spec.IntervalSeconds = 0
	}
	emptyBehavior = strings.ToLower(strings.TrimSpace(emptyBehavior))
	if emptyBehavior == "" {
		emptyBehavior = "fail-closed"
	}
	if !slices.Contains(SupportedEmptyBehaviors(), emptyBehavior) {
		return normalizedGroup{}, fmt.Errorf("%w: unsupported empty_behavior %q", ErrInvalid, emptyBehavior)
	}
	fallbackTarget = strings.TrimSpace(fallbackTarget)
	encoded, err := json.Marshal(spec)
	if err != nil {
		return normalizedGroup{}, fmt.Errorf("encode source spec: %w", err)
	}
	return normalizedGroup{
		Name:               name,
		Strategy:           strategy,
		SourceSpecJSON:     string(encoded),
		Enabled:            enabled,
		EmptyBehavior:      emptyBehavior,
		FallbackTargetID:   fallbackTarget,
		DialerProxyGroupID: dialerProxyGroup,
	}, nil
}

// derivedProxySeparator returns the string the data-plane compiler uses to join
// a node name with the id of the group that chains it. It is a function rather
// than a constant so the validator states the constraint it enforces instead of
// importing the data plane, which the layering rules forbid.
func derivedProxySeparator() string {
	return "|via|"
}

// SupportedStrategies lists the selection strategies a proxy group may use. It
// is shared by the validator and the capability catalog so the published list
// cannot drift from the accepted one.
func SupportedStrategies() []string {
	return []string{"manual", "url-test", "fallback", "round-robin", "consistent-hashing", "sticky-sessions"}
}

// SupportedEmptyBehaviors lists what a group does when its source selects no
// node. "fail-closed" refuses traffic; "direct" falls back to a direct dial.
func SupportedEmptyBehaviors() []string {
	return []string{"fail-closed", "direct"}
}

// SupportedNodeStates lists the node lifecycle states a source spec may filter
// on. They mirror the probe state machine, not a separate vocabulary.
func SupportedNodeStates() []string {
	return []string{"candidate", "healthy", "degraded", "quarantined"}
}

// SupportedSortOrders lists the accepted source-spec sort keys.
func SupportedSortOrders() []string {
	return []string{"latency", "name"}
}

func validStrategy(value string) bool {
	return slices.Contains(SupportedStrategies(), value)
}

func deduplicateIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeValues(values []string, lowercase bool) []string {
	values = deduplicateIDs(values)
	for index := range values {
		if lowercase {
			values[index] = strings.ToLower(values[index])
		}
	}
	return values
}

func validNodeState(value string) bool {
	return slices.Contains(SupportedNodeStates(), value)
}

func fromRecord(record store.ProxyGroupRecord) Group {
	var spec SourceSpec
	_ = json.Unmarshal([]byte(record.SourceSpecJSON), &spec)
	return Group{
		ID:                 record.ID,
		Name:               record.Name,
		Type:               typeFromSpec(spec),
		Strategy:           record.Strategy,
		SourceSpec:         spec,
		Enabled:            record.Enabled,
		EmptyBehavior:      record.EmptyBehavior,
		FallbackTargetID:   record.FallbackTargetID,
		DialerProxyGroupID: record.DialerProxyGroupID,
		Version:            record.Version,
		CreatedAt:          record.CreatedAt,
		UpdatedAt:          record.UpdatedAt,
	}
}

func mapStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}

func newID(prefix string) (string, error) {
	var buffer [12]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(buffer[:]), nil
}
