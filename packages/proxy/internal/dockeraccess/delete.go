package dockeraccess

import (
	"context"
	"fmt"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

// DeleteSource deletes a service listener together with its Docker entry.
//
// The order matters. A stale version is rejected before anything changes, so
// a 409 never costs the caller a working Docker entry. The companion is
// looked up first (the link cascades away with the source), then the source
// is deleted with the listener service's own version check, which closes the
// window between the pre-check and the delete. Only after the source is gone
// is the companion removed. If that last step fails the error names the
// companion, which is then an ordinary listener the operator can delete.
func (s *Service) DeleteSource(ctx context.Context, listenerID string, version int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.listeners.Get(ctx, listenerID)
	if err != nil {
		return err
	}
	if source.Version != version {
		return fmt.Errorf("%w: listener version is %d, not %d", listener.ErrConflict, source.Version, version)
	}
	entry, err := s.entryFor(ctx, listenerID)
	if err != nil {
		return err
	}
	if err := s.listeners.Delete(ctx, listenerID, version); err != nil {
		return err
	}
	if entry == nil {
		return nil
	}
	if err := s.removeEntry(ctx, *entry, entry.SharedInbound != ""); err != nil {
		return fmt.Errorf("service deleted, but removing its Docker entry %s failed: %w", entry.ID, err)
	}
	return nil
}
