package subscription

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/HengXin666/hx-pxy/internal/cron"
	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type RefreshResult struct {
	SubscriptionID string        `json:"subscription_id"`
	SnapshotID     string        `json:"snapshot_id"`
	Changed        bool          `json:"changed"`
	ContentHash    string        `json:"content_hash"`
	Size           int64         `json:"size"`
	DetectedFormat string        `json:"detected_format"`
	EstimatedNodes int           `json:"estimated_nodes"`
	FetchedAt      time.Time     `json:"fetched_at"`
	FetchMetadata  FetchMetadata `json:"fetch_metadata"`
}

type BatchRefreshResult struct {
	SubscriptionID string         `json:"subscription_id"`
	Success        bool           `json:"success"`
	Result         *RefreshResult `json:"result,omitempty"`
	Error          string         `json:"error,omitempty"`
}

func (s *Service) RefreshMany(ctx context.Context, ids []string) ([]BatchRefreshResult, error) {
	ids = uniqueSubscriptionIDs(ids)
	if len(ids) == 0 {
		records, err := s.repository.ListSubscriptions(ctx, 100, 0)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if record.Enabled {
				ids = append(ids, record.ID)
			}
		}
	}
	if len(ids) > 100 {
		return nil, fmt.Errorf("at most 100 subscriptions can be refreshed at once")
	}
	type indexedResult struct {
		index  int
		result BatchRefreshResult
	}
	jobs := make(chan int)
	results := make(chan indexedResult, len(ids))
	workerCount := 4
	if len(ids) < workerCount {
		workerCount = len(ids)
	}
	for range workerCount {
		go func() {
			for index := range jobs {
				result, err := s.Refresh(ctx, ids[index])
				item := BatchRefreshResult{SubscriptionID: ids[index], Success: err == nil}
				if err != nil {
					item.Error = sanitizeBatchError(err)
				} else {
					item.Result = &result
				}
				results <- indexedResult{index: index, result: item}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range ids {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	ordered := make([]BatchRefreshResult, len(ids))
	for range ids {
		select {
		case item := <-results:
			ordered[item.index] = item.result
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return ordered, nil
}

func uniqueSubscriptionIDs(ids []string) []string {
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

func sanitizeBatchError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) > 256 {
		message = message[:256]
	}
	return message
}

type ParseSummary struct {
	DetectedFormat string              `json:"detected_format"`
	EstimatedNodes int                 `json:"estimated_nodes"`
	ParsedNodes    int                 `json:"parsed_nodes"`
	FailedNodes    int                 `json:"failed_nodes"`
	Failures       []nodeparse.Failure `json:"failures,omitempty"`
}

func (s *Service) Refresh(ctx context.Context, id string) (result RefreshResult, resultErr error) {
	if s.loader == nil || strings.TrimSpace(s.snapshotDirectory) == "" {
		return RefreshResult{}, errors.New("subscription refresh is not configured")
	}
	unlock := s.refreshLocks.lock(id)
	defer unlock()
	defer func() {
		if resultErr != nil || result.SnapshotID == "" || s.reconciler == nil {
			return
		}
		if err := s.reconciler.Apply(ctx); err != nil {
			resultErr = fmt.Errorf("subscription refreshed but dataplane apply failed: %w", err)
		}
	}()

	record, err := s.repository.GetSubscription(ctx, id)
	if err != nil {
		return RefreshResult{}, mapRepositoryError(err)
	}
	if !record.Enabled {
		return RefreshResult{}, fmt.Errorf("%w: disabled subscription cannot be refreshed", ErrInvalid)
	}
	config, err := s.decryptSourceConfig(record)
	if err != nil {
		return RefreshResult{}, err
	}

	condition, latestSnapshot, err := s.fetchCondition(ctx, record.LastSuccessSnapshotID)
	if err != nil {
		return RefreshResult{}, err
	}
	fetchedAt := s.now().UTC()
	nextSuccessAt := nextScheduledRefresh(record, fetchedAt)
	fetchResult, err := s.loader.Load(ctx, SourceType(record.SourceType), config, condition)
	if err != nil {
		return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, err)
	}
	metadataJSON, err := json.Marshal(fetchResult.Metadata)
	if err != nil {
		return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, fmt.Errorf("encode fetch metadata: %w", err))
	}

	if fetchResult.NotModified {
		if latestSnapshot.ID == "" {
			return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, errors.New("subscription returned not modified without a previous snapshot"))
		}
		if s.parser != nil {
			content, readErr := os.ReadFile(latestSnapshot.ArtifactPath)
			if readErr != nil {
				return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, fmt.Errorf("read previous subscription snapshot: %w", readErr))
			}
			parseSummary, imports, parseErr := s.parseNodeImports(ctx, content, fetchResult.Metadata.ContentType, SourceType(record.SourceType), config)
			if parseErr != nil {
				return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, parseErr)
			}
			parseSummaryJSON, marshalErr := json.Marshal(parseSummary)
			if marshalErr != nil {
				return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, fmt.Errorf("encode parse summary: %w", marshalErr))
			}
			latestSnapshot.FetchedAt = fetchedAt
			latestSnapshot.NextRefreshAt = nextSuccessAt
			latestSnapshot.NodeCount = len(imports)
			latestSnapshot.ParseSummaryJSON = string(parseSummaryJSON)
			parsedRepository := s.repository.(ParsedNodeRepository)
			if activateErr := parsedRepository.ActivateParsedSubscriptionSnapshot(ctx, latestSnapshot, imports, string(metadataJSON)); activateErr != nil {
				return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, mapRepositoryError(activateErr))
			}
			return refreshResultFromSnapshot(latestSnapshot, false, fetchedAt, fetchResult.Metadata), nil
		}
		if err := s.repository.ActivateSubscriptionSnapshot(ctx, id, latestSnapshot.ID, fetchedAt, nextSuccessAt, string(metadataJSON)); err != nil {
			return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, mapRepositoryError(err))
		}
		return refreshResultFromSnapshot(latestSnapshot, false, fetchedAt, fetchResult.Metadata), nil
	}

	digest := sha256.Sum256(fetchResult.Content)
	contentHash := hex.EncodeToString(digest[:])
	existing, err := s.repository.GetSubscriptionSnapshotByHash(ctx, id, contentHash)
	if err == nil {
		if s.parser != nil {
			parseSummary, imports, parseErr := s.parseNodeImports(ctx, fetchResult.Content, fetchResult.Metadata.ContentType, SourceType(record.SourceType), config)
			if parseErr != nil {
				return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, parseErr)
			}
			parseSummaryJSON, marshalErr := json.Marshal(parseSummary)
			if marshalErr != nil {
				return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, fmt.Errorf("encode parse summary: %w", marshalErr))
			}
			existing.FetchedAt = fetchedAt
			existing.NextRefreshAt = nextSuccessAt
			existing.NodeCount = len(imports)
			existing.ParseSummaryJSON = string(parseSummaryJSON)
			parsedRepository := s.repository.(ParsedNodeRepository)
			if activateErr := parsedRepository.ActivateParsedSubscriptionSnapshot(ctx, existing, imports, string(metadataJSON)); activateErr != nil {
				return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, mapRepositoryError(activateErr))
			}
			return refreshResultFromSnapshot(existing, false, fetchedAt, fetchResult.Metadata), nil
		}
		if err := s.repository.ActivateSubscriptionSnapshot(ctx, id, existing.ID, fetchedAt, nextSuccessAt, string(metadataJSON)); err != nil {
			return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, mapRepositoryError(err))
		}
		return refreshResultFromSnapshot(existing, false, fetchedAt, fetchResult.Metadata), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, err)
	}

	parseSummary, imports, err := s.parseNodeImports(ctx, fetchResult.Content, fetchResult.Metadata.ContentType, SourceType(record.SourceType), config)
	if err != nil {
		return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, err)
	}
	parseSummaryJSON, err := json.Marshal(parseSummary)
	if err != nil {
		return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, fmt.Errorf("encode parse summary: %w", err))
	}
	snapshotID, err := newSnapshotID()
	if err != nil {
		return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, err)
	}
	artifactPath, err := s.writeSnapshot(id, snapshotID, fetchResult.Content)
	if err != nil {
		return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, err)
	}

	snapshot := store.SubscriptionSnapshotRecord{
		ID:                snapshotID,
		SubscriptionID:    id,
		ContentHash:       contentHash,
		FetchedAt:         fetchedAt,
		NodeCount:         parseSummary.EstimatedNodes,
		Status:            "active",
		ArtifactPath:      artifactPath,
		ParseSummaryJSON:  string(parseSummaryJSON),
		FetchMetadataJSON: string(metadataJSON),
		NextRefreshAt:     nextSuccessAt,
		CreatedAt:         fetchedAt,
	}
	if s.parser != nil {
		parsedRepository := s.repository.(ParsedNodeRepository)
		err = parsedRepository.CommitParsedSubscriptionSnapshot(ctx, snapshot, imports)
	} else {
		err = s.repository.CommitSubscriptionSnapshot(ctx, snapshot)
	}
	if err != nil {
		_ = os.Remove(artifactPath)
		return RefreshResult{}, s.refreshFailed(ctx, record, fetchedAt, mapRepositoryError(err))
	}
	return RefreshResult{
		SubscriptionID: id,
		SnapshotID:     snapshotID,
		Changed:        true,
		ContentHash:    contentHash,
		Size:           int64(len(fetchResult.Content)),
		DetectedFormat: parseSummary.DetectedFormat,
		EstimatedNodes: parseSummary.EstimatedNodes,
		FetchedAt:      fetchedAt,
		FetchMetadata:  fetchResult.Metadata,
	}, nil
}

