package dockeraccess

import (
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

var docker0 = Bridge{Interface: "docker0", Address: "172.17.0.1", Subnet: "172.17.0.0/16"}
var compose = Bridge{Interface: "br-1234", Address: "172.18.0.1", Subnet: "172.18.0.0/16"}

func service(kind, bind string, auth bool) listener.Listener {
	return listener.Listener{ID: "svc", Name: "svc", Kind: kind, BindAddress: bind, Port: 18080, AuthConfigured: auth, SharePath: "/sub/token", Enabled: true, Version: 3, ProxyGroupID: "group"}
}

func TestAssessReadyForAuthenticatedWildcardUsesBridge(t *testing.T) {
	got := Assess(Input{Source: service("mixed", "0.0.0.0", true), Bridges: []Bridge{docker0}})
	if got.State != StateReady || got.Reason != ReasonDirect || got.Entry == nil {
		t.Fatalf("assessment = %+v", got)
	}
	if got.Entry.Host != "172.17.0.1" || got.Entry.Port != 18080 || got.Entry.Companion {
		t.Fatalf("entry = %+v", got.Entry)
	}
	if len(got.Entry.Schemes) != 2 || got.Entry.Schemes[1] != "socks5h" {
		t.Fatalf("mixed must offer http and socks5h (remote DNS), got %v", got.Entry.Schemes)
	}
}

func TestAssessSharedMemberUsesCarrierEndpoint(t *testing.T) {
	member := service("socks", "0.0.0.0", true)
	member.SharedInbound = listener.SharedInboundStandardOwner
	got := Assess(Input{Source: member, Bridges: []Bridge{docker0}, Shared: SharedEntry{Enabled: true, Bind: "0.0.0.0", Port: 7890}})
	if got.State != StateReady || got.Entry.Port != 7890 || got.Entry.Schemes[0] != "socks5h" {
		t.Fatalf("assessment = %+v entry=%+v", got, got.Entry)
	}
}

func TestAssessLoopbackServicePlansCompanionOnBridgeNotWildcard(t *testing.T) {
	got := Assess(Input{Source: service("http", "127.0.0.1", false), Bridges: []Bridge{docker0, compose}, Gateway: "172.18.0.1", AllocatedPort: 17890})
	if got.State != StateNeedsEntry || got.Reason != ReasonLoopbackBind || got.Plan == nil {
		t.Fatalf("assessment = %+v", got)
	}
	plan := got.Plan
	if plan.Placement != PlacementBridge || plan.BindAddress != "172.18.0.1" || plan.Port != 17890 || !plan.NewPort {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.BindAddress == "0.0.0.0" || plan.Kind != "mixed" || plan.Name != "svc · Docker" || plan.ID == "" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestAssessWebSocketServicePrefersReachableSharedCarrier(t *testing.T) {
	got := Assess(Input{
		Source: service("vless", "127.0.0.1", true), Bridges: []Bridge{docker0},
		Shared: SharedEntry{Enabled: true, Bind: "0.0.0.0", Port: 7890}, AllocatedPort: 17890,
	})
	if got.State != StateNeedsEntry || got.Reason != ReasonWebSocket || !got.ClientLanding {
		t.Fatalf("assessment = %+v", got)
	}
	if got.Plan.Placement != PlacementShared || got.Plan.Port != 7890 || got.Plan.NewPort || got.Plan.DialHost != "172.17.0.1" {
		t.Fatalf("plan = %+v", got.Plan)
	}
}

func TestAssessLoopbackSharedCarrierFallsBackToBridge(t *testing.T) {
	member := service("mixed", "127.0.0.1", true)
	member.SharedInbound = listener.SharedInboundStandardOwner
	got := Assess(Input{Source: member, Bridges: []Bridge{docker0}, Shared: SharedEntry{Enabled: true, Bind: "127.0.0.1", Port: 7890}, AllocatedPort: 17891})
	if got.State != StateNeedsEntry || got.Plan.Placement != PlacementBridge || got.Plan.Port != 17891 {
		t.Fatalf("assessment = %+v plan=%+v", got, got.Plan)
	}
}

func TestAssessWithoutBridgeOrCarrierIsBlockedNotDeadEndCopy(t *testing.T) {
	got := Assess(Input{Source: service("http", "127.0.0.1", true)})
	if got.State != StateBlocked || got.Reason != ReasonNoBridge || got.Entry != nil || got.Plan != nil {
		t.Fatalf("assessment = %+v", got)
	}
}

func TestAssessDisabledAndResidential(t *testing.T) {
	disabled := service("mixed", "0.0.0.0", true)
	disabled.Enabled = false
	if got := Assess(Input{Source: disabled, Bridges: []Bridge{docker0}}); got.State != StateBlocked || got.Reason != ReasonServiceDisabled || got.Entry != nil {
		t.Fatalf("disabled = %+v", got)
	}
	if got := Assess(Input{Source: service("vless", "127.0.0.1", true), Residential: true}); got.State != StateClientLanding {
		t.Fatalf("residential = %+v", got)
	}
}

func TestAssessExistingCompanionIsReadyAndRevocable(t *testing.T) {
	entry := service("mixed", "172.17.0.1", true)
	entry.ID, entry.Port = "companion", 17890
	got := Assess(Input{Source: service("vless", "127.0.0.1", true), Bridges: []Bridge{docker0}, Entry: &entry})
	if got.State != StateReady || got.Reason != ReasonCompanion || !got.Revocable || !got.Entry.Companion || got.Entry.ListenerID != "companion" {
		t.Fatalf("assessment = %+v entry=%+v", got, got.Entry)
	}
	entry.BindAddress = "127.0.0.1"
	if got := Assess(Input{Source: service("vless", "127.0.0.1", true), Entry: &entry}); got.State != StateBlocked || got.Reason != ReasonEntryUnreachable || !got.Revocable {
		t.Fatalf("stranded companion = %+v", got)
	}
}

func TestAssessWithheldCompanionIsPendingNotReady(t *testing.T) {
	entry := service("mixed", "172.17.0.1", true)
	entry.ID, entry.Port = "companion", 17890
	got := Assess(Input{Source: service("vless", "127.0.0.1", true), Bridges: []Bridge{docker0}, Entry: &entry, EntryWithheld: true})
	if got.State != StateBlocked || got.Reason != ReasonEntryPending || !got.Revocable || got.Entry != nil {
		t.Fatalf("withheld companion = %+v (must not hand out a URI the data plane does not serve)", got)
	}
}

func TestPlanIDChangesWhenTheSourceChanges(t *testing.T) {
	in := Input{Source: service("http", "127.0.0.1", true), Bridges: []Bridge{docker0}, AllocatedPort: 17890}
	first := Assess(in).Plan.ID
	in.Source.Version++
	if Assess(in).Plan.ID == first {
		t.Fatal("plan id must change when the source service changes")
	}
}

func TestCompanionNameAvoidsCollisionsAndLimit(t *testing.T) {
	taken := map[string]struct{}{"svc · Docker": {}}
	got := Assess(Input{Source: service("http", "127.0.0.1", true), Bridges: []Bridge{docker0}, AllocatedPort: 17890, TakenNames: taken})
	if got.Plan.Name != "svc · Docker 2" {
		t.Fatalf("name = %q", got.Plan.Name)
	}
	long := make([]rune, 200)
	for index := range long {
		long[index] = '名'
	}
	if name := companionName(string(long)); len([]rune(name)) != maxListenerNameRunes {
		t.Fatalf("name runes = %d", len([]rune(name)))
	}
}

func TestSortBridgesPrefersDocker0(t *testing.T) {
	bridges := []Bridge{compose, docker0}
	SortBridges(bridges)
	if bridges[0].Interface != "docker0" {
		t.Fatalf("order = %+v", bridges)
	}
}

func TestAssessSharedMemberIsNotDialableWhenSharedEntryIsOff(t *testing.T) {
	member := service("mixed", "0.0.0.0", true)
	member.SharedInbound = listener.SharedInboundStandardOwner
	got := Assess(Input{Source: member, Bridges: []Bridge{docker0}, Shared: SharedEntry{Enabled: false, Bind: "0.0.0.0", Port: 7890}, AllocatedPort: 17890})
	if got.State != StateNeedsEntry || got.Reason != ReasonSharedDisabled || got.Plan.Placement != PlacementBridge {
		t.Fatalf("assessment = %+v plan=%+v", got, got.Plan)
	}
	entry := service("mixed", "0.0.0.0", true)
	entry.ID, entry.SharedInbound = "companion", listener.SharedInboundStandardOwner
	got = Assess(Input{Source: service("vless", "127.0.0.1", true), Entry: &entry, Bridges: []Bridge{docker0}, Shared: SharedEntry{Enabled: false}})
	if got.State != StateBlocked || got.Reason != ReasonEntryUnreachable || !got.Revocable {
		t.Fatalf("shared companion after switch-off = %+v", got)
	}
}

func TestAssessBridgeCompanionWhoseBridgeVanishedIsUnreachable(t *testing.T) {
	entry := service("mixed", "172.18.0.1", true)
	entry.ID, entry.Port = "companion", 17890
	got := Assess(Input{Source: service("vless", "127.0.0.1", true), Entry: &entry, Bridges: []Bridge{docker0}})
	if got.State != StateBlocked || got.Reason != ReasonEntryUnreachable || !got.Revocable || got.Entry != nil {
		t.Fatalf("assessment = %+v", got)
	}
}

func TestAssessDisabledSourceKeepsCompanionRevocable(t *testing.T) {
	source := service("vless", "127.0.0.1", true)
	source.Enabled = false
	entry := service("mixed", "172.17.0.1", true)
	entry.ID = "companion"
	got := Assess(Input{Source: source, Entry: &entry, Bridges: []Bridge{docker0}})
	if got.State != StateBlocked || got.Reason != ReasonServiceDisabled || !got.Revocable {
		t.Fatalf("assessment = %+v", got)
	}
}

func TestAssessReportsExhaustedPortWindowSeparately(t *testing.T) {
	got := Assess(Input{Source: service("http", "127.0.0.1", true), Bridges: []Bridge{docker0}})
	if got.State != StateBlocked || got.Reason != ReasonNoFreePort {
		t.Fatalf("assessment = %+v", got)
	}
}

func TestAssessFailedBridgeScanIsNotReportedAsNoBridge(t *testing.T) {
	got := Assess(Input{Source: service("http", "127.0.0.1", false), BridgeScanFailed: true})
	if got.State != StateBlocked || got.Reason != ReasonBridgeScanFailed || got.Plan != nil {
		t.Fatalf("assessment = %+v", got)
	}
}

func TestAssessFailedBridgeScanStillPlansWhenBridgesWereRead(t *testing.T) {
	got := Assess(Input{Source: service("http", "127.0.0.1", false), Bridges: []Bridge{docker0}, BridgeScanFailed: true, AllocatedPort: 17890})
	if got.State != StateNeedsEntry || got.Plan == nil || got.Plan.Placement != PlacementBridge {
		t.Fatalf("assessment = %+v", got)
	}
}

func TestAssessFailedBridgeScanDoesNotStrandBridgeCompanion(t *testing.T) {
	entry := listener.Listener{ID: "entry", Name: "svc · Docker", Kind: "mixed", BindAddress: "172.17.0.1", Port: 17890, AuthConfigured: true, SharePath: "/sub/entry", Enabled: true, Version: 1, ProxyGroupID: "group"}
	got := Assess(Input{Source: service("http", "127.0.0.1", false), Entry: &entry, BridgeScanFailed: true})
	if got.State != StateBlocked || got.Reason != ReasonBridgeScanFailed || !got.Revocable {
		t.Fatalf("assessment = %+v", got)
	}
}
