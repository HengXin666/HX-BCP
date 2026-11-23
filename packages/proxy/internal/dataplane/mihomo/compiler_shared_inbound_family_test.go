package mihomo

import (
	"context"
	"testing"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/store"
)

// TestSharedInboundCarriesHTTPAndSOCKSMembers is the compiler-side regression
// test for the proxy service that disappeared from the data plane.
//
// Mihomo's Mixed listener multiplexes HTTP proxy and SOCKS5 on one socket, which
// is exactly what the standard entry point (7890) is documented to offer. A
// service created with kind "http" or "socks" is therefore carried by that Mixed
// listener. The compiler used to require record.Kind == "mixed", so those
// members were skipped: their rows still claimed a shared entry point, but the
// compiled document had no listener and no IN-USER rule for them, and every
// client using the published URL was rejected.
func TestSharedInboundCarriesHTTPAndSOCKSMembers(t *testing.T) {
	t.Parallel()
	port := reservePort(t)
	aggregate := aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, port)
	aggregate.BindAddress = "0.0.0.0"

	repository := sharedInboundRepository{
		groups: []store.ProxyGroupRecord{
			testGroup("group-http"),
			testGroup("group-socks"),
			testGroup("group-mixed"),
		},
		listeners: []store.ListenerRecord{
			aggregate,
			memberListener(t, "member-http", "service-http", "http", "group-http", "svc-group-http", "pass-http", listener.SharedInboundStandardOwner, port),
			memberListener(t, "member-socks", "service-socks", "socks", "group-socks", "svc-group-socks", "pass-socks", listener.SharedInboundStandardOwner, port),
			memberListener(t, "member-mixed", "service-mixed", "mixed", "group-mixed", "svc-group-mixed", "pass-mixed", listener.SharedInboundStandardOwner, port),
		},
	}
	document := compileShared(t, repository)

	listeners := listenersOf(t, document)
	if len(listeners) != 1 {
		t.Fatalf("compiled listeners = %d, want one Mixed entry point: %#v", len(listeners), listeners)
	}
	carrier := listeners[0]
	if carrier["type"] != "mixed" {
		t.Fatalf("carrier type = %v, want mixed", carrier["type"])
	}
	if carrier["listen"] != "0.0.0.0" || carrier["port"] != port {
		t.Fatalf("carrier endpoint = %v:%v, want 0.0.0.0:%d", carrier["listen"], carrier["port"], port)
	}

	// Every member must be routable by its own username, whichever protocol it
	// was created with.
	users := usersOf(t, carrier)
	for _, username := range []string{"svc-group-http", "svc-group-socks", "svc-group-mixed"} {
		if _, ok := users[username]; !ok {
			t.Fatalf("username %q is missing from the aggregate listener: %#v", username, users)
		}
	}
	name := SharedListenerConfigName(listener.SharedInboundStandardOwner, "mixed")
	rules := rulesOf(t, document)
	for username, group := range map[string]string{
		"svc-group-http":  "group-http",
		"svc-group-socks": "group-socks",
		"svc-group-mixed": "group-mixed",
	} {
		expected := "AND,((IN-NAME," + name + "),(IN-USER," + username + "))," + group
		if indexOfRule(rules, expected) < 0 {
			t.Fatalf("missing member rule %q in %#v", expected, rules)
		}
	}
}

// TestSharedInboundUsesTheFamilyEndpointNotTheMemberRow guards the case where a
// member row still carries its pre-migration dedicated port. Members share the
// carrier, so the published endpoint must come from the family configuration.
func TestSharedInboundUsesTheFamilyEndpointNotTheMemberRow(t *testing.T) {
	t.Parallel()
	port := reservePort(t)
	aggregate := aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, port)
	aggregate.BindAddress = "0.0.0.0"
	member := memberListener(t, "member-http", "service-http", "http", "group-a", "svc-group-a", "pass-a", listener.SharedInboundStandardOwner, port)
	// A stale dedicated port on the member row must not become the entry point.
	member.Port = 19999
	member.BindAddress = "127.0.0.1"

	document := compileShared(t, sharedInboundRepository{
		groups:    []store.ProxyGroupRecord{testGroup("group-a")},
		listeners: []store.ListenerRecord{aggregate, member},
	})
	listeners := listenersOf(t, document)
	if len(listeners) != 1 {
		t.Fatalf("compiled listeners = %d, want 1", len(listeners))
	}
	if listeners[0]["port"] != port || listeners[0]["listen"] != "0.0.0.0" {
		t.Fatalf("endpoint = %v:%v, want the family endpoint 0.0.0.0:%d", listeners[0]["listen"], listeners[0]["port"], port)
	}
}

// TestSharedInboundRejectsDuplicateUsernamesAcrossProtocols pins the invariant
// the membership model rests on now that one listener carries every standard
// protocol: a username selects exactly one group, so two services of *any*
// protocol behind the same entry point cannot share one.
func TestSharedInboundRejectsDuplicateUsernamesAcrossProtocols(t *testing.T) {
	t.Parallel()
	port := reservePort(t)
	aggregate := aggregateListener("agg-standard", "mixed", listener.SharedInboundStandardOwner, port)
	compiler, err := NewCompiler(sharedInboundRepository{
		groups: []store.ProxyGroupRecord{testGroup("group-a"), testGroup("group-b")},
		listeners: []store.ListenerRecord{
			aggregate,
			memberListener(t, "member-http", "service-http", "http", "group-a", "same-user", "pass-a", listener.SharedInboundStandardOwner, port),
			memberListener(t, "member-mixed", "service-mixed", "mixed", "group-b", "same-user", "pass-b", listener.SharedInboundStandardOwner, port),
		},
	}, plaintextCipher{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Compile(context.Background()); err == nil {
		t.Fatal("Compile() accepted two standard members with the same username")
	}
}

func usersOf(t *testing.T, carrier map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := carrier["users"].([]any)
	if !ok {
		t.Fatalf("carrier has no users: %#v", carrier)
	}
	result := make(map[string]map[string]any, len(raw))
	for _, item := range raw {
		user, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected user entry %#v", item)
		}
		username, _ := user["username"].(string)
		result[username] = user
	}
	return result
}
