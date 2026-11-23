package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

const MaxQueryRange = 30 * 24 * time.Hour

var ErrInvalidQuery = errors.New("invalid traffic query")

type Resource struct {
	Type string
	ID   string
}

type Connection struct {
	Detail    ConnectionDetail
	ID        string
	Upload    int64
	Download  int64
	Resources []Resource
}

type RuntimeSnapshot struct {
	Connections []Connection
}

const MaxLiveSamples = 120

type LiveResource struct {
	ResourceType        string
	ResourceID          string
	UploadBytesPerSec   int64
	DownloadBytesPerSec int64
	ActiveConnections   int64
}

type LiveSample struct {
	Timestamp           time.Time
	UploadBytesPerSec   int64
	DownloadBytesPerSec int64
	ActiveConnections   int64
	Resources           []LiveResource
}

type LiveSnapshot struct {
	Latest  LiveSample
	History []LiveSample
}

type LiveSubscription struct {
	C     <-chan LiveSample
	close func()
	once  sync.Once
}

func (subscription *LiveSubscription) Close() {
	if subscription == nil {
		return
	}
	subscription.once.Do(func() {
		if subscription.close != nil {
			subscription.close()
		}
	})
}

type Source interface {
	TrafficSnapshot(context.Context) (RuntimeSnapshot, error)
}

type Repository interface {
	WriteTraffic(context.Context, []store.TrafficWrite, time.Time) error
	GetTrafficTotal(context.Context, string, string) (store.TrafficTotalRecord, error)
	ListTrafficTotals(context.Context, string, int, int) ([]store.TrafficTotalRecord, error)
	ListTrafficSummaries(context.Context, string, time.Time, time.Time, int, int) ([]store.TrafficTotalRecord, error)
	ListTrafficBuckets(context.Context, string, string, time.Time, time.Time, time.Duration, int) ([]store.TrafficBucketRecord, error)
	CompactTraffic(context.Context, time.Time) error
}

type Config struct {
	PollInterval    time.Duration
	FlushInterval   time.Duration
	CompactInterval time.Duration
}

type counters struct {
	upload      int64
	download    int64
	connections int64
}

type observedConnection struct {
	upload   int64
	download int64
}

type Service struct {
	repository Repository
	source     Source
	logger     *slog.Logger
	config     Config
	now        func() time.Time

	mu              sync.Mutex
	observed        map[string]observedConnection
	pending         map[Resource]counters
	active          map[Resource]int64
	lastActive      map[Resource]int64
	lastCollectedAt time.Time
	liveHistory     []LiveSample
	connections     ActiveConnections
	subscribers     map[chan LiveSample]struct{}
}

func NewService(repository Repository, source Source, logger *slog.Logger, config Config) (*Service, error) {
	if repository == nil {
		return nil, errors.New("traffic repository is required")
	}
	if source == nil {
		return nil, errors.New("traffic source is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = time.Minute
	}
	if config.CompactInterval <= 0 {
		config.CompactInterval = time.Hour
	}
	return &Service{
		repository:  repository,
		source:      source,
		logger:      logger,
		config:      config,
		now:         time.Now,
		observed:    make(map[string]observedConnection),
		pending:     make(map[Resource]counters),
		active:      make(map[Resource]int64),
		lastActive:  make(map[Resource]int64),
		subscribers: make(map[chan LiveSample]struct{}),
	}, nil
}

func (s *Service) LiveSnapshot() LiveSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := make([]LiveSample, len(s.liveHistory))
	for index, sample := range s.liveHistory {
		history[index] = cloneLiveSample(sample)
	}
	var latest LiveSample
	if len(history) > 0 {
		latest = cloneLiveSample(history[len(history)-1])
	}
	return LiveSnapshot{Latest: latest, History: history}
}

func (s *Service) SubscribeLive() *LiveSubscription {
	updates := make(chan LiveSample, 8)
	s.mu.Lock()
	s.subscribers[updates] = struct{}{}
	s.mu.Unlock()
	return &LiveSubscription{
		C: updates,
		close: func() {
			s.mu.Lock()
			if _, exists := s.subscribers[updates]; exists {
				delete(s.subscribers, updates)
				close(updates)
			}
			s.mu.Unlock()
		},
	}
}

