// Package alert implements the alert state machine and notification
// channels. Detectors report currently-true findings; the service compares
// them with persisted firing alerts, creates new alerts, re-notifies after a
// cooldown, and emits a single recovery notification when a finding clears.
// State lives in SQLite so a restart never re-sends the same alert storm.
package alert

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

const (
	SeverityWarning  = "warning"
	SeverityCritical = "critical"

	StatusFiring   = "firing"
	StatusResolved = "resolved"
)

var (
	ErrNotFound       = errors.New("alert not found")
	ErrInvalidSetting = errors.New("invalid alert settings")
	ErrNoChannel      = errors.New("alert channel is not configured")
)

const (
	defaultRepeatInterval = 6 * time.Hour
	resolvedRetention     = 30 * 24 * time.Hour
)

// Finding is one currently-true alert condition.
type Finding struct {
	Rule       string
	TargetID   string
	TargetName string
	Severity   string
	Message    string
}

// Detector reports the findings that are true right now. Returning an empty
// slice means every previously firing alert from this detector recovered.
type Detector interface {
	Name() string
	Detect(context.Context) ([]Finding, error)
}

// Channel delivers one notification. Implementations must respect ctx.
type Channel interface {
	Send(ctx context.Context, subject, body string) error
}

type Repository interface {
	CreateAlert(context.Context, store.AlertRecord) error
	ListAlerts(context.Context, string, int) ([]store.AlertRecord, error)
	ResolveAlert(context.Context, string, time.Time) error
	MarkAlertNotified(context.Context, string, time.Time) error
	AcknowledgeAlert(context.Context, string) error
	PruneResolvedAlerts(context.Context, time.Time) error
	GetAlertSettings(context.Context) (store.AlertSettingsRecord, error)
	UpsertAlertSettings(context.Context, store.AlertSettingsRecord) error
}

type Cipher interface {
	Seal(plaintext, additionalData []byte) ([]byte, error)
	Open(ciphertext, additionalData []byte) ([]byte, error)
}

// ChannelFactory builds a channel from decrypted SMTP settings.
type ChannelFactory func(SMTPConfig) Channel

type Service struct {
	repository     Repository
	detectors      []Detector
	cipher         Cipher
	channelFactory ChannelFactory
	logger         *slog.Logger
	repeatInterval time.Duration
	now            func() time.Time
}

func NewService(repository Repository, cipher Cipher, detectors []Detector, logger *slog.Logger) (*Service, error) {
	if repository == nil {
		return nil, errors.New("alert repository is required")
	}
	if cipher == nil {
		return nil, errors.New("alert cipher is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repository:     repository,
		detectors:      detectors,
		cipher:         cipher,
		channelFactory: func(config SMTPConfig) Channel { return NewSMTPChannel(config) },
		logger:         logger,
		repeatInterval: defaultRepeatInterval,
		now:            time.Now,
	}, nil
}

// Evaluate runs every detector, reconciles alert states and sends pending
// notifications. A failing detector or channel only logs; evaluation of the
// remaining rules continues.
func (s *Service) Evaluate(ctx context.Context) error {
	findings := make(map[string]Finding)
	for _, detector := range s.detectors {
		detected, err := detector.Detect(ctx)
		if err != nil {
			s.logger.Warn("alert detector failed", "detector", detector.Name(), "error", err)
			continue
		}
		for _, finding := range detected {
			if finding.Rule == "" || finding.TargetID == "" {
				continue
			}
			findings[findingKey(finding.Rule, finding.TargetID)] = finding
		}
	}
	firing, err := s.repository.ListAlerts(ctx, StatusFiring, 500)
	if err != nil {
		return err
	}
	channel := s.loadChannel(ctx)
	now := s.now().UTC()

	firingByKey := make(map[string]store.AlertRecord, len(firing))
	for _, record := range firing {
		firingByKey[findingKey(record.Rule, record.TargetID)] = record
	}

	// New findings become firing alerts; known ones may re-notify.
	keys := make([]string, 0, len(findings))
	for key := range findings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		finding := findings[key]
		record, exists := firingByKey[key]
		if !exists {
			record = store.AlertRecord{
				ID:         newAlertID(),
				Rule:       finding.Rule,
				TargetID:   finding.TargetID,
				TargetName: finding.TargetName,
				Severity:   finding.Severity,
				Status:     StatusFiring,
				Message:    finding.Message,
				FiredAt:    now,
			}
			if err := s.repository.CreateAlert(ctx, record); err != nil {
				if errors.Is(err, store.ErrConflict) {
					continue
				}
				return err
			}
			s.notify(ctx, channel, record, false, now)
			continue
		}
		if record.Acknowledged {
			continue
		}
		if record.LastNotifiedAt == nil || now.Sub(*record.LastNotifiedAt) >= s.repeatInterval {
			s.notify(ctx, channel, record, false, now)
		}
	}

	// Firing alerts without a matching finding recovered.
	for key, record := range firingByKey {
		if _, stillFiring := findings[key]; stillFiring {
			continue
		}
		if err := s.repository.ResolveAlert(ctx, record.ID, now); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		s.notify(ctx, channel, record, true, now)
	}

	if err := s.repository.PruneResolvedAlerts(ctx, now.Add(-resolvedRetention)); err != nil {
		s.logger.Warn("prune resolved alerts failed", "error", err)
	}
	return nil
}

