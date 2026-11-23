package node

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

var (
	ErrNotFound         = errors.New("node not found")
	ErrCheckUnavailable = errors.New("node quality check is unavailable")
)

const (
	defaultTestURL     = "http://cp.cloudflare.com/generate_204"
	defaultTestTimeout = 10 * time.Second
)

type Repository interface {
	ListNodes(context.Context, store.NodeFilter) ([]store.NodeRecord, error)
	CountNodes(context.Context, store.NodeFilter) (int, error)
	ListActiveNodeSources(context.Context, []string) ([]store.NodeSourceRecord, error)
	GetNode(context.Context, string) (store.NodeRecord, error)
	GetNodeConfig(context.Context, string) (store.NodeConfigRecord, error)
	RecordNodeQualityResult(context.Context, store.NodeQualityResult) (store.NodeRecord, error)
	RecordNodeHealthResult(context.Context, store.NodeQualityResult) error
	ListLatestNodeHealthResults(context.Context, []string) ([]store.NodeQualityCheckRecord, error)
	DueNodeIDs(context.Context, time.Time, int) ([]string, error)
	SetNodeLifecycleState(context.Context, string, string) error
	GetMetadata(context.Context, string) (string, error)
	SetMetadata(context.Context, string, string) error
}

type Prober interface {
	Apply(context.Context) error
	TestProxy(context.Context, string, string, time.Duration) (int, error)
}

type Option func(*Service) error

func WithProber(prober Prober) Option {
	return func(service *Service) error {
		if prober == nil {
			return errors.New("node prober is required")
		}
		service.prober = prober
		return nil
	}
}

type Node struct {
	ID                       string     `json:"id"`
	Fingerprint              string     `json:"fingerprint"`
	DisplayName              string     `json:"display_name"`
	Protocol                 string     `json:"protocol"`
	LifecycleState           string     `json:"lifecycle_state"`
	FirstSeenAt              time.Time  `json:"first_seen_at"`
	LastSeenAt               time.Time  `json:"last_seen_at"`
	RetiredAt                *time.Time `json:"retired_at,omitempty"`
	LastCheckedAt            *time.Time `json:"last_checked_at,omitempty"`
	LastLatencyMS            *int       `json:"last_latency_ms,omitempty"`
	LastErrorCode            string     `json:"last_error_code,omitempty"`
	LastErrorMessage         string     `json:"last_error_message,omitempty"`
	ConsecutiveProbeFailures int        `json:"consecutive_probe_failures"`
	// NextProbeAt is when the class-specific backoff lets this node be probed
	// again. Nil means no backoff is in force.
	NextProbeAt *time.Time `json:"next_probe_at,omitempty"`
	// ProbePaused means the probe policy has stopped probing this node (a spent
	// pool member, or a retired one). It is what distinguishes "we are waiting
	// longer between probes" from "we no longer intend to probe it".
	ProbePaused  bool          `json:"probe_paused"`
	Version      int           `json:"version"`
	SourceCount  int           `json:"source_count"`
	Sources      []Source      `json:"sources"`
	HealthChecks []HealthCheck `json:"health_checks"`
}

type Source struct {
	SubscriptionID   string `json:"subscription_id"`
	SubscriptionName string `json:"subscription_name"`
	SourceName       string `json:"source_name"`
}

