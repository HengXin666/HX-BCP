// Package residential adds dynamic residential IP proxy support. A residential
// vendor is not a subscription: it is a single gateway endpoint plus one account
// whose *username* carries the routing parameters (region, sticky session id,
// session lifetime). This package owns the vendor-facing rendering rules, the
// provider and channel domain model, and the exit-IP rotation state machine.
package residential

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidTemplate reports a username template that cannot be rendered.
var ErrInvalidTemplate = errors.New("invalid username template")

const (
	maximumTemplateLength = 512
	maximumUsernameLength = 256
)

// Placeholder names accepted inside a username template. Vendors differ only in
// the literal separators around these values, so a template plus this fixed set
// covers the common gateway dialects without hard-coding any single vendor.
const (
	PlaceholderUser    = "user"
	PlaceholderSession = "session"
	PlaceholderRegion  = "region"
	PlaceholderCountry = "country"
	PlaceholderCity    = "city"
	PlaceholderTTL     = "ttl"
)

// SupportedPlaceholders lists every placeholder a template may reference, in a
// stable order suitable for display in the admin UI.
func SupportedPlaceholders() []string {
	return []string{
		PlaceholderUser,
		PlaceholderSession,
		PlaceholderRegion,
		PlaceholderCountry,
		PlaceholderCity,
		PlaceholderTTL,
	}
}

// Variables carries the values substituted into a username template.
type Variables struct {
	User    string
	Session string
	Region  string
	Country string
	City    string
	TTL     string
}

func (v Variables) lookup(name string) (string, bool) {
	switch name {
	case PlaceholderUser:
		return v.User, true
	case PlaceholderSession:
		return v.Session, true
	case PlaceholderRegion:
		return v.Region, true
	case PlaceholderCountry:
		return v.Country, true
	case PlaceholderCity:
		return v.City, true
	case PlaceholderTTL:
		return v.TTL, true
	default:
		return "", false
	}
}

// ValidateTemplate checks a template without rendering it. It is used when an
// administrator saves a provider so a malformed template is rejected at write
// time rather than at data-plane compile time.
func ValidateTemplate(template string) error {
	if strings.TrimSpace(template) == "" {
		return fmt.Errorf("%w: template is empty", ErrInvalidTemplate)
	}
	if len(template) > maximumTemplateLength {
		return fmt.Errorf("%w: template exceeds %d characters", ErrInvalidTemplate, maximumTemplateLength)
	}
	names, err := placeholderNames(template)
	if err != nil {
		return err
	}
	if _, references := names[PlaceholderUser]; !references {
		return fmt.Errorf("%w: template must reference {%s}", ErrInvalidTemplate, PlaceholderUser)
	}
	return nil
}

// TemplateUsesSession reports whether a template pins a sticky session. A
// template without {session} cannot produce distinct exit IPs per pool slot, so
// sticky channels require one.
func TemplateUsesSession(template string) bool {
	names, err := placeholderNames(template)
	if err != nil {
		return false
	}
	_, uses := names[PlaceholderSession]
	return uses
}

// Render substitutes variables into a username template.
//
// The result is used as the proxy username in a user:password credential pair,
// so any character that would break that framing (colon, whitespace, control
// characters) is rejected instead of silently escaped.
func Render(template string, variables Variables) (string, error) {
	if err := ValidateTemplate(template); err != nil {
		return "", err
	}
	if strings.TrimSpace(variables.User) == "" {
		return "", fmt.Errorf("%w: gateway username is empty", ErrInvalidTemplate)
	}

	var builder strings.Builder
	builder.Grow(len(template) + 32)
	remaining := template
	for {
		start := strings.IndexByte(remaining, '{')
		if start < 0 {
			builder.WriteString(remaining)
			break
		}
		builder.WriteString(remaining[:start])
		end := strings.IndexByte(remaining[start:], '}')
		if end < 0 {
			return "", fmt.Errorf("%w: unterminated placeholder", ErrInvalidTemplate)
		}
		name := remaining[start+1 : start+end]
		value, known := variables.lookup(name)
		if !known {
			return "", fmt.Errorf("%w: unknown placeholder %q", ErrInvalidTemplate, name)
		}
		if err := validateSegment(name, value); err != nil {
			return "", err
		}
		builder.WriteString(value)
		remaining = remaining[start+end+1:]
	}

	rendered := builder.String()
	if rendered == "" {
		return "", fmt.Errorf("%w: rendered username is empty", ErrInvalidTemplate)
	}
	if len(rendered) > maximumUsernameLength {
		return "", fmt.Errorf("%w: rendered username exceeds %d characters", ErrInvalidTemplate, maximumUsernameLength)
	}
	return rendered, nil
}

// placeholderNames extracts the placeholder set referenced by a template and
// rejects unknown or malformed placeholders.
func placeholderNames(template string) (map[string]struct{}, error) {
	names := make(map[string]struct{}, len(SupportedPlaceholders()))
	remaining := template
	for {
		start := strings.IndexByte(remaining, '{')
		if start < 0 {
			return names, nil
		}
		end := strings.IndexByte(remaining[start:], '}')
		if end < 0 {
			return nil, fmt.Errorf("%w: unterminated placeholder", ErrInvalidTemplate)
		}
		name := remaining[start+1 : start+end]
		if name == "" {
			return nil, fmt.Errorf("%w: empty placeholder", ErrInvalidTemplate)
		}
		if _, known := (Variables{}).lookup(name); !known {
			return nil, fmt.Errorf(
				"%w: unknown placeholder %q, supported: %s",
				ErrInvalidTemplate,
				name,
				strings.Join(SupportedPlaceholders(), ", "),
			)
		}
		names[name] = struct{}{}
		remaining = remaining[start+end+1:]
	}
}

// validateSegment rejects substituted values that would corrupt the credential
// framing or leak into an adjacent field.
func validateSegment(name, value string) error {
	if value == "" {
		// An empty optional value is allowed: templates commonly leave region
		// blank to mean "any". The caller strips the surrounding separators by
		// choosing a template that tolerates it.
		return nil
	}
	for _, character := range value {
		switch {
		case character == ':':
			return fmt.Errorf("%w: %s value must not contain ':'", ErrInvalidTemplate, name)
		case character == '{' || character == '}':
			return fmt.Errorf("%w: %s value must not contain braces", ErrInvalidTemplate, name)
		case character <= ' ' || character == 0x7f:
			return fmt.Errorf("%w: %s value must not contain whitespace or control characters", ErrInvalidTemplate, name)
		case character > 0x7f:
			return fmt.Errorf("%w: %s value must be ASCII", ErrInvalidTemplate, name)
		}
	}
	return nil
}
