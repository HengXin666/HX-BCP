package subscription

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestClassifyRefreshError(t *testing.T) {
	cases := map[string]error{
		"timeout":           errors.New("subscription network request timed out"),
		"http_status":       errors.New("subscription responded with status 502"),
		"network":           errors.New("subscription network request failed"),
		"empty_content":     errors.New("subscription response is empty"),
		"parse":             errors.New("decode subscription source: unexpected token"),
		"content_too_large": errors.New("subscription content exceeds the configured limit"),
		"internal":          errors.New("some completely unexpected condition"),
	}
	for expected, err := range cases {
		if actual := classifyRefreshError(err); actual != expected {
			t.Fatalf("error %q: expected code %q, got %q", err, expected, actual)
		}
	}
	if classifyRefreshError(context.DeadlineExceeded) != "timeout" {
		t.Fatal("context deadline must classify as timeout")
	}
}

func TestRefreshFailureBackoffJitterBounds(t *testing.T) {
	interval := time.Hour
	for failureCount := 1; failureCount <= 10; failureCount++ {
		base := 30 * time.Second * time.Duration(1<<min(failureCount-1, 6))
		if base > 30*time.Minute {
			base = 30 * time.Minute
		}
		for range 50 {
			delay := refreshFailureBackoff(failureCount, interval)
			low := time.Duration(float64(base) * 0.8)
			high := time.Duration(float64(base) * 1.2)
			if delay < low || delay > high {
				t.Fatalf("failure %d: delay %s outside jitter window [%s, %s]", failureCount, delay, low, high)
			}
		}
	}
}

func TestNextScheduledRefreshPrefersCron(t *testing.T) {
	from := time.Date(2026, 7, 26, 10, 7, 0, 0, time.UTC)
	record := store.SubscriptionRecord{RefreshIntervalSeconds: 3600, RefreshCron: "30 3 * * *"}
	next := nextScheduledRefresh(record, from)
	expected := time.Date(2026, 7, 27, 3, 30, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected cron slot %s, got %s", expected, next)
	}
	// Without cron the fixed interval applies.
	record.RefreshCron = ""
	if next := nextScheduledRefresh(record, from); !next.Equal(from.Add(time.Hour)) {
		t.Fatalf("expected interval slot, got %s", next)
	}
	// A stored-but-invalid cron falls back to the interval instead of never
	// scheduling again.
	record.RefreshCron = "not a cron"
	if next := nextScheduledRefresh(record, from); !next.Equal(from.Add(time.Hour)) {
		t.Fatalf("invalid cron must fall back to interval, got %s", next)
	}
}

func TestValidateRequestCron(t *testing.T) {
	config := SourceConfig{Inline: "vless://test@example.com:443#node"}
	if err := validateRequest("name", SourceInline, config, 3600, "*/30 * * * *"); err != nil {
		t.Fatalf("valid cron rejected: %v", err)
	}
	if err := validateRequest("name", SourceInline, config, 3600, "99 * * * *"); err == nil {
		t.Fatal("invalid cron must be rejected")
	}
}