func cloneLiveSample(sample LiveSample) LiveSample {
	sample.Resources = append([]LiveResource(nil), sample.Resources...)
	return sample
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.repository.CompactTraffic(ctx, s.now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Error("compact traffic history at startup", "error", err)
	}
	pollTicker := time.NewTicker(s.config.PollInterval)
	flushTicker := time.NewTicker(s.config.FlushInterval)
	compactTicker := time.NewTicker(s.config.CompactInterval)
	defer pollTicker.Stop()
	defer flushTicker.Stop()
	defer compactTicker.Stop()

	var lastCollectWarning time.Time
	for {
		select {
		case <-ctx.Done():
			shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := s.Flush(shutdownContext)
			cancel()
			return err
		case <-pollTicker.C:
			if err := s.Collect(ctx); err != nil {
				now := s.now()
				if lastCollectWarning.IsZero() || now.Sub(lastCollectWarning) >= time.Minute {
					s.logger.Warn("collect Mihomo traffic snapshot", "error", err)
					lastCollectWarning = now
				}
			}
		case <-flushTicker.C:
			if err := s.Flush(ctx); err != nil {
				s.logger.Error("flush aggregated traffic", "error", err)
			}
		case <-compactTicker.C:
			if err := s.repository.CompactTraffic(ctx, s.now().UTC()); err != nil {
				s.logger.Error("compact traffic history", "error", err)
			}
		}
	}
}

