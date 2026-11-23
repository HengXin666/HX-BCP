package node

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/nodeparse"
	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
	"github.com/HengXin666/hx-pxy/internal/subscription"
)

type fakeProber struct {
	latency int
	err     error
	applies int
	urls    []string
}

func (prober *fakeProber) Apply(context.Context) error {
	prober.applies++
	return nil
}

func (prober *fakeProber) TestProxy(_ context.Context, _ string, url string, _ time.Duration) (int, error) {
	prober.urls = append(prober.urls, url)
	return prober.latency, prober.err
}

func TestCheckUpdatesNodeLifecycle(t *testing.T) {
	ctx := context.Background()
	database, nodeID := createCandidateNode(t, ctx)
	defer database.Close()
	prober := &fakeProber{latency: 120}
	service, err := NewService(database, WithProber(prober))
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Check(ctx, nodeID)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !result.Success || result.LatencyMS == nil || *result.LatencyMS != 120 {
		t.Fatalf("unexpected successful result: %+v", result)
	}
	if result.Node.LifecycleState != "healthy" || result.Node.ConsecutiveProbeFailures != 0 {
		t.Fatalf("unexpected healthy node: %+v", result.Node)
	}

	prober.latency = 1800
	result, err = service.Check(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Node.LifecycleState != "degraded" {
		t.Fatalf("high-latency state = %q, want degraded", result.Node.LifecycleState)
	}

	// The fixture's subscription carries no explicit source_class, so it takes
	// the documented default ('subscription'), which quarantines at 5
	// consecutive failures — NOT the old global constant of 3. Pinning every
	// intermediate state is the point: this is the threshold that used to be
	// hardcoded for every source, and the regression it caused was retiring a
	// single-member source's only exit.
	prober.err = errors.New("connection refused")
	for attempt := 1; attempt <= 5; attempt++ {
		result, err = service.Check(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Success {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
		wantState := "degraded"
		if attempt == 5 {
			wantState = "quarantined"
		}
		if result.Node.LifecycleState != wantState {
			t.Fatalf("attempt %d state = %q, want %q", attempt, result.Node.LifecycleState, wantState)
		}
		if result.Node.ConsecutiveProbeFailures != attempt {
			t.Fatalf("attempt %d failures = %d, want %d", attempt, result.Node.ConsecutiveProbeFailures, attempt)
		}
	}
	// A subscription-class node keeps being probed after quarantine: a paid
	// source's node may recover, so it must not be written off.
	if result.Node.ProbePaused {
		t.Fatalf("subscription node must keep being probed after quarantine: %+v", result.Node)
	}
	if result.Node.NextProbeAt == nil {
		t.Fatalf("failed node has no backoff schedule: %+v", result.Node)
	}

	prober.err = nil
	prober.latency = 80
	result, err = service.Check(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Node.LifecycleState != "healthy" || result.Node.ConsecutiveProbeFailures != 0 {
		t.Fatalf("successful recovery did not reset node: %+v", result.Node)
	}
	// Recovery must also drop the backoff and the pause, otherwise a node that
	// came back would still be held out by the failure schedule.
	if result.Node.NextProbeAt != nil || result.Node.ProbePaused {
		t.Fatalf("recovery left failure schedule behind: %+v", result.Node)
	}
	if prober.applies != 8 {
		t.Fatalf("Apply() calls = %d, want 8", prober.applies)
	}
}

func TestCheckResultKeepsNodeSources(t *testing.T) {
	ctx := context.Background()
	database, nodeID := createCandidateNode(t, ctx)
	defer database.Close()
	service, err := NewService(database, WithProber(&fakeProber{latency: 42}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Check(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Node.Sources) != 1 || result.Node.Sources[0].SubscriptionID == "" {
		t.Fatalf("sources = %+v, want active subscription source", result.Node.Sources)
	}
}

func TestCheckRecordsConfiguredSiteHealthWithoutChangingPrimaryState(t *testing.T) {
	ctx := context.Background()
	database, nodeID := createCandidateNode(t, ctx)
	defer database.Close()
	prober := &fakeProber{latency: 55}
	service, err := NewService(database, WithProber(prober))
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultQualitySettings()
	settings.HealthTargets = []HealthTarget{{ID: "github", Name: "GitHub", URL: "https://github.com/", Enabled: true}}
	if _, err := service.UpdateQualitySettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	result, err := service.Check(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Node.LifecycleState != "healthy" || len(result.Node.HealthChecks) != 1 {
		t.Fatalf("result = %+v", result)
	}
	check := result.Node.HealthChecks[0]
	if check.TargetID != "github" || !check.Success || check.LatencyMS == nil || *check.LatencyMS != 55 {
		t.Fatalf("health check = %+v", check)
	}
	if len(prober.urls) != 2 || prober.urls[1] != "https://github.com/" {
		t.Fatalf("probe URLs = %v", prober.urls)
	}
}

func createCandidateNode(t *testing.T, ctx context.Context) (*store.Store, string) {
	t.Helper()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secret.New(make([]byte, 32))
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	service, err := subscription.NewService(
		database,
		box,
		subscription.WithRefresh(subscription.NewDefaultSourceLoader(), filepath.Join(directory, "snapshots")),
		subscription.WithParser(nodeparse.Parse),
	)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	created, err := service.Create(ctx, subscription.CreateRequest{
		Name:         "quality-node",
		SourceType:   subscription.SourceInline,
		SourceConfig: subscription.SourceConfig{Inline: "socks5://127.0.0.1:1080#quality"},
	})
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, created.ID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	nodes, err := database.ListNodes(ctx, store.NodeFilter{Limit: 10})
	if err != nil || len(nodes) != 1 {
		database.Close()
		t.Fatalf("ListNodes() = (%+v, %v)", nodes, err)
	}
	return database, nodes[0].ID
}
