package alert

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPChannel sends notifications via standard SMTP with optional STARTTLS
// or implicit TLS. It opens one short-lived connection per notification; the
// alert service already bounds the send rate via cooldowns.
type SMTPChannel struct {
	config SMTPConfig
	// dialTimeout bounds connection establishment; the caller's context
	// bounds the full delivery.
	dialTimeout time.Duration
}

func NewSMTPChannel(config SMTPConfig) *SMTPChannel {
	return &SMTPChannel{config: config, dialTimeout: 10 * time.Second}
}

func (c *SMTPChannel) Send(ctx context.Context, subject, body string) error {
	address := net.JoinHostPort(c.config.Host, fmt.Sprintf("%d", c.config.Port))
	dialer := &net.Dialer{Timeout: c.dialTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("dial smtp server: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if c.config.Security == "tls" {
		connection = tls.Client(connection, &tls.Config{ServerName: c.config.Host})
	}
	client, err := smtp.NewClient(connection, c.config.Host)
	if err != nil {
		_ = connection.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if c.config.Security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp server does not offer STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: c.config.Host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if c.config.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp server does not offer AUTH")
		}
		auth := smtp.PlainAuth("", c.config.Username, c.config.Password, c.config.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(c.config.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, recipient := range c.config.To {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt %q: %w", recipient, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	message := buildMessage(c.config.From, c.config.To, subject, body)
	if _, err := writer.Write([]byte(message)); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp finish body: %w", err)
	}
	return client.Quit()
}

func buildMessage(from string, to []string, subject, body string) string {
	var message strings.Builder
	fmt.Fprintf(&message, "From: %s\r\n", from)
	fmt.Fprintf(&message, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&message, "Subject: %s\r\n", subject)
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&message, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	message.WriteString("\r\n")
	message.WriteString(body)
	if !strings.HasSuffix(body, "\r\n") {
		message.WriteString("\r\n")
	}
	return message.String()
}