func (s *Service) Collect(ctx context.Context) error {
	snapshot, err := s.source.TrafficSnapshot(ctx)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	details := connectionDetails(snapshot, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections = details

	nextObserved := make(map[string]observedConnection, len(snapshot.Connections))
	active := make(map[Resource]int64)
	deltas := make(map[Resource]counters)
	var totalUpload, totalDownload, activeConnections int64
	for _, connection := range snapshot.Connections {
		if connection.ID == "" || connection.Upload < 0 || connection.Download < 0 {
			continue
		}
		activeConnections++
		previous, existed := s.observed[connection.ID]
		uploadDelta := nonnegativeDelta(connection.Upload, previous.upload)
		downloadDelta := nonnegativeDelta(connection.Download, previous.download)
		totalUpload += uploadDelta
		totalDownload += downloadDelta
		connectionDelta := int64(0)
		if !existed {
			connectionDelta = 1
		}
		seenResources := make(map[Resource]struct{}, len(connection.Resources))
		for _, resource := range connection.Resources {
			if !validResource(resource) {
				continue
			}
			if _, duplicate := seenResources[resource]; duplicate {
				continue
			}
			seenResources[resource] = struct{}{}
			value := s.pending[resource]
			value.upload += uploadDelta
			value.download += downloadDelta
			value.connections += connectionDelta
			s.pending[resource] = value
			delta := deltas[resource]
			delta.upload += uploadDelta
			delta.download += downloadDelta
			deltas[resource] = delta
			active[resource]++
		}
		nextObserved[connection.ID] = observedConnection{upload: connection.Upload, download: connection.Download}
	}
	s.observed = nextObserved
	s.active = active
	elapsed := s.config.PollInterval
	if !s.lastCollectedAt.IsZero() && now.After(s.lastCollectedAt) {
		elapsed = now.Sub(s.lastCollectedAt)
	}
	if elapsed <= 0 {
		elapsed = time.Second
	}
	resourceSet := make(map[Resource]struct{}, len(deltas)+len(active))
	for resource := range deltas {
		resourceSet[resource] = struct{}{}
	}
	for resource := range active {
		resourceSet[resource] = struct{}{}
	}
	resources := make([]LiveResource, 0, len(resourceSet))
	for resource := range resourceSet {
		delta := deltas[resource]
		resources = append(resources, LiveResource{
			ResourceType:        resource.Type,
			ResourceID:          resource.ID,
			UploadBytesPerSec:   bytesPerSecond(delta.upload, elapsed),
			DownloadBytesPerSec: bytesPerSecond(delta.download, elapsed),
			ActiveConnections:   active[resource],
		})
	}
	sort.Slice(resources, func(left, right int) bool {
		if resources[left].ResourceType == resources[right].ResourceType {
			return resources[left].ResourceID < resources[right].ResourceID
		}
		return resources[left].ResourceType < resources[right].ResourceType
	})
	sample := LiveSample{
		Timestamp:           now,
		UploadBytesPerSec:   bytesPerSecond(totalUpload, elapsed),
		DownloadBytesPerSec: bytesPerSecond(totalDownload, elapsed),
		ActiveConnections:   activeConnections,
		Resources:           resources,
	}
	s.lastCollectedAt = now
	s.liveHistory = append(s.liveHistory, sample)
	if len(s.liveHistory) > MaxLiveSamples {
		s.liveHistory = append([]LiveSample(nil), s.liveHistory[len(s.liveHistory)-MaxLiveSamples:]...)
	}
	for updates := range s.subscribers {
		value := cloneLiveSample(sample)
		select {
		case updates <- value:
		default:
			select {
			case <-updates:
			default:
			}
			updates <- value
		}
	}
	return nil
}

func bytesPerSecond(bytes int64, elapsed time.Duration) int64 {
	if bytes <= 0 || elapsed <= 0 {
		return 0
	}
	return int64(float64(bytes) / elapsed.Seconds())
}

func (s *Service) Flush(ctx context.Context) error {
	s.mu.Lock()
	resources := make(map[Resource]struct{}, len(s.pending)+len(s.active)+len(s.lastActive))
	for resource := range s.pending {
		resources[resource] = struct{}{}
	}
	for resource := range s.active {
		resources[resource] = struct{}{}
	}
	for resource := range s.lastActive {
		resources[resource] = struct{}{}
	}
	now := s.now().UTC()
	writes := make([]store.TrafficWrite, 0, len(resources))
	flushed := make(map[Resource]counters, len(s.pending))
	for resource, value := range s.pending {
		flushed[resource] = value
	}
	flushedActive := make(map[Resource]int64, len(s.active))
	for resource, count := range s.active {
		flushedActive[resource] = count
	}
	for resource := range resources {
		value := s.pending[resource]
		writes = append(writes, store.TrafficWrite{
			ResourceType:      resource.Type,
			ResourceID:        resource.ID,
			BucketStart:       now.Truncate(time.Minute),
			Granularity:       time.Minute,
			UploadBytes:       value.upload,
			DownloadBytes:     value.download,
			ConnectionCount:   value.connections,
			ActiveConnections: s.active[resource],
		})
	}
	s.mu.Unlock()

	sort.Slice(writes, func(left, right int) bool {
		if writes[left].ResourceType == writes[right].ResourceType {
			return writes[left].ResourceID < writes[right].ResourceID
		}
		return writes[left].ResourceType < writes[right].ResourceType
	})
	if err := s.repository.WriteTraffic(ctx, writes, now); err != nil {
		return err
	}

	s.mu.Lock()
	for resource, value := range flushed {
		current := s.pending[resource]
		current.upload -= value.upload
		current.download -= value.download
		current.connections -= value.connections
		if current == (counters{}) {
			delete(s.pending, resource)
		} else {
			s.pending[resource] = current
		}
	}
	s.lastActive = make(map[Resource]int64, len(flushedActive))
	for resource, count := range flushedActive {
		s.lastActive[resource] = count
	}
	s.mu.Unlock()
	return nil
}

type Summary struct {
	ResourceType      string     `json:"resource_type"`
	ResourceID        string     `json:"resource_id"`
	UploadBytes       int64      `json:"upload_bytes"`
	DownloadBytes     int64      `json:"download_bytes"`
	ConnectionCount   int64      `json:"connection_count"`
	ActiveConnections int64      `json:"active_connections"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
}

type Point struct {
	Time                  time.Time `json:"time"`
	UploadBytes           int64     `json:"upload_bytes"`
	DownloadBytes         int64     `json:"download_bytes"`
	ConnectionCount       int64     `json:"connection_count"`
	PeakActiveConnections int64     `json:"peak_active_connections"`
}

type Series struct {
	Summary    Summary   `json:"summary"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Resolution int64     `json:"resolution_seconds"`
	Points     []Point   `json:"points"`
}

func (s *Service) Summary(ctx context.Context, resourceType, resourceID string) (Summary, error) {
	record, err := s.repository.GetTrafficTotal(ctx, resourceType, resourceID)
	if err != nil {
		return Summary{}, err
	}
	return summaryFromRecord(record), nil
}

func (s *Service) ListSummaries(ctx context.Context, resourceType string, limit, offset int) ([]Summary, error) {
	records, err := s.repository.ListTrafficTotals(ctx, resourceType, limit, offset)
	if err != nil {
		return nil, err
	}
	items := make([]Summary, 0, len(records))
	for _, record := range records {
		items = append(items, summaryFromRecord(record))
	}
	return items, nil
}

func (s *Service) ListSummariesBetween(ctx context.Context, resourceType string, from, to time.Time, limit, offset int) ([]Summary, error) {
	if !validResource(Resource{Type: resourceType, ID: "range"}) || from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > MaxQueryRange {
		return nil, fmt.Errorf("%w: invalid traffic summary range", ErrInvalidQuery)
	}
	if limit < 1 || limit > 1000 || offset < 0 {
		return nil, fmt.Errorf("%w: invalid traffic summary pagination", ErrInvalidQuery)
	}
	records, err := s.repository.ListTrafficSummaries(ctx, resourceType, from.UTC(), to.UTC(), limit, offset)
	if err != nil {
		return nil, err
	}
	items := make([]Summary, 0, len(records))
	for _, record := range records {
		items = append(items, summaryFromRecord(record))
	}
	return items, nil
}

func (s *Service) Query(ctx context.Context, resourceType, resourceID string, from, to time.Time, maxPoints int) (Series, error) {
	if !validResource(Resource{Type: resourceType, ID: resourceID}) {
		return Series{}, fmt.Errorf("%w: invalid traffic resource", ErrInvalidQuery)
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > MaxQueryRange {
		return Series{}, fmt.Errorf("%w: traffic range must be positive and no longer than %s", ErrInvalidQuery, MaxQueryRange)
	}
	if maxPoints < 1 || maxPoints > 500 {
		return Series{}, fmt.Errorf("%w: max_points must be between 1 and 500", ErrInvalidQuery)
	}
	resolution := chooseResolution(to.Sub(from), maxPoints)
	records, err := s.repository.ListTrafficBuckets(ctx, resourceType, resourceID, from, to, resolution, maxPoints)
	if err != nil {
		return Series{}, err
	}
	total, err := s.Summary(ctx, resourceType, resourceID)
	if err != nil {
		return Series{}, err
	}
	points := make([]Point, 0, len(records))
	for _, record := range records {
		points = append(points, Point{
			Time:                  record.BucketStart,
			UploadBytes:           record.UploadBytes,
			DownloadBytes:         record.DownloadBytes,
			ConnectionCount:       record.ConnectionCount,
			PeakActiveConnections: record.PeakActiveConnections,
		})
	}
	return Series{Summary: total, From: from.UTC(), To: to.UTC(), Resolution: int64(resolution / time.Second), Points: points}, nil
}

func chooseResolution(span time.Duration, maxPoints int) time.Duration {
	if span <= time.Duration(maxPoints)*time.Minute && span <= 24*time.Hour {
		return time.Minute
	}
	if span <= time.Duration(maxPoints)*5*time.Minute && span <= 7*24*time.Hour {
		return 5 * time.Minute
	}
	return time.Hour
}

func summaryFromRecord(record store.TrafficTotalRecord) Summary {
	result := Summary{
		ResourceType:      record.ResourceType,
		ResourceID:        record.ResourceID,
		UploadBytes:       record.UploadBytes,
		DownloadBytes:     record.DownloadBytes,
		ConnectionCount:   record.ConnectionCount,
		ActiveConnections: record.ActiveConnections,
	}
	if !record.UpdatedAt.IsZero() {
		updatedAt := record.UpdatedAt
		result.UpdatedAt = &updatedAt
	}
	return result
}

func nonnegativeDelta(current, previous int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}

func ValidResourceType(resourceType string) bool {
	return resourceType == store.TrafficResourceListener ||
		resourceType == store.TrafficResourceProxyGroup ||
		resourceType == store.TrafficResourceNode ||
		resourceType == store.TrafficResourceResidentialChannel
}

func validResource(resource Resource) bool {
	return resource.ID != "" && ValidResourceType(resource.Type)
}
