package store

import (
	"context"
	"fmt"
	"time"
)

// DockerAccessEntryRecord links a proxy service listener to the companion
// listener created so containers can dial it. See
// .agents/notes/implemented/feature/2026-10-15-docker-access-entry.md.
type DockerAccessEntryRecord struct {
	ListenerID      string
	EntryListenerID string
	// BindTarget records where the companion was placed: "shared" when it is
	// a member of the shared Mixed entry, otherwise the bridge gateway IP it
	// binds to. It is informational; the listener row stays authoritative.
	BindTarget string
	CreatedAt  time.Time
}

// CreateDockerAccessEntry stores a link. A second link for the same source or
// the same companion is a conflict.
func (s *Store) CreateDockerAccessEntry(ctx context.Context, record DockerAccessEntryRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO docker_access_entries(listener_id, entry_listener_id, bind_target, created_at)
VALUES (?, ?, ?, ?)`,
		record.ListenerID, record.EntryListenerID, record.BindTarget,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrConflict
		}
		return fmt.Errorf("create docker access entry: %w", err)
	}
	return nil
}

// ListDockerAccessEntries returns every link in a stable order.
func (s *Store) ListDockerAccessEntries(ctx context.Context) ([]DockerAccessEntryRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT listener_id, entry_listener_id, bind_target, created_at
FROM docker_access_entries ORDER BY listener_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list docker access entries: %w", err)
	}
	defer rows.Close()
	records := make([]DockerAccessEntryRecord, 0)
	for rows.Next() {
		var record DockerAccessEntryRecord
		var created string
		if err := rows.Scan(&record.ListenerID, &record.EntryListenerID, &record.BindTarget, &created); err != nil {
			return nil, fmt.Errorf("scan docker access entry: %w", err)
		}
		record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate docker access entries: %w", err)
	}
	return records, nil
}

// DeleteDockerAccessEntry removes the link for one source listener. Removing a
// link that does not exist is not an error: the caller is converging.
func (s *Store) DeleteDockerAccessEntry(ctx context.Context, listenerID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM docker_access_entries WHERE listener_id = ?`, listenerID); err != nil {
		return fmt.Errorf("delete docker access entry: %w", err)
	}
	return nil
}

// DeleteDockerAccessEntryFor removes the link whose companion is entryID.
func (s *Store) DeleteDockerAccessEntryFor(ctx context.Context, entryID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM docker_access_entries WHERE entry_listener_id = ?`, entryID); err != nil {
		return fmt.Errorf("delete docker access entry: %w", err)
	}
	return nil
}
