package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestQualitySettingsDefaultsAndPersistence(t *testing.T) {
	ctx := context.Background()
	database, _ := createCandidateNode(t, ctx)
	defer database.Close()
	service, err := NewService(database)
	if err != nil {
		t.Fatal(err)
	}

	settings, err := service.QualitySettings(ctx)
	if err != nil {
		t.Fatalf("QualitySettings() error = %v", err)
	}
	if !reflect.DeepEqual(settings, DefaultQualitySettings()) {
		t.Fatalf("initial settings = %+v, want defaults", settings)
	}

	updated, err := service.UpdateQualitySettings(ctx, QualitySettings{
		CheckIntervalSeconds: 120,
		TimeoutSeconds:       5,
		BatchSize:            50,
		TestURL:              " http://cp.cloudflare.com/generate_204 ",
	})
	if err != nil {
		t.Fatalf("UpdateQualitySettings() error = %v", err)
	}
	if updated.TestURL != "http://cp.cloudflare.com/generate_204" {
		t.Fatalf("test URL not trimmed: %q", updated.TestURL)
	}

	reloaded, err := service.QualitySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded, updated) {
		t.Fatalf("reloaded settings = %+v, want %+v", reloaded, updated)
	}
}

func TestUpdateQualitySettingsRejectsInvalidValues(t *testing.T) {
	ctx := context.Background()
	database, _ := createCandidateNode(t, ctx)
	defer database.Close()
	service, err := NewService(database)
	if err != nil {
		t.Fatal(err)
	}

	invalid := []QualitySettings{
		{CheckIntervalSeconds: 30, TimeoutSeconds: 8, BatchSize: 20, TestURL: "https://example.com"},
		{CheckIntervalSeconds: 600, TimeoutSeconds: 0, BatchSize: 20, TestURL: "https://example.com"},
		{CheckIntervalSeconds: 600, TimeoutSeconds: 8, BatchSize: 0, TestURL: "https://example.com"},
		{CheckIntervalSeconds: 600, TimeoutSeconds: 8, BatchSize: 20, TestURL: "ftp://example.com"},
	}
	for index, settings := range invalid {
		if _, err := service.UpdateQualitySettings(ctx, settings); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("case %d: error = %v, want ErrInvalidSettings", index, err)
		}
	}
}

func TestCheckDueUsesConfiguredSettings(t *testing.T) {
	ctx := context.Background()
	database, nodeID := createCandidateNode(t, ctx)
	defer database.Close()
	prober := &recordingProber{latency: 90}
	service, err := NewService(database, WithProber(prober))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateQualitySettings(ctx, QualitySettings{
		CheckIntervalSeconds: 60,
		TimeoutSeconds:       3,
		BatchSize:            10,
		TestURL:              "https://custom.example/generate_204",
	}); err != nil {
		t.Fatal(err)
	}

	results, err := service.CheckDue(ctx)
	if err != nil {
		t.Fatalf("CheckDue() error = %v", err)
	}
	if len(results) != 1 || results[0].Node.ID != nodeID {
		t.Fatalf("results = %+v, want the single candidate node", results)
	}
	if prober.lastURL != "https://custom.example/generate_204" {
		t.Fatalf("test URL = %q, want configured URL", prober.lastURL)
	}
	if prober.lastTimeout != 3*time.Second {
		t.Fatalf("timeout = %v, want 3s", prober.lastTimeout)
	}

	// The node was just checked, so a second pass has nothing due.
	results, err = service.CheckDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("second pass results = %+v, want none", results)
	}
}

type recordingProber struct {
	latency     int
	lastURL     string
	lastTimeout time.Duration
}

func (prober *recordingProber) Apply(context.Context) error { return nil }

func (prober *recordingProber) TestProxy(_ context.Context, _ string, url string, timeout time.Duration) (int, error) {
	prober.lastURL = url
	prober.lastTimeout = timeout
	return prober.latency, nil
}