func (s *Service) decryptSourceConfig(record store.SubscriptionRecord) (SourceConfig, error) {
	plaintext, err := s.cipher.Open(record.SourceConfigEncrypted, associatedData(record.ID))
	if err != nil {
		return SourceConfig{}, fmt.Errorf("decrypt subscription source: %w", err)
	}
	var config SourceConfig
	if err := json.Unmarshal(plaintext, &config); err != nil {
		return SourceConfig{}, fmt.Errorf("decode subscription source: %w", err)
	}
	return config, nil
}

func (s *Service) fetchCondition(
	ctx context.Context,
	latestSnapshotID string,
) (FetchCondition, store.SubscriptionSnapshotRecord, error) {
	if latestSnapshotID == "" {
		return FetchCondition{}, store.SubscriptionSnapshotRecord{}, nil
	}
	snapshot, err := s.repository.GetSubscriptionSnapshot(ctx, latestSnapshotID)
	if err != nil {
		return FetchCondition{}, store.SubscriptionSnapshotRecord{}, mapRepositoryError(err)
	}
	var metadata FetchMetadata
	if snapshot.FetchMetadataJSON != "" {
		if err := json.Unmarshal([]byte(snapshot.FetchMetadataJSON), &metadata); err != nil {
			return FetchCondition{}, store.SubscriptionSnapshotRecord{}, fmt.Errorf("decode previous fetch metadata: %w", err)
		}
	}
	return FetchCondition{ETag: metadata.ETag, LastModified: metadata.LastModified}, snapshot, nil
}

