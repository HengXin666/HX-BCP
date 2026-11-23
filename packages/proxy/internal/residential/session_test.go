package residential

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestSessionPoolRefreshAgeUsesVendorLifetimeUnits(t *testing.T) {
	t.Parallel()

	bestProxy := Provider{
		Vendor:            "bestproxy",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 30,
	}
	if got, want := SessionPoolLifetime(bestProxy), 30*time.Minute; got != want {
		t.Fatalf("BestProxy lifetime = %s, want %s", got, want)
	}
	if got, want := SessionPoolRefreshAge(bestProxy), 24*time.Minute; got != want {
		t.Fatalf("BestProxy refresh age = %s, want %s", got, want)
	}

	apiList := bestProxy
	apiList.RotationMode = RotationAPIList
	apiList.APIURL = "https://api.example.com/nodes?life=15&num=8"
	if got, want := SessionPoolLifetime(apiList), 15*time.Minute; got != want {
		t.Fatalf("BestProxy API-list lifetime = %s, want %s", got, want)
	}

	generic := Provider{Vendor: "generic", RotationMode: RotationSessionTemplate, SessionTTLSeconds: 600}
	if got, want := SessionPoolLifetime(generic), 10*time.Minute; got != want {
		t.Fatalf("generic lifetime = %s, want %s", got, want)
	}

	rapid := Provider{Vendor: "rapidproxy", RotationMode: RotationSessionTemplate, SessionTTLSeconds: 60}
	if got, want := SessionPoolLifetime(rapid), 60*time.Minute; got != want {
		t.Fatalf("RapidProxy lifetime = %s, want %s", got, want)
	}
	if got, want := SessionPoolRefreshAge(rapid), 48*time.Minute; got != want {
		t.Fatalf("RapidProxy refresh age = %s, want %s", got, want)
	}
}

// Residential gateways such as BestProxy constrain the sticky session id to
// 4-12 alphanumeric characters. The generated id must stay inside that bound so
// a channel pool actually works against those gateways.
func TestNewSessionIDIsWithinVendorBounds(t *testing.T) {
	t.Parallel()

	for range 64 {
		sessionID, err := newSessionID()
		if err != nil {
			t.Fatalf("newSessionID() error = %v", err)
		}
		if len(sessionID) < 4 || len(sessionID) > 12 {
			t.Fatalf("session id %q has length %d, want 4-12", sessionID, len(sessionID))
		}
		if _, err := hex.DecodeString(sessionID); err != nil {
			t.Fatalf("session id %q is not hex: %v", sessionID, err)
		}
	}
}

// Every pool slot must carry its own session id, so parallel clients pinned to
// different slots exit from different residential IPs instead of sharing one.
func TestBuildSessionsAssignsUniqueSessionIDs(t *testing.T) {
	t.Parallel()

	provider := Provider{
		Protocol:          "http",
		GatewayHost:       "proxy.bestproxy.com",
		GatewayPort:       2312,
		UsernameTemplate:  "{user}_area-{region}_life-{ttl}_session-{session}",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 60,
	}
	credentials := Credentials{Username: "acct123", Password: "s3cret"}

	sessions, err := buildSessions(provider, credentials, "US", 8)
	if err != nil {
		t.Fatalf("buildSessions() error = %v", err)
	}
	if len(sessions) != 8 {
		t.Fatalf("pool size = %d, want 8", len(sessions))
	}
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if session.ID == "" {
			t.Fatal("session id must not be empty")
		}
		if !strings.Contains(session.Username, "_session-"+session.ID) {
			t.Fatalf("session username %q does not embed its session id", session.Username)
		}
		if !strings.HasPrefix(session.Username, "acct123_area-US_life-60_session-") {
			t.Fatalf("session username %q does not follow the BestProxy syntax", session.Username)
		}
		if _, duplicate := seen[session.ID]; duplicate {
			t.Fatalf("duplicate session id %q in pool", session.ID)
		}
		seen[session.ID] = struct{}{}
	}
}

func TestCanonicalNodeConfigCarriesDialerProxyGroupID(t *testing.T) {
	t.Parallel()

	config := canonicalNodeConfig(
		Provider{
			Protocol:             "http",
			GatewayHost:          "proxy.example.com",
			GatewayPort:          2312,
			UpstreamProxyGroupID: "group-overseas",
		},
		Session{Index: 0, Username: "account"},
		"password",
		"residential #01",
	)
	if got := config["hx_dialer_proxy_group_id"]; got != "group-overseas" {
		t.Fatalf("dialer group metadata = %v, want group-overseas", got)
	}
}
