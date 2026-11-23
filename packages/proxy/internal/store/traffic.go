package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	TrafficResourceListener           = "listener"
	TrafficResourceProxyGroup         = "proxy_group"
	TrafficResourceNode               = "node"
	TrafficResourceResidentialChannel = "residential_channel"
)

type TrafficWrite struct {
	ResourceType      string
	ResourceID        string
	BucketStart       time.Time
	Granularity       time.Duration
	UploadBytes       int64
	DownloadBytes     int64
	ConnectionCount   int64
	ActiveConnections int64
}

type TrafficTotalRecord struct {
	ResourceType      string
	ResourceID        string
	UploadBytes       int64
	DownloadBytes     int64
	ConnectionCount   int64
	ActiveConnections int64
	UpdatedAt         time.Time
}

type TrafficBucketRecord struct {
	BucketStart           time.Time
	UploadBytes           int64
	DownloadBytes         int64
	ConnectionCount       int64
	PeakActiveConnections int64
}

func (s *Store) WriteTraffic(ctx context.Context, writes []TrafficWrite, updatedAt time.Time) error {
	if len(writes) == 0 {
		return nil
	}
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin traffic write: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	for _, write := range writes {
		if !validTrafficResource(write.ResourceType) || write.ResourceID == "" || write.Granularity != time.Minute {
			return errors.New("invalid traffic write")
		}
		if write.UploadBytes < 0 || write.DownloadBytes < 0 || write.ConnectionCount < 0 || write.ActiveConnections < 0 {
			return errors.New("traffic counters cannot be negative")
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO traffic_totals(
    resource_type, resource_id, upload_bytes, download_bytes,
    connection_count, active_connections, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(resource_type, resource_id) DO UPDATE SET
    upload_bytes = upload_bytes + excluded.upload_bytes,
    download_bytes = download_bytes + excluded.download_bytes,
    connection_count = connection_count + excluded.connection_count,
    active_connections = excluded.active_connections,
    updated_at = excluded.updated_at
`, write.ResourceType, write.ResourceID, write.UploadBytes, write.DownloadBytes,
			write.ConnectionCount, write.ActiveConnections, updatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("upsert traffic total: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO traffic_buckets(
    resource_type, resource_id, bucket_start, granularity_seconds,
    upload_bytes, download_bytes, connection_count, peak_active_connections
) VALUES (?, ?, ?, 60, ?, ?, ?, ?)
ON CONFLICT(resource_type, resource_id, bucket_start, granularity_seconds) DO UPDATE SET
    upload_bytes = upload_bytes + excluded.upload_bytes,
    download_bytes = download_bytes + excluded.download_bytes,
    connection_count = connection_count + excluded.connection_count,
    peak_active_connections = MAX(peak_active_connections, excluded.peak_active_connections)
`, write.ResourceType, write.ResourceID, write.BucketStart.UTC().Truncate(time.Minute).Format(time.RFC3339Nano),
			write.UploadBytes, write.DownloadBytes, write.ConnectionCount, write.ActiveConnections); err != nil {
			return fmt.Errorf("upsert traffic bucket: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit traffic write: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) GetTrafficTotal(ctx context.Context, resourceType, resourceID string) (TrafficTotalRecord, error) {
	if !validTrafficResource(resourceType) || resourceID == "" {
		return TrafficTotalRecord{}, errors.New("invalid traffic resource")
	}
	var record TrafficTotalRecord
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT resource_type, resource_id, upload_bytes, download_bytes,
       connection_count, active_connections, updated_at
FROM traffic_totals WHERE resource_type = ? AND resource_id = ?
`, resourceType, resourceID).Scan(&record.ResourceType, &record.ResourceID, &record.UploadBytes,
		&record.DownloadBytes, &record.ConnectionCount, &record.ActiveConnections, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TrafficTotalRecord{ResourceType: resourceType, ResourceID: resourceID}, nil
	}
	if err != nil {
		return TrafficTotalRecord{}, fmt.Errorf("get traffic total: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return TrafficTotalRecord{}, fmt.Errorf("parse traffic total timestamp: %w", err)
	}
	return record, nil
}

func (s *Store) ListTrafficTotals(ctx context.Context, resourceType string, limit, offset int) ([]TrafficTotalRecord, error) {
	if !validTrafficResource(resourceType) || limit < 1 || limit > 1000 || offset < 0 {
		return nil, errors.New("invalid traffic totals query")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT resource_type, resource_id, upload_bytes, download_bytes,
       connection_count, active_connections, updated_at
FROM traffic_totals WHERE resource_type = ?
ORDER BY updated_at DESC, resource_id ASC LIMIT ? OFFSET ?
`, resourceType, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list traffic totals: %w", err)
	}
	defer rows.Close()
	records := make([]TrafficTotalRecord, 0)
	for rows.Next() {
		var record TrafficTotalRecord
		var updatedAt string
		if err := rows.Scan(&record.ResourceType, &record.ResourceID, &record.UploadBytes,
			&record.DownloadBytes, &record.ConnectionCount, &record.ActiveConnections, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan traffic total: %w", err)
		}
		record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse traffic total timestamp: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic totals: %w", err)
	}
	return records, nil
}

func (s *Store) ListTrafficSummaries(
	ctx context.Context,
	resourceType string,
	from, to time.Time,
	limit, offset int,
) ([]TrafficTotalRecord, error) {
	if !validTrafficResource(resourceType) || from.IsZero() || to.IsZero() || !from.Before(to) || limit < 1 || limit > 1000 || offset < 0 {
		return nil, errors.New("invalid traffic summaries query")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT b.resource_type, b.resource_id,
       SUM(b.upload_bytes), SUM(b.download_bytes),
       SUM(b.connection_count), MAX(b.peak_active_connections),
       COALESCE(MAX(t.updated_at), '')
FROM traffic_buckets b
LEFT JOIN traffic_totals t
  ON t.resource_type = b.resource_type AND t.resource_id = b.resource_id
WHERE b.resource_type = ? AND b.bucket_start >= ? AND b.bucket_start < ?
GROUP BY b.resource_type, b.resource_id
ORDER BY (SUM(b.upload_bytes) + SUM(b.download_bytes)) DESC, b.resource_id ASC
LIMIT ? OFFSET ?
`, resourceType, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list traffic summaries: %w", err)
	}
	defer rows.Close()
	records := make([]TrafficTotalRecord, 0)
	for rows.Next() {
		var record TrafficTotalRecord
		var updatedAt string
		if err := rows.Scan(&record.ResourceType, &record.ResourceID, &record.UploadBytes,
			&record.DownloadBytes, &record.ConnectionCount, &record.ActiveConnections, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan traffic summary: %w", err)
		}
		if updatedAt != "" {
			record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
			if err != nil {
				return nil, fmt.Errorf("parse traffic summary timestamp: %w", err)
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic summaries: %w", err)
	}
	return records, nil
}

func (s *Store) ListTrafficBuckets(ctx context.Context, resourceType, resourceID string, from, to time.Time, granularity time.Duration, limit int) ([]TrafficBucketRecord, error) {
	if !validTrafficResource(resourceType) || resourceID == "" || !validTrafficGranularity(granularity) {
		return nil, errors.New("invalid traffic query")
	}
	if limit < 1 || limit > 500 {
		return nil, errors.New("traffic query limit must be between 1 and 500")
	}
	granularitySeconds := int64(granularity / time.Second)
	rows, err := s.db.QueryContext(ctx, `
SELECT strftime('%Y-%m-%dT%H:%M:%SZ',
                (CAST(strftime('%s', bucket_start) AS INTEGER) / ?) * ?, 'unixepoch') AS aggregate_start,
       SUM(upload_bytes), SUM(download_bytes), SUM(connection_count), MAX(peak_active_connections)
FROM traffic_buckets
WHERE resource_type = ? AND resource_id = ? AND granularity_seconds <= ?
  AND bucket_start >= ? AND bucket_start < ?
GROUP BY (CAST(strftime('%s', bucket_start) AS INTEGER) / ?)
ORDER BY aggregate_start ASC LIMIT ?
`, granularitySeconds, granularitySeconds, resourceType, resourceID, granularitySeconds,
		from.UTC().Format(time.RFC3339Nano),
		to.UTC().Format(time.RFC3339Nano), granularitySeconds, limit)
	if err != nil {
		return nil, fmt.Errorf("list traffic buckets: %w", err)
	}
	defer rows.Close()
	records := make([]TrafficBucketRecord, 0)
	for rows.Next() {
		var record TrafficBucketRecord
		var bucketStart string
		if err := rows.Scan(&bucketStart, &record.UploadBytes, &record.DownloadBytes,
			&record.ConnectionCount, &record.PeakActiveConnections); err != nil {
			return nil, fmt.Errorf("scan traffic bucket: %w", err)
		}
		record.BucketStart, err = time.Parse(time.RFC3339Nano, bucketStart)
		if err != nil {
			return nil, fmt.Errorf("parse traffic bucket timestamp: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic buckets: %w", err)
	}
	return records, nil
}

func (s *Store) CompactTraffic(ctx context.Context, now time.Time) error {
	transaction, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin traffic compaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = transaction.Rollback()
		}
	}()
	minuteCutoff := now.UTC().Add(-24 * time.Hour).Truncate(5 * time.Minute)
	fiveMinuteCutoff := now.UTC().Add(-7 * 24 * time.Hour).Truncate(time.Hour)
	retentionCutoff := now.UTC().Add(-30 * 24 * time.Hour).Truncate(time.Hour)
	if err := rollupTraffic(ctx, transaction, 60, 300, minuteCutoff); err != nil {
		return err
	}
	if err := rollupTraffic(ctx, transaction, 300, 3600, fiveMinuteCutoff); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM traffic_buckets WHERE bucket_start < ?", retentionCutoff.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("delete expired traffic buckets: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit traffic compaction: %w", err)
	}
	committed = true
	return nil
}

func rollupTraffic(ctx context.Context, transaction *sql.Tx, sourceSeconds, targetSeconds int, cutoff time.Time) error {
	_, err := transaction.ExecContext(ctx, `
INSERT INTO traffic_buckets(
    resource_type, resource_id, bucket_start, granularity_seconds,
    upload_bytes, download_bytes, connection_count, peak_active_connections
)
SELECT resource_type, resource_id,
       strftime('%Y-%m-%dT%H:%M:%SZ', (CAST(strftime('%s', bucket_start) AS INTEGER) / ?) * ?, 'unixepoch'),
       ?, SUM(upload_bytes), SUM(download_bytes), SUM(connection_count), MAX(peak_active_connections)
FROM traffic_buckets
WHERE granularity_seconds = ? AND bucket_start < ?
GROUP BY resource_type, resource_id, (CAST(strftime('%s', bucket_start) AS INTEGER) / ?)
ON CONFLICT(resource_type, resource_id, bucket_start, granularity_seconds) DO UPDATE SET
    upload_bytes = excluded.upload_bytes,
    download_bytes = excluded.download_bytes,
    connection_count = excluded.connection_count,
    peak_active_connections = excluded.peak_active_connections
`, targetSeconds, targetSeconds, targetSeconds, sourceSeconds, cutoff.Format(time.RFC3339Nano), targetSeconds)
	if err != nil {
		return fmt.Errorf("roll up %d-second traffic: %w", sourceSeconds, err)
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM traffic_buckets WHERE granularity_seconds = ? AND bucket_start < ?", sourceSeconds, cutoff.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("delete rolled-up traffic: %w", err)
	}
	return nil
}

func validTrafficResource(value string) bool {
	return value == TrafficResourceListener || value == TrafficResourceProxyGroup ||
		value == TrafficResourceNode || value == TrafficResourceResidentialChannel
}

func validTrafficGranularity(value time.Duration) bool {
	return value == time.Minute || value == 5*time.Minute || value == time.Hour
}
