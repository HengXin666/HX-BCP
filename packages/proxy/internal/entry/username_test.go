package entry

import (
	"errors"
	"testing"
)

func TestParseReadsEveryKey(t *testing.T) {
	t.Parallel()

	got, err := Parse("acme-pool-resi-cc-us-city-new_york-asn-AS7922-session-job42-ttl-15-loss-hold")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := Selector{Tenant: "acme", Pool: PoolResidential, Country: "US", City: "new_york", ASN: 7922, Slot: "job42", TTL: 15, OnLoss: OnLossHold}
	if got != want {
		t.Fatalf("Parse() = %+v, want %+v", got, want)
	}
}

func TestParseFoldsVendorSpellings(t *testing.T) {
	t.Parallel()

	got, err := Parse("acme-area-JP-sessid-a1-life-5")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Country != "JP" || got.Slot != "a1" || got.TTL != 5 || got.Pool != PoolSubscription {
		t.Fatalf("Parse() = %+v", got)
	}
}

func TestParseDefaults(t *testing.T) {
	t.Parallel()

	rotating, err := Parse("acme")
	if err != nil {
		t.Fatalf("Parse(acme) error = %v", err)
	}
	if rotating.Pool != PoolSubscription || rotating.Sticky() || rotating.TTL != 0 {
		t.Fatalf("Parse(acme) = %+v", rotating)
	}
	sticky, err := Parse("acme-session-s1")
	if err != nil {
		t.Fatalf("Parse(sticky) error = %v", err)
	}
	if sticky.TTL != 30 || sticky.OnLoss != OnLossRebind {
		t.Fatalf("Parse(sticky) = %+v", sticky)
	}
	grouped, err := Parse("acme-group-hk_fast")
	if err != nil {
		t.Fatalf("Parse(group) error = %v", err)
	}
	if grouped.Pool != "" || grouped.Group != "hk_fast" {
		t.Fatalf("Parse(group) = %+v", grouped)
	}
}

func TestParseRejects(t *testing.T) {
	t.Parallel()

	for _, username := range []string{
		"",
		"acme-pool",
		"acme-pool-vip",
		"acme-cc-usa",
		"acme-cc-us-cc-jp",
		"acme-ttl-10",
		"acme-session-s1-ttl-0",
		"acme-pool-direct-cc-us",
		"acme-bogus-1",
		"ac:me",
		"acme-session-a.b",
	} {
		if _, err := Parse(username); !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalidUsername", username, err)
		}
	}
}
