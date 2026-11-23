package entry

import (
	"bufio"
	"context"
	"github.com/HengXin666/hx-pxy/internal/slot"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/residential"
	"github.com/HengXin666/hx-pxy/internal/store"
)

func TestBuildSnapshotClassifiesPools(t *testing.T) {
	t.Parallel()
	now := time.Now()
	catalog := mihomo.EntryCatalog{Nodes: []mihomo.EntryNode{
		{ID: "plain", DisplayName: "HK 01", LifecycleState: "healthy"},
		{ID: "measured", DisplayName: "node", LifecycleState: "healthy"},
		{ID: "stale", DisplayName: "node", LifecycleState: "healthy"},
		{ID: "channel", DisplayName: "residential session"},
		{ID: "dead", DisplayName: "US", LifecycleState: "quarantined"},
	}}
	classes := map[string][]store.SourceClass{
		"plain": {store.SourceClassPool}, "measured": {store.SourceClassSubscription},
		"stale": {store.SourceClassSubscription}, "dead": {store.SourceClassPool},
	}
	profiles := []store.NodeExitProfileRecord{
		{NodeID: "measured", Success: true, Residential: true, CountryCode: "US", City: "New York", ASN: 7922, SampledAt: now},
		{NodeID: "stale", Success: true, Residential: true, CountryCode: "DE", SampledAt: now.Add(-ProfileTTL - time.Hour)},
	}
	snapshot := BuildSnapshot(catalog, classes, profiles, now)
	if _, ok := snapshot.Candidate("channel"); ok {
		t.Fatal("a node no subscription holds entered the snapshot")
	}
	plain, _ := snapshot.Candidate("plain")
	if plain.Country != "HK" || plain.Residential {
		t.Fatalf("plain = %+v", plain)
	}
	measured, _ := snapshot.Candidate("measured")
	if !measured.Residential || measured.City != "new_york" || measured.ASN != 7922 {
		t.Fatalf("measured = %+v", measured)
	}
	stale, _ := snapshot.Candidate("stale")
	if stale.Residential || stale.Country == "DE" {
		t.Fatalf("a stale profile steered routing: %+v", stale)
	}
	dead, _ := snapshot.Candidate("dead")
	if dead.Alive {
		t.Fatal("a quarantined node is alive")
	}
}

func TestPickPrefersSuccessorEndpoint(t *testing.T) {
	t.Parallel()
	catalog := mihomo.EntryCatalog{Nodes: []mihomo.EntryNode{
		{ID: "old", Protocol: "ss", Server: "1.1.1.1", Port: 8388, LifecycleState: "disabled"},
		{ID: "other", Protocol: "ss", Server: "2.2.2.2", Port: 8388},
		{ID: "renamed", Protocol: "ss", Server: "1.1.1.1", Port: 8388},
	}}
	classes := map[string][]store.SourceClass{"old": nil, "other": nil, "renamed": nil}
	snapshot := BuildSnapshot(catalog, classes, nil, time.Now())
	for rotation := uint64(0); rotation < 4; rotation++ {
		node, err := snapshotCatalog{snapshot: snapshot, rotation: rotation}.Pick(slotRequest("t", Selector{Pool: PoolSubscription}), slot.Binding{NodeID: "old"})
		if err != nil || node.ID != "renamed" {
			t.Fatalf("Pick = %+v, %v", node, err)
		}
	}
}

func TestPickFindsSuccessorOfVanishedNode(t *testing.T) {
	t.Parallel()
	catalog := mihomo.EntryCatalog{Nodes: []mihomo.EntryNode{
		{ID: "other", Protocol: "ss", Server: "2.2.2.2", Port: 8388},
		{ID: "renamed", Protocol: "ss", Server: "1.1.1.1", Port: 8388},
	}}
	classes := map[string][]store.SourceClass{"other": nil, "renamed": nil}
	snapshot := BuildSnapshot(catalog, classes, nil, time.Now())
	identity := snapshot.Nodes[snapshot.byID["renamed"]].Identity
	for rotation := uint64(0); rotation < 4; rotation++ {
		lost := slot.Binding{NodeID: "gone", Identity: identity}
		node, err := snapshotCatalog{snapshot: snapshot, rotation: rotation}.Pick(slotRequest("t", Selector{Pool: PoolSubscription}), lost)
		if err != nil || node.ID != "renamed" {
			t.Fatalf("Pick = %+v, %v", node, err)
		}
	}
}

func TestProfileFromAnswer(t *testing.T) {
	t.Parallel()
	record := profileFromAnswer(store.NodeExitProfileRecord{}, exitAnswer{CountryCode: "us", AS: "AS7922 Comcast Cable", Query: "1.2.3.4"})
	if !record.Residential || record.ASN != 7922 || record.ASName != "Comcast Cable" || record.CountryCode != "US" {
		t.Fatalf("record = %+v", record)
	}
	hosting := profileFromAnswer(store.NodeExitProfileRecord{}, exitAnswer{Hosting: true})
	if hosting.Residential {
		t.Fatal("a hosting exit was marked residential")
	}
}

type staticGateways []residential.EntryGateway

func (g staticGateways) EntryGateways(context.Context) ([]residential.EntryGateway, error) {
	return g, nil
}

// fakeGateway accepts HTTP CONNECT and reports each rendered username.
func fakeGateway(t *testing.T, users chan<- string) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				request, err := http.ReadRequest(reader)
				if err != nil {
					return
				}
				user, _, _ := parseProxyAuthorization(request.Header.Get("Proxy-Authorization"))
				users <- user
				upstream, err := net.Dial("tcp", request.Host)
				if err != nil {
					return
				}
				defer upstream.Close()
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")
				go func() { _, _ = io.Copy(upstream, reader) }()
				_, _ = io.Copy(conn, upstream)
			}()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return address.IP.String(), address.Port
}

func TestResidentialSlotKeepsSessionUntilReleased(t *testing.T) {
	h := newHarness(t, []string{"resi"})
	users := make(chan string, 8)
	host, port := fakeGateway(t, users)
	h.service.router.gateways = staticGateways{{
		ID: "p1", Name: "vendor", Protocol: "http", Host: host, Port: port, Username: "acct",
		Template: "{user}-cc-{country|lower}-sid-{session}-t-{ttl|min}", RotationMode: residential.RotationSessionTemplate, TTLSeconds: 600,
	}}
	echo := echoServer(t)
	for attempt := 0; attempt < 2; attempt++ {
		if got, err := socksRoundTrip(t, h.address, "acme-pool-resi-cc-JP-session-a-ttl-5", h.secret, echo); err != nil || got != "echo:hi" {
			t.Fatalf("round trip = %q, %v", got, err)
		}
	}
	first, second := <-users, <-users
	if first != second || !strings.HasPrefix(first, "acct-cc-jp-sid-") || !strings.HasSuffix(first, "-t-5") {
		t.Fatalf("usernames = %q, %q", first, second)
	}
	tenants, _ := h.store.ListEntryTenants(context.Background())
	h.service.ReleaseSlot(tenants[0].ID, "a")
	if _, err := socksRoundTrip(t, h.address, "acme-pool-resi-cc-JP-session-a-ttl-5", h.secret, echo); err != nil {
		t.Fatal(err)
	}
	if third := <-users; third == first {
		t.Fatal("a released slot kept its vendor session")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.service.Tenants().Flush(context.Background())
		items, _ := h.store.ListEntryTenants(context.Background())
		if items[0].ResiUsedBytes > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("residential bytes were not booked")
}