// Failure is the persisted, structured reason of the most recent refresh
// failure. It never contains the subscription URL or credentials.
type Failure struct {
	Code    string    `json:"code"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// maxFastRetries bounds the exponential fast-retry phase. Beyond it the
// subscription falls back to its regular schedule instead of hammering a
// broken source.
const maxFastRetries = 8

func (s *Service) refreshFailed(
	ctx context.Context,
	record store.SubscriptionRecord,
	failedAt time.Time,
	cause error,
) error {
	failureCount := record.ConsecutiveFailures + 1
	var nextRefreshAt time.Time
	if failureCount > maxFastRetries {
		nextRefreshAt = nextScheduledRefresh(record, failedAt)
	} else {
		nextRefreshAt = failedAt.Add(refreshFailureBackoff(
			failureCount,
			time.Duration(record.RefreshIntervalSeconds)*time.Second,
		))
	}
	failure := Failure{Code: classifyRefreshError(cause), Message: sanitizeBatchError(cause), At: failedAt}
	failureJSON, marshalErr := json.Marshal(failure)
	if marshalErr != nil {
		failureJSON = []byte(`{"code":"internal","message":"failed to encode failure"}`)
	}
	if markErr := s.repository.MarkSubscriptionRefreshFailed(ctx, record.ID, failedAt, nextRefreshAt, string(failureJSON)); markErr != nil {
		return errors.Join(cause, fmt.Errorf("record refresh failure: %w", mapRepositoryError(markErr)))
	}
	return cause
}

// nextScheduledRefresh computes the next regular slot: the cron schedule
// when configured, otherwise the fixed interval.
func nextScheduledRefresh(record store.SubscriptionRecord, from time.Time) time.Time {
	if expression := strings.TrimSpace(record.RefreshCron); expression != "" {
		if schedule, err := cron.Parse(expression); err == nil {
			if next := schedule.Next(from); !next.IsZero() {
				return next
			}
		}
	}
	return from.Add(time.Duration(record.RefreshIntervalSeconds) * time.Second)
}

// classifyRefreshError maps an error chain onto a stable failure code the
// UI can rely on without string matching.
func classifyRefreshError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(message, "timed out"), strings.Contains(message, "timeout"):
		return "timeout"
	case strings.Contains(message, "status"):
		return "http_status"
	case strings.Contains(message, "too large"), strings.Contains(message, "exceeds"):
		return "content_too_large"
	case strings.Contains(message, "parse"), strings.Contains(message, "decode"), strings.Contains(message, "unsupported"):
		return "parse"
	case strings.Contains(message, "network"), strings.Contains(message, "dial"), strings.Contains(message, "dns"),
		strings.Contains(message, "connection"), strings.Contains(message, "request failed"):
		return "network"
	case strings.Contains(message, "empty"):
		return "empty_content"
	default:
		return "internal"
	}
}

// refreshFailureBackoff returns the exponential retry delay with a ±20%
// deterministic-free jitter so many failing subscriptions do not retry in
// lockstep after an outage.
func refreshFailureBackoff(failureCount int, refreshInterval time.Duration) time.Duration {
	if failureCount < 1 {
		failureCount = 1
	}
	shift := failureCount - 1
	if shift > 6 {
		shift = 6
	}
	delay := 30 * time.Second * time.Duration(1<<shift)
	maximum := refreshInterval
	if maximum <= 0 || maximum > 30*time.Minute {
		maximum = 30 * time.Minute
	}
	if delay > maximum {
		delay = maximum
	}
	// Jitter in [0.8, 1.2) of the base delay.
	jittered := time.Duration(float64(delay) * (0.8 + 0.4*rand.Float64()))
	if jittered < time.Second {
		jittered = time.Second
	}
	return jittered
}

func (s *Service) writeSnapshot(subscriptionID, snapshotID string, content []byte) (string, error) {
	directory := filepath.Join(s.snapshotDirectory, subscriptionID)
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot directory: %w", err)
	}
	if err := os.MkdirAll(absoluteDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create snapshot directory: %w", err)
	}
	if err := os.Chmod(absoluteDirectory, 0o700); err != nil {
		return "", fmt.Errorf("secure snapshot directory: %w", err)
	}
	finalPath := filepath.Join(absoluteDirectory, snapshotID+".source")
	temporary, err := os.CreateTemp(absoluteDirectory, ".snapshot-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create snapshot file: %w", err)
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure snapshot file: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		return "", fmt.Errorf("write snapshot file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync snapshot file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close snapshot file: %w", err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return "", fmt.Errorf("publish snapshot file: %w", err)
	}
	if err := syncSnapshotDirectory(absoluteDirectory); err != nil {
		_ = os.Remove(finalPath)
		return "", err
	}
	published = true
	return finalPath, nil
}

func inspectSource(content []byte, contentType string) ParseSummary {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return ParseSummary{DetectedFormat: "empty"}
	}
	if json.Valid(trimmed) {
		return ParseSummary{DetectedFormat: "json"}
	}
	text := string(trimmed)
	if strings.Contains(text, "proxies:") || strings.Contains(text, "proxy-providers:") {
		return ParseSummary{DetectedFormat: "clash-yaml", EstimatedNodes: estimateYAMLNodes(text)}
	}
	if count := countURILines(text); count > 0 {
		return ParseSummary{DetectedFormat: "uri-list", EstimatedNodes: count}
	}
	compact := strings.Map(func(character rune) rune {
		if character == '\r' || character == '\n' || character == ' ' || character == '\t' {
			return -1
		}
		return character
	}, text)
	if decoded, err := base64.StdEncoding.DecodeString(compact); err == nil && len(decoded) > 0 {
		decodedSummary := inspectSource(decoded, "")
		if decodedSummary.DetectedFormat != "unknown" && decodedSummary.DetectedFormat != "empty" {
			decodedSummary.DetectedFormat = "base64-" + decodedSummary.DetectedFormat
			return decodedSummary
		}
	}
	if normalizedContentType(contentType) == "application/yaml" || normalizedContentType(contentType) == "text/yaml" {
		return ParseSummary{DetectedFormat: "yaml"}
	}
	return ParseSummary{DetectedFormat: "unknown"}
}

func countURILines(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "://") {
			count++
		}
	}
	return count
}

func estimateYAMLNodes(text string) int {
	count := 0
	inProxies := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "proxies:" {
			inProxies = true
			continue
		}
		if inProxies && trimmed != "" && !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break
		}
		if inProxies && strings.HasPrefix(trimmed, "-") {
			count++
		}
	}
	return count
}

func refreshResultFromSnapshot(
	snapshot store.SubscriptionSnapshotRecord,
	changed bool,
	fetchedAt time.Time,
	metadata FetchMetadata,
) RefreshResult {
	var summary ParseSummary
	_ = json.Unmarshal([]byte(snapshot.ParseSummaryJSON), &summary)
	if metadata.Size == 0 {
		var previousMetadata FetchMetadata
		if json.Unmarshal([]byte(snapshot.FetchMetadataJSON), &previousMetadata) == nil {
			metadata.Size = previousMetadata.Size
			if metadata.ContentType == "" {
				metadata.ContentType = previousMetadata.ContentType
			}
		}
	}
	return RefreshResult{
		SubscriptionID: snapshot.SubscriptionID,
		SnapshotID:     snapshot.ID,
		Changed:        changed,
		ContentHash:    snapshot.ContentHash,
		Size:           metadata.Size,
		DetectedFormat: summary.DetectedFormat,
		EstimatedNodes: summary.EstimatedNodes,
		FetchedAt:      fetchedAt,
		FetchMetadata:  metadata,
	}
}

func newSnapshotID() (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	return "snap-" + strings.TrimPrefix(id, "sub-"), nil
}

func syncSnapshotDirectory(directoryPath string) error {
	directory, err := os.Open(directoryPath)
	if err != nil {
		return fmt.Errorf("open snapshot directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync snapshot directory: %w", err)
	}
	return nil
}

type keyedLocker struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu         sync.Mutex
	references int
}

func (locker *keyedLocker) lock(key string) func() {
	locker.mu.Lock()
	if locker.locks == nil {
		locker.locks = make(map[string]*keyedLock)
	}
	entry := locker.locks[key]
	if entry == nil {
		entry = &keyedLock{}
		locker.locks[key] = entry
	}
	entry.references++
	locker.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		locker.mu.Lock()
		entry.references--
		if entry.references == 0 {
			delete(locker.locks, key)
		}
		locker.mu.Unlock()
	}
}
