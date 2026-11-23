package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/HengXin666/hx-pxy/internal/store"
)

const smtpSettingsAAD = "alert:smtp-settings"

// SMTPConfig is stored AEAD-encrypted; the API never echoes the password.
type SMTPConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security string   `json:"security"` // none | starttls | tls
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

// SettingsView is the redacted API representation.
type SettingsView struct {
	Enabled     bool     `json:"enabled"`
	Configured  bool     `json:"configured"`
	Host        string   `json:"host,omitempty"`
	Port        int      `json:"port,omitempty"`
	Security    string   `json:"security,omitempty"`
	Username    string   `json:"username,omitempty"`
	HasPassword bool     `json:"has_password"`
	From        string   `json:"from,omitempty"`
	To          []string `json:"to,omitempty"`
}

// UpdateSettingsRequest carries new settings. An empty password keeps the
// previously stored one so updates never require re-entering the secret.
type UpdateSettingsRequest struct {
	Enabled  bool     `json:"enabled"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security string   `json:"security"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

func (s *Service) Settings(ctx context.Context) (SettingsView, error) {
	config, enabled, err := s.smtpConfig(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return SettingsView{}, nil
	}
	if err != nil {
		return SettingsView{}, err
	}
	return SettingsView{
		Enabled:     enabled,
		Configured:  config.Host != "",
		Host:        config.Host,
		Port:        config.Port,
		Security:    config.Security,
		Username:    config.Username,
		HasPassword: config.Password != "",
		From:        config.From,
		To:          config.To,
	}, nil
}

func (s *Service) UpdateSettings(ctx context.Context, request UpdateSettingsRequest) (SettingsView, error) {
	config := SMTPConfig{
		Host:     strings.TrimSpace(request.Host),
		Port:     request.Port,
		Security: strings.ToLower(strings.TrimSpace(request.Security)),
		Username: strings.TrimSpace(request.Username),
		Password: request.Password,
		From:     strings.TrimSpace(request.From),
		To:       normalizeRecipients(request.To),
	}
	if config.Password == "" {
		// Keep the stored password when the client leaves the field empty.
		previous, _, err := s.smtpConfig(ctx)
		if err == nil {
			config.Password = previous.Password
		}
	}
	if err := validateSMTPConfig(config); err != nil {
		return SettingsView{}, err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return SettingsView{}, fmt.Errorf("encode smtp settings: %w", err)
	}
	sealed, err := s.cipher.Seal(encoded, []byte(smtpSettingsAAD))
	if err != nil {
		return SettingsView{}, fmt.Errorf("encrypt smtp settings: %w", err)
	}
	if err := s.repository.UpsertAlertSettings(ctx, store.AlertSettingsRecord{
		Enabled:             request.Enabled,
		SMTPConfigEncrypted: sealed,
		UpdatedAt:           s.now().UTC(),
	}); err != nil {
		return SettingsView{}, err
	}
	return s.Settings(ctx)
}

// SendTest delivers a test notification through the configured channel.
func (s *Service) SendTest(ctx context.Context) error {
	config, _, err := s.smtpConfig(ctx)
	if err != nil || config.Host == "" {
		return ErrNoChannel
	}
	channel := s.channelFactory(config)
	return channel.Send(ctx,
		"[HX-Pxy] test alert",
		"This is a test notification from HX-Pxy.\r\nIf you received this mail, the SMTP alert channel works.\r\n",
	)
}

func (s *Service) smtpConfig(ctx context.Context) (SMTPConfig, bool, error) {
	record, err := s.repository.GetAlertSettings(ctx)
	if err != nil {
		return SMTPConfig{}, false, err
	}
	if len(record.SMTPConfigEncrypted) == 0 {
		return SMTPConfig{}, record.Enabled, nil
	}
	plaintext, err := s.cipher.Open(record.SMTPConfigEncrypted, []byte(smtpSettingsAAD))
	if err != nil {
		return SMTPConfig{}, false, fmt.Errorf("decrypt smtp settings: %w", err)
	}
	var config SMTPConfig
	if err := json.Unmarshal(plaintext, &config); err != nil {
		return SMTPConfig{}, false, fmt.Errorf("decode smtp settings: %w", err)
	}
	return config, record.Enabled, nil
}

func validateSMTPConfig(config SMTPConfig) error {
	if config.Host == "" {
		return fmt.Errorf("%w: host is required", ErrInvalidSetting)
	}
	if net.ParseIP(config.Host) == nil && strings.ContainsAny(config.Host, " /") {
		return fmt.Errorf("%w: host must be a hostname or IP", ErrInvalidSetting)
	}
	if config.Port < 1 || config.Port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidSetting)
	}
	switch config.Security {
	case "none", "starttls", "tls":
	default:
		return fmt.Errorf("%w: security must be none, starttls or tls", ErrInvalidSetting)
	}
	if config.From == "" || !strings.Contains(config.From, "@") {
		return fmt.Errorf("%w: from must be a mail address", ErrInvalidSetting)
	}
	if len(config.To) == 0 {
		return fmt.Errorf("%w: at least one recipient is required", ErrInvalidSetting)
	}
	for _, recipient := range config.To {
		if !strings.Contains(recipient, "@") {
			return fmt.Errorf("%w: recipient %q is not a mail address", ErrInvalidSetting, recipient)
		}
	}
	return nil
}

func normalizeRecipients(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
