// Package entry parses the credentials a client presents to the unified proxy
// entry. The username speaks the same key-value dialect residential vendors use,
// so any client that can set a proxy username can pick a pool, an exit region and
// a sticky slot without calling an API.
package entry

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidUsername reports a username the entry cannot route.
var ErrInvalidUsername = errors.New("invalid entry username")

// Pool classes a client can ask for.
const (
	PoolResidential             = "resi"
	PoolSubscriptionResidential = "subresi"
	PoolSubscription            = "sub"
	PoolDirect                  = "direct"
)

// Slot loss policies, applied when the node bound to a slot disappears.
const (
	OnLossRebind = "rebind"
	OnLossHold   = "hold"
	OnLossFail   = "fail"
)

const (
	maximumUsernameLength = 256
	maximumSlotLength     = 64
	maximumTTLMinutes     = 24 * 60
)

// Selector is what one entry username asks for.
type Selector struct {
	Tenant  string
	Pool    string
	Group   string
	Country string
	City    string
	ASN     int
	Slot    string
	TTL     int
	OnLoss  string
}

// Sticky reports whether the selector pins its exit to a slot.
func (s Selector) Sticky() bool {
	return s.Slot != ""
}

// Pools lists the accepted pool classes in display order.
func Pools() []string {
	return []string{PoolResidential, PoolSubscriptionResidential, PoolSubscription, PoolDirect}
}

// Parse reads tenant-key-value-key-value... into a Selector. The first segment
// is the tenant, keys are case-insensitive and may come in any order, unknown
// keys and repeated keys are rejected. TTL is in minutes and defaults to 30 for a
// sticky selector. Pool defaults to sub when no group is named.
func Parse(username string) (Selector, error) {
	if username == "" || len(username) > maximumUsernameLength {
		return Selector{}, fmt.Errorf("%w: length must be 1..%d", ErrInvalidUsername, maximumUsernameLength)
	}
	for _, character := range username {
		if character <= ' ' || character == ':' || character > 0x7e {
			return Selector{}, fmt.Errorf("%w: only printable ASCII without ':' is allowed", ErrInvalidUsername)
		}
	}
	parts := strings.Split(username, "-")
	selector := Selector{Tenant: parts[0]}
	if !validToken(selector.Tenant, maximumSlotLength) {
		return Selector{}, fmt.Errorf("%w: tenant %q", ErrInvalidUsername, selector.Tenant)
	}
	rest := parts[1:]
	if len(rest)%2 != 0 {
		return Selector{}, fmt.Errorf("%w: key %q has no value", ErrInvalidUsername, rest[len(rest)-1])
	}
	seen := make(map[string]bool, len(rest)/2)
	for index := 0; index < len(rest); index += 2 {
		key := canonicalKey(strings.ToLower(rest[index]))
		value := rest[index+1]
		if seen[key] {
			return Selector{}, fmt.Errorf("%w: key %q repeated", ErrInvalidUsername, key)
		}
		seen[key] = true
		if err := selector.set(key, value); err != nil {
			return Selector{}, err
		}
	}
	return selector.withDefaults()
}

// canonicalKey folds the common vendor spellings onto one key.
func canonicalKey(key string) string {
	switch key {
	case "cc", "country", "region", "area":
		return "cc"
	case "session", "sessid", "sid", "slot":
		return "session"
	case "ttl", "sesstime", "stime", "life":
		return "ttl"
	case "group", "g":
		return "group"
	default:
		return key
	}
}

// set applies one key-value pair to the selector.
func (s *Selector) set(key, value string) error {
	switch key {
	case "pool":
		value = strings.ToLower(value)
		for _, known := range Pools() {
			if value == known {
				s.Pool = value
				return nil
			}
		}
		return fmt.Errorf("%w: pool %q, supported: %s", ErrInvalidUsername, value, strings.Join(Pools(), ", "))
	case "group":
		if !validToken(value, maximumSlotLength) {
			return fmt.Errorf("%w: group %q", ErrInvalidUsername, value)
		}
		s.Group = value
	case "cc":
		if len(value) != 2 || !isLetters(value) {
			return fmt.Errorf("%w: country %q must be ISO 3166 alpha-2", ErrInvalidUsername, value)
		}
		s.Country = strings.ToUpper(value)
	case "city":
		if !validToken(value, maximumSlotLength) {
			return fmt.Errorf("%w: city %q", ErrInvalidUsername, value)
		}
		s.City = strings.ToLower(value)
	case "asn":
		number, err := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(value), "AS"))
		if err != nil || number <= 0 {
			return fmt.Errorf("%w: asn %q", ErrInvalidUsername, value)
		}
		s.ASN = number
	case "session":
		if !validToken(value, maximumSlotLength) {
			return fmt.Errorf("%w: session %q", ErrInvalidUsername, value)
		}
		s.Slot = value
	case "ttl":
		minutes, err := strconv.Atoi(value)
		if err != nil || minutes < 1 || minutes > maximumTTLMinutes {
			return fmt.Errorf("%w: ttl %q must be 1..%d minutes", ErrInvalidUsername, value, maximumTTLMinutes)
		}
		s.TTL = minutes
	case "loss":
		value = strings.ToLower(value)
		if value != OnLossRebind && value != OnLossHold && value != OnLossFail {
			return fmt.Errorf("%w: loss %q, supported: rebind, hold, fail", ErrInvalidUsername, value)
		}
		s.OnLoss = value
	default:
		return fmt.Errorf("%w: unknown key %q", ErrInvalidUsername, key)
	}
	return nil
}

// withDefaults fills the fields a client may omit and rejects contradictions.
func (s Selector) withDefaults() (Selector, error) {
	if s.Pool == "" && s.Group == "" {
		s.Pool = PoolSubscription
	}
	if s.Pool == PoolDirect && (s.Country != "" || s.City != "" || s.ASN != 0) {
		return Selector{}, fmt.Errorf("%w: pool direct takes no exit filters", ErrInvalidUsername)
	}
	if s.Slot == "" && (s.TTL != 0 || s.OnLoss != "") {
		return Selector{}, fmt.Errorf("%w: ttl and loss need a session", ErrInvalidUsername)
	}
	if s.Slot != "" && s.TTL == 0 {
		s.TTL = 30
	}
	if s.Slot != "" && s.OnLoss == "" {
		s.OnLoss = OnLossRebind
	}
	return s, nil
}

// validToken accepts letters, digits and underscore up to a length limit.
func validToken(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, character := range value {
		isLetter := (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
		isDigit := character >= '0' && character <= '9'
		if !isLetter && !isDigit && character != '_' {
			return false
		}
	}
	return true
}

// isLetters reports whether a value holds ASCII letters only.
func isLetters(value string) bool {
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')) {
			return false
		}
	}
	return true
}
