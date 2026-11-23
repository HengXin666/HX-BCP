package alert

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/secret"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type stubDetector struct {
	name     string
	mutex    sync.Mutex
	findings []Finding
	err      error
}

func (d *stubDetector) Name() string { return d.name }

func (d *stubDetector) Detect(context.Context) ([]Finding, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return append([]Finding(nil), d.findings...), d.err
}

func (d *stubDetector) set(findings ...Finding) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.findings = findings
}

type recordingChannel struct {
	mutex    sync.Mutex
	subjects []string
	fail     bool
}

func (c *recordingChannel) Send(_ context.Context, subject, _ string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.fail {
		return errors.New("smtp unreachable")
	}
	c.subjects = append(c.subjects, subject)
	return nil
}

func (c *recordingChannel) sent() []string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]string(nil), c.subjects...)
}

func newAlertTestService(t *testing.T, detectors ...Detector) (*Service, *recordingChannel, *store.Store) {
	t.Helper()
	root := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(root, "alerts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	box, err := secret.LoadOrCreate(filepath.Join(root, "master.key"))
	if err != nil {
		t.Fatalf("load secret box: %v", err)
	}
	service, err := NewService(database, box, detectors, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new alert service: %v", err)
	}
	channel := &recordingChannel{}
	service.channelFactory = func(SMTPConfig) Channel { return channel }
	// Store enabled settings so evaluations use the recording channel.
	if _, err := service.UpdateSettings(context.Background(), UpdateSettingsRequest{
		Enabled:  true,
		Host:     "mail.example.com",
		Port:     587,
		Security: "starttls",
		Username: "alerts",
		Password: "secret",
		From:     "alerts@example.com",
		To:       []string{"admin@example.com"},
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	return service, channel, database
}

func TestAlertLifecycleFireCooldownRecover(t *testing.T) {
	detector := &stubDetector{name: "stub"}
	service, channel, _ := newAlertTestService(t, detector)
	ctx := context.Background()

	finding := Finding{
		Rule: "subscription-refresh-failing", TargetID: "sub-1", TargetName: "example",
		Severity: SeverityWarning, Message: "refresh failed 3 times",
	}
	detector.set(finding)
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := channel.sent(); len(sent) != 1 {
		t.Fatalf("expected 1 firing notification, got %v", sent)
	}

	// Same finding again inside the cooldown: no duplicate mail.
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := channel.sent(); len(sent) != 1 {
		t.Fatalf("cooldown must suppress repeats, got %v", sent)
	}

	// After the repeat interval the alert re-notifies.
	service.now = func() time.Time { return time.Now().Add(7 * time.Hour) }
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := channel.sent(); len(sent) != 2 {
		t.Fatalf("expected re-notification after cooldown, got %v", sent)
	}

	// Recovery resolves the alert and sends exactly one resolved mail.
	detector.set()
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	sent := channel.sent()
	if len(sent) != 3 || !strings.Contains(sent[2], "RESOLVED") {
		t.Fatalf("expected one RESOLVED notification, got %v", sent)
	}
	firing, err := service.List(ctx, StatusFiring, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(firing) != 0 {
		t.Fatalf("expected no firing alerts, got %d", len(firing))
	}
	resolved, err := service.List(ctx, StatusResolved, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].ResolvedAt == nil {
		t.Fatalf("expected one resolved alert with timestamp, got %+v", resolved)
	}

	// The same condition returning later fires a fresh alert.
	detector.set(finding)
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := channel.sent(); len(sent) != 4 {
		t.Fatalf("expected a new firing notification, got %v", sent)
	}
}

func TestAcknowledgedAlertsStaySilent(t *testing.T) {
	detector := &stubDetector{name: "stub"}
	service, channel, _ := newAlertTestService(t, detector)
	ctx := context.Background()
	detector.set(Finding{
		Rule: "proxy-group-empty", TargetID: "group-1", TargetName: "asia",
		Severity: SeverityCritical, Message: "no nodes",
	})
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	firing, err := service.List(ctx, StatusFiring, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Acknowledge(ctx, firing[0].ID); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := channel.sent(); len(sent) != 1 {
		t.Fatalf("acknowledged alert must not re-notify, got %v", sent)
	}
	if err := service.Acknowledge(ctx, "alert-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestNotificationFailureRetriesNextCycle(t *testing.T) {
	detector := &stubDetector{name: "stub"}
	service, channel, _ := newAlertTestService(t, detector)
	ctx := context.Background()
	channel.fail = true
	detector.set(Finding{
		Rule: "dataplane-not-running", TargetID: "mihomo", TargetName: "Mihomo data plane",
		Severity: SeverityCritical, Message: "process exited",
	})
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := channel.sent(); len(sent) != 0 {
		t.Fatalf("failed sends must not be recorded, got %v", sent)
	}
	// The alert exists even though the mail failed.
	firing, err := service.List(ctx, StatusFiring, 10)
	if err != nil || len(firing) != 1 {
		t.Fatalf("alert must persist despite channel failure: %v %d", err, len(firing))
	}
	channel.fail = false
	if err := service.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := channel.sent(); len(sent) != 1 {
		t.Fatalf("expected retry to deliver, got %v", sent)
	}
}

func TestDetectorFailureDoesNotBlockOthers(t *testing.T) {
	broken := &stubDetector{name: "broken", err: errors.New("query failed")}
	working := &stubDetector{name: "working"}
	working.set(Finding{
		Rule: "subscription-no-nodes", TargetID: "sub-9", TargetName: "empty",
		Severity: SeverityCritical, Message: "zero nodes",
	})
	service, channel, _ := newAlertTestService(t, broken, working)
	if err := service.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent := channel.sent(); len(sent) != 1 {
		t.Fatalf("working detector must still alert, got %v", sent)
	}
}

func TestSettingsRedactionAndPasswordKeeping(t *testing.T) {
	service, _, _ := newAlertTestService(t)
	ctx := context.Background()
	view, err := service.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Configured || !view.HasPassword {
		t.Fatalf("expected configured settings, got %+v", view)
	}
	// Update without a password keeps the stored secret.
	if _, err := service.UpdateSettings(ctx, UpdateSettingsRequest{
		Enabled: true, Host: "mail.example.com", Port: 465, Security: "tls",
		Username: "alerts", From: "alerts@example.com", To: []string{"admin@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	config, _, err := service.smtpConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if config.Password != "secret" {
		t.Fatalf("password must be kept, got %q", config.Password)
	}
	if _, err := service.UpdateSettings(ctx, UpdateSettingsRequest{
		Enabled: true, Host: "", Port: 587, Security: "starttls",
		From: "alerts@example.com", To: []string{"admin@example.com"},
	}); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("expected ErrInvalidSetting, got %v", err)
	}
}
