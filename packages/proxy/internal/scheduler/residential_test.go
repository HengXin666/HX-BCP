package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type stubResidentialMaintainer struct {
	processed int
	limit     int
	err       error
}

func (stub *stubResidentialMaintainer) MaintainExpiredClientSessions(_ context.Context, limit int) (int, error) {
	stub.limit = limit
	return stub.processed, stub.err
}

func TestResidentialSchedulerRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewResidentialScheduler(nil, logger, ResidentialConfig{}); err == nil {
		t.Fatal("a nil service must be rejected")
	}
	for name, config := range map[string]ResidentialConfig{
		"poll too short":     {PollInterval: time.Second},
		"refresh limit high": {MaximumRefreshesPerPass: 1000},
	} {
		if _, err := NewResidentialScheduler(&stubResidentialMaintainer{}, logger, config); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestResidentialSchedulerRunsBoundedExpiryMaintenance(t *testing.T) {
	t.Parallel()
	stub := &stubResidentialMaintainer{processed: 3}
	scheduler, err := NewResidentialScheduler(stub, slog.New(slog.NewTextHandler(io.Discard, nil)), ResidentialConfig{
		MaximumRefreshesPerPass: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce() error = %v", err)
	}
	if stub.limit != 3 {
		t.Fatalf("maintenance limit = %d, want 3", stub.limit)
	}
}

func TestResidentialSchedulerReturnsMaintenanceError(t *testing.T) {
	t.Parallel()
	want := errors.New("apply failed")
	stub := &stubResidentialMaintainer{err: want}
	scheduler, err := NewResidentialScheduler(stub, slog.New(slog.NewTextHandler(io.Discard, nil)), ResidentialConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.runOnce(context.Background()); !errors.Is(err, want) {
		t.Fatalf("runOnce() error = %v, want %v", err, want)
	}
}

func TestResidentialSchedulerStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	scheduler, err := NewResidentialScheduler(&stubResidentialMaintainer{}, slog.New(slog.NewTextHandler(io.Discard, nil)), ResidentialConfig{PollInterval: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after cancellation")
	}
}