func (s *Service) List(ctx context.Context, status string, limit int) ([]store.AlertRecord, error) {
	switch status {
	case "", StatusFiring, StatusResolved:
	default:
		return nil, fmt.Errorf("%w: unknown status %q", ErrInvalidSetting, status)
	}
	return s.repository.ListAlerts(ctx, status, limit)
}

func (s *Service) Acknowledge(ctx context.Context, id string) error {
	if err := s.repository.AcknowledgeAlert(ctx, id); errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}

// notify sends one notification and records success. Send failures keep
// last_notified_at unchanged so the next evaluation retries.
func (s *Service) notify(ctx context.Context, channel Channel, record store.AlertRecord, recovered bool, now time.Time) {
	if channel == nil {
		return
	}
	subject, body := renderNotification(record, recovered)
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := channel.Send(sendCtx, subject, body); err != nil {
		s.logger.Warn("alert notification failed", "alert_id", record.ID, "rule", record.Rule, "error", err)
		return
	}
	if !recovered {
		if err := s.repository.MarkAlertNotified(ctx, record.ID, now); err != nil {
			s.logger.Warn("mark alert notified failed", "alert_id", record.ID, "error", err)
		}
	}
}

// renderNotification builds the mail content. Messages must not contain
// subscription URLs, tokens or credentials; detectors only pass names and
// counters.
func renderNotification(record store.AlertRecord, recovered bool) (string, string) {
	state := "FIRING"
	if recovered {
		state = "RESOLVED"
	}
	subject := fmt.Sprintf("[HX-Pxy] %s %s: %s", state, record.Severity, record.Rule)
	var body strings.Builder
	fmt.Fprintf(&body, "Alert: %s\r\n", record.Rule)
	fmt.Fprintf(&body, "State: %s\r\n", state)
	fmt.Fprintf(&body, "Severity: %s\r\n", record.Severity)
	fmt.Fprintf(&body, "Target: %s\r\n", record.TargetName)
	fmt.Fprintf(&body, "Message: %s\r\n", record.Message)
	fmt.Fprintf(&body, "Fired at: %s\r\n", record.FiredAt.UTC().Format(time.RFC3339))
	return subject, body.String()
}

func (s *Service) loadChannel(ctx context.Context) Channel {
	config, enabled, err := s.smtpConfig(ctx)
	if err != nil || !enabled {
		return nil
	}
	return s.channelFactory(config)
}

func findingKey(rule, targetID string) string {
	return rule + "|" + targetID
}

func newAlertID() string {
	var buffer [12]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("alert-%d", time.Now().UnixNano())
	}
	return "alert-" + hex.EncodeToString(buffer[:])
}