type HealthCheck struct {
	TargetID   string     `json:"target_id"`
	TargetName string     `json:"target_name"`
	URL        string     `json:"url"`
	Success    bool       `json:"success"`
	LatencyMS  *int       `json:"latency_ms,omitempty"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	ErrorCode  string     `json:"error_code,omitempty"`
}

type CheckResult struct {
	Node      Node      `json:"node"`
	Success   bool      `json:"success"`
	LatencyMS *int      `json:"latency_ms,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	TestURL   string    `json:"test_url"`
	ErrorCode string    `json:"error_code,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type CheckProgress struct {
	Completed int         `json:"completed"`
	Total     int         `json:"total"`
	NodeID    string      `json:"node_id"`
	Result    CheckResult `json:"result"`
}

type Filter struct {
	Search   string
	Protocol string
	State    string
	Limit    int
	Offset   int
	// SkipHealthChecks drops the per-node health-target samples from the
	// result. They are the single most expensive part of a node page: attaching
	// them for 1000 nodes costs ~240ms on a 2750-node inventory (105k quality
	// rows), while the members list of a proxy service reads only the node's
	// lifecycle state, latency and sources. Callers that do not render health
	// targets must ask for the cheaper projection instead of paying for a field
	// they discard — see .agents/notes/implemented/feature/2026-10-14-node-list-lean-projection.md.
	SkipHealthChecks bool
}

type Service struct {
	repository Repository
	prober     Prober
	now        func() time.Time
}

func NewService(repository Repository, options ...Option) (*Service, error) {
	if repository == nil {
		return nil, errors.New("node repository is required")
	}
	service := &Service{repository: repository, now: time.Now}
	for _, option := range options {
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) List(ctx context.Context, filter Filter) ([]Node, error) {
	records, err := s.repository.ListNodes(ctx, store.NodeFilter{
		Search:   strings.TrimSpace(filter.Search),
		Protocol: strings.ToLower(strings.TrimSpace(filter.Protocol)),
		State:    strings.ToLower(strings.TrimSpace(filter.State)),
		Limit:    filter.Limit,
		Offset:   filter.Offset,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	sourceRecords, err := s.repository.ListActiveNodeSources(ctx, ids)
	if err != nil {
		return nil, err
	}
	sourcesByNode := make(map[string][]Source, len(records))
	for _, record := range sourceRecords {
		sourcesByNode[record.NodeID] = append(sourcesByNode[record.NodeID], Source{
			SubscriptionID:   record.SubscriptionID,
			SubscriptionName: record.SubscriptionName,
			SourceName:       record.SourceName,
		})
	}
	items := make([]Node, 0, len(records))
	for _, record := range records {
		item := fromRecord(record)
		item.Sources = sourcesByNode[record.ID]
		if item.Sources == nil {
			item.Sources = []Source{}
		}
		items = append(items, item)
	}
	if filter.SkipHealthChecks {
		for index := range items {
			items[index].HealthChecks = []HealthCheck{}
		}
		return items, nil
	}
	settings, err := s.QualitySettings(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.attachHealthChecks(ctx, items, settings); err != nil {
		return nil, err
	}
	return items, nil
}

// Count returns how many nodes match the filter, ignoring pagination. It backs
// the list API's total, which lets a client fetch the remaining pages
// concurrently instead of walking offsets one round trip at a time.
func (s *Service) Count(ctx context.Context, filter Filter) (int, error) {
	return s.repository.CountNodes(ctx, store.NodeFilter{
		Search:   strings.TrimSpace(filter.Search),
		Protocol: strings.ToLower(strings.TrimSpace(filter.Protocol)),
		State:    strings.ToLower(strings.TrimSpace(filter.State)),
	})
}

// Disable removes a node from every compiled configuration until an
// administrator re-enables it. The dataplane is re-applied immediately.
func (s *Service) Disable(ctx context.Context, id string) (Node, error) {
	return s.setLifecycle(ctx, id, "disabled")
}

// Enable returns a disabled node to the candidate state; the next probe
// decides its health.
func (s *Service) Enable(ctx context.Context, id string) (Node, error) {
	return s.setLifecycle(ctx, id, "candidate")
}

func (s *Service) setLifecycle(ctx context.Context, id, state string) (Node, error) {
	if err := s.repository.SetNodeLifecycleState(ctx, id, state); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Node{}, ErrNotFound
		}
		return Node{}, err
	}
	updated, err := s.Get(ctx, id)
	if err != nil {
		return Node{}, err
	}
	if s.prober != nil {
		if applyErr := s.prober.Apply(ctx); applyErr != nil {
			return updated, fmt.Errorf("node state saved but dataplane apply failed: %w", applyErr)
		}
	}
	return updated, nil
}

func (s *Service) Get(ctx context.Context, id string) (Node, error) {
	record, err := s.repository.GetNode(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, err
	}
	return s.nodeWithSources(ctx, record)
}

func (s *Service) nodeWithSources(ctx context.Context, record store.NodeRecord) (Node, error) {
	item := fromRecord(record)
	item.Sources = []Source{}
	records, err := s.repository.ListActiveNodeSources(ctx, []string{record.ID})
	if err != nil {
		return Node{}, err
	}
	for _, source := range records {
		item.Sources = append(item.Sources, Source{
			SubscriptionID:   source.SubscriptionID,
			SubscriptionName: source.SubscriptionName,
			SourceName:       source.SourceName,
		})
	}
	settings, err := s.QualitySettings(ctx)
	if err != nil {
		return Node{}, err
	}
	items := []Node{item}
	if err := s.attachHealthChecks(ctx, items, settings); err != nil {
		return Node{}, err
	}
	item = items[0]
	return item, nil
}

func (s *Service) attachHealthChecks(ctx context.Context, items []Node, settings QualitySettings) error {
	ids := make([]string, 0, len(items))
	for index := range items {
		ids = append(ids, items[index].ID)
		items[index].HealthChecks = []HealthCheck{}
	}
	records, err := s.repository.ListLatestNodeHealthResults(ctx, ids)
	if err != nil {
		return err
	}
	targetsByURL := make(map[string]HealthTarget)
	for _, target := range settings.HealthTargets {
		if target.Enabled {
			targetsByURL[target.URL] = target
		}
	}
	checksByNode := make(map[string][]HealthCheck)
	for _, record := range records {
		target, exists := targetsByURL[record.TestURL]
		if !exists {
			continue
		}
		checkedAt := record.CheckedAt
		checksByNode[record.NodeID] = append(checksByNode[record.NodeID], HealthCheck{
			TargetID: target.ID, TargetName: target.Name, URL: target.URL,
			Success: record.Success, LatencyMS: record.LatencyMS, CheckedAt: &checkedAt, ErrorCode: record.ErrorCode,
		})
	}
	for index := range items {
		if checks := checksByNode[items[index].ID]; checks != nil {
			items[index].HealthChecks = checks
		}
	}
	return nil
}

func (s *Service) Check(ctx context.Context, id string) (CheckResult, error) {
	if s.prober == nil {
		return CheckResult{}, ErrCheckUnavailable
	}
	config, err := s.repository.GetNodeConfig(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return CheckResult{}, ErrNotFound
	}
	if err != nil {
		return CheckResult{}, err
	}
	if config.LifecycleState == "retired" || config.LifecycleState == "disabled" {
		return CheckResult{}, fmt.Errorf("%w: node is %s", ErrCheckUnavailable, config.LifecycleState)
	}
	if err := s.prober.Apply(ctx); err != nil {
		return CheckResult{}, fmt.Errorf("prepare Mihomo for node check: %w", err)
	}
	settings, err := s.QualitySettings(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	return s.checkPrepared(ctx, id, settings)
}

func (s *Service) checkPrepared(ctx context.Context, id string, settings QualitySettings) (CheckResult, error) {
	config, err := s.repository.GetNodeConfig(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return CheckResult{}, ErrNotFound
	}
	if err != nil {
		return CheckResult{}, err
	}
	if config.LifecycleState == "retired" || config.LifecycleState == "disabled" {
		return CheckResult{}, fmt.Errorf("%w: node is %s", ErrCheckUnavailable, config.LifecycleState)
	}
	checkedAt := s.now().UTC()
	testURL := strings.TrimSpace(settings.TestURL)
	if testURL == "" {
		testURL = defaultTestURL
	}
	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTestTimeout
	}
	latency, probeErr := s.prober.TestProxy(ctx, config.Fingerprint, testURL, timeout)
	if probeErr != nil && isDataplaneUnavailable(probeErr) {
		// A dead control socket is a dataplane outage, not a property of this
		// node: surface the error instead of polluting the quality history.
		return CheckResult{}, fmt.Errorf("%w: %s", ErrCheckUnavailable, sanitizeProbeError(probeErr))
	}
	quality := store.NodeQualityResult{
		NodeID:    id,
		CheckedAt: checkedAt,
		Success:   probeErr == nil,
		TestURL:   testURL,
	}
	if probeErr == nil {
		quality.LatencyMS = &latency
	} else {
		quality.ErrorCode = classifyProbeError(probeErr)
		quality.ErrorMessage = sanitizeProbeError(probeErr)
	}
	record, err := s.repository.RecordNodeQualityResult(ctx, quality)
	if errors.Is(err, store.ErrNotFound) {
		return CheckResult{}, ErrNotFound
	}
	if err != nil {
		return CheckResult{}, err
	}
	if probeErr == nil {
		if err := s.checkHealthTargets(ctx, config, settings, quality); err != nil {
			return CheckResult{}, err
		}
	}
	checkedNode, err := s.nodeWithSources(ctx, record)
	if err != nil {
		return CheckResult{}, err
	}
	result := CheckResult{
		Node:      checkedNode,
		Success:   probeErr == nil,
		LatencyMS: quality.LatencyMS,
		CheckedAt: checkedAt,
		TestURL:   testURL,
		ErrorCode: quality.ErrorCode,
		Error:     quality.ErrorMessage,
	}
	return result, nil
}

func (s *Service) checkHealthTargets(ctx context.Context, config store.NodeConfigRecord, settings QualitySettings, primary store.NodeQualityResult) error {
	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	for _, target := range settings.HealthTargets {
		if !target.Enabled {
			continue
		}
		if target.URL == primary.TestURL {
			continue
		}
		checkedAt := s.now().UTC()
		latency, probeErr := s.prober.TestProxy(ctx, config.Fingerprint, target.URL, timeout)
		result := store.NodeQualityResult{NodeID: config.ID, CheckedAt: checkedAt, Success: probeErr == nil, TestURL: target.URL}
		if probeErr == nil {
			result.LatencyMS = &latency
		} else {
			result.ErrorCode = classifyProbeError(probeErr)
			result.ErrorMessage = sanitizeProbeError(probeErr)
		}
		if err := s.repository.RecordNodeHealthResult(ctx, result); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) CheckMany(ctx context.Context, ids []string) ([]CheckResult, error) {
	results := make([]CheckResult, 0)
	err := s.CheckManyProgress(ctx, ids, func(progress CheckProgress) error {
		results = append(results, progress.Result)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(results, func(left, right int) bool {
		return indexOfID(ids, results[left].Node.ID) < indexOfID(ids, results[right].Node.ID)
	})
	return results, nil
}

func (s *Service) CheckManyProgress(ctx context.Context, ids []string, notify func(CheckProgress) error) error {
	if s.prober == nil {
		return ErrCheckUnavailable
	}
	ids = deduplicateIDs(ids)
	if len(ids) == 0 {
		records, err := s.repository.ListNodes(ctx, store.NodeFilter{Limit: 100})
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.LifecycleState != "disabled" && record.LifecycleState != "retired" {
				ids = append(ids, record.ID)
			}
		}
	}
	if len(ids) > 1000 {
		return fmt.Errorf("at most 1000 nodes can be checked at once")
	}
	if err := s.prober.Apply(ctx); err != nil {
		return fmt.Errorf("prepare Mihomo for node checks: %w", err)
	}
	settings, err := s.QualitySettings(ctx)
	if err != nil {
		return err
	}
	jobs := make(chan string)
	results := make(chan CheckResult, len(ids))
	workerCount := settings.ProbeConcurrency
	if len(ids) < workerCount {
		workerCount = len(ids)
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for id := range jobs {
				result, err := s.checkPrepared(ctx, id, settings)
				if err != nil {
					result = CheckResult{Node: Node{ID: id, Sources: []Source{}}, Success: false, CheckedAt: s.now().UTC(), ErrorCode: classifyProbeError(err), Error: sanitizeProbeError(err)}
				}
				select {
				case results <- result:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, id := range ids {
			select {
			case jobs <- id:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	completed := 0
	for {
		select {
		case result, ok := <-results:
			if !ok {
				return nil
			}
			completed++
			if notify != nil {
				if err := notify(CheckProgress{Completed: completed, Total: len(ids), NodeID: result.Node.ID, Result: result}); err != nil {
					return err
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func indexOfID(ids []string, id string) int {
	for index, candidate := range ids {
		if candidate == id {
			return index
		}
	}
	return len(ids)
}

func deduplicateIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// CheckDue tests every node whose last check is older than the configured
// interval. Interval, batch size, test URL, and timeout all come from the
// administrator-editable quality settings.
func (s *Service) CheckDue(ctx context.Context) ([]CheckResult, error) {
	if s.prober == nil {
		return nil, ErrCheckUnavailable
	}
	settings, err := s.QualitySettings(ctx)
	if err != nil {
		return nil, err
	}
	interval := time.Duration(settings.CheckIntervalSeconds) * time.Second
	ids, err := s.repository.DueNodeIDs(ctx, s.now().UTC().Add(-interval), settings.BatchSize)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := s.prober.Apply(ctx); err != nil {
		return nil, fmt.Errorf("prepare Mihomo for node checks: %w", err)
	}
	results := make([]CheckResult, 0, len(ids))
	for _, id := range ids {
		result, checkErr := s.checkPrepared(ctx, id, settings)
		if checkErr != nil {
			if errors.Is(checkErr, context.Canceled) ||
				errors.Is(checkErr, context.DeadlineExceeded) ||
				errors.Is(checkErr, ErrCheckUnavailable) {
				return results, checkErr
			}
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func fromRecord(record store.NodeRecord) Node {
	return Node{
		ID:                       record.ID,
		Fingerprint:              record.Fingerprint,
		DisplayName:              record.DisplayName,
		Protocol:                 record.Protocol,
		LifecycleState:           record.LifecycleState,
		FirstSeenAt:              record.FirstSeenAt,
		LastSeenAt:               record.LastSeenAt,
		RetiredAt:                record.RetiredAt,
		LastCheckedAt:            record.LastCheckedAt,
		LastLatencyMS:            record.LastLatencyMS,
		LastErrorCode:            record.LastErrorCode,
		LastErrorMessage:         record.LastErrorMessage,
		ConsecutiveProbeFailures: record.ConsecutiveProbeFailures,
		NextProbeAt:              record.NextProbeAt,
		ProbePaused:              record.ProbePaused,
		Version:                  record.Version,
		SourceCount:              record.SourceCount,
	}
}

// isDataplaneUnavailable reports whether a probe failure was caused by the
// Mihomo process or control socket rather than the tested node. The prober is
// an interface, so detection is based on the stable error text.
func isDataplaneUnavailable(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "mihomo is not running") || strings.Contains(message, "dial unix")
}

func classifyProbeError(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case isDataplaneUnavailable(err):
		return "dataplane_down"
	case strings.Contains(message, "status 404"):
		return "proxy_not_found"
	case strings.Contains(message, "delay test"), strings.Contains(message, "status 503"):
		return "node_unreachable"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return "timeout"
	case strings.Contains(message, "connection refused"), strings.Contains(message, "network is unreachable"):
		return "connect_failed"
	case strings.Contains(message, "status"):
		return "http_failed"
	default:
		return "probe_failed"
	}
}

func sanitizeProbeError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) > 256 {
		message = message[:256]
	}
	return message
}
