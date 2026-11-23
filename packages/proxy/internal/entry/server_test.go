package entry

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/dataplane/mihomo"
	"github.com/HengXin666/hx-pxy/internal/store"
)

type memoryStore struct {
	mutex    sync.Mutex
	tenants  map[string]store.EntryTenantRecord
	profiles map[string]store.NodeExitProfileRecord
	classes  map[string][]store.SourceClass
}

func newMemoryStore() *memoryStore {
	return &memoryStore{tenants: map[string]store.EntryTenantRecord{}, profiles: map[string]store.NodeExitProfileRecord{}, classes: map[string][]store.SourceClass{}}
}

func (m *memoryStore) CreateEntryTenant(_ context.Context, record store.EntryTenantRecord) (store.EntryTenantRecord, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	for _, existing := range m.tenants {
		if existing.Name == record.Name {
			return store.EntryTenantRecord{}, store.ErrConflict
		}
	}
	record.Version = 1
	m.tenants[record.ID] = record
	return record, nil
}

func (m *memoryStore) ListEntryTenants(context.Context) ([]store.EntryTenantRecord, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	items := make([]store.EntryTenantRecord, 0, len(m.tenants))
	for _, record := range m.tenants {
		items = append(items, record)
	}
	return items, nil
}

func (m *memoryStore) GetEntryTenant(_ context.Context, id string) (store.EntryTenantRecord, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	record, ok := m.tenants[id]
	if !ok {
		return store.EntryTenantRecord{}, store.ErrNotFound
	}
	return record, nil
}

func (m *memoryStore) UpdateEntryTenant(_ context.Context, record store.EntryTenantRecord) (store.EntryTenantRecord, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	current := m.tenants[record.ID]
	if current.Version != record.Version {
		return store.EntryTenantRecord{}, store.ErrConflict
	}
	if record.SecretSealed == nil {
		record.SecretSealed = current.SecretSealed
	}
	record.Version++
	m.tenants[record.ID] = record
	return record, nil
}

func (m *memoryStore) DeleteEntryTenant(_ context.Context, id string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.tenants, id)
	return nil
}

func (m *memoryStore) AddEntryTenantUsage(_ context.Context, id string, resi, total int64) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	record := m.tenants[id]
	record.ResiUsedBytes += resi
	record.TotalBytes += total
	m.tenants[id] = record
	return nil
}

func (m *memoryStore) ResetEntryTenantUsage(_ context.Context, id string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	record := m.tenants[id]
	record.ResiUsedBytes = 0
	m.tenants[id] = record
	return nil
}

func (m *memoryStore) UpsertNodeExitProfile(_ context.Context, record store.NodeExitProfileRecord) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.profiles[record.NodeID] = record
	return nil
}

func (m *memoryStore) ListNodeExitProfiles(context.Context) ([]store.NodeExitProfileRecord, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	items := make([]store.NodeExitProfileRecord, 0, len(m.profiles))
	for _, record := range m.profiles {
		items = append(items, record)
	}
	return items, nil
}

func (m *memoryStore) ListNodeSourceClasses(context.Context) (map[string][]store.SourceClass, error) {
	return m.classes, nil
}

type plainCipher struct{}

func (plainCipher) Seal(plaintext, _ []byte) ([]byte, error) {
	return append([]byte("x"), plaintext...), nil
}
func (plainCipher) Open(envelope, _ []byte) ([]byte, error) {
	if len(envelope) == 0 {
		return nil, errors.New("empty envelope")
	}
	return envelope[1:], nil
}

// echoServer answers every line it reads with the prefix "echo:".
func echoServer(t *testing.T) string {
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
				line, _ := reader.ReadString('\n')
				_, _ = io.WriteString(conn, "echo:"+line)
			}()
		}
	}()
	return listener.Addr().String()
}

// fakeHub is a SOCKS5 server that records which hub user each tunnel used.
func fakeHub(t *testing.T, secret string, users chan<- string) string {
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
				var user string
				target, err := socksServerHandshake(reader, conn, func(u, p string) error {
					if p != secret {
						return errSocksAuth
					}
					user = u
					return nil
				})
				if err != nil {
					return
				}
				users <- user
				upstream, err := net.Dial("tcp", target)
				if err != nil {
					socksReply(conn, socksHostUnreachable)
					return
				}
				defer upstream.Close()
				socksReply(conn, socksSucceeded)
				go func() { _, _ = io.Copy(upstream, reader) }()
				_, _ = io.Copy(conn, upstream)
			}()
		}
	}()
	return listener.Addr().String()
}

type harness struct {
	service *Service
	address string
	secret  string
	users   chan string
	store   *memoryStore
}

func newHarness(t *testing.T, pools []string) *harness {
	t.Helper()
	memory := newMemoryStore()
	memory.classes = map[string][]store.SourceClass{"n1": {store.SourceClassSubscription}, "n2": {store.SourceClassResidential}}
	service, err := NewService(context.Background(), Config{}, memory, plainCipher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := service.Tenants().Create(context.Background(), TenantRequest{Name: "acme", Pools: pools})
	if err != nil {
		t.Fatal(err)
	}
	users := make(chan string, 16)
	hub := fakeHub(t, "hub-secret-0123456789", users)
	service.OnCatalog(mihomo.EntryCatalog{
		HubAddress: hub, HubSecret: "hub-secret-0123456789",
		Nodes: []mihomo.EntryNode{
			{ID: "n1", HubUser: "hxe-n-1", DisplayName: "US 01", Protocol: "ss", Server: "a", Port: 1},
			{ID: "n2", HubUser: "hxe-n-2", DisplayName: "JP 01", Protocol: "ss", Server: "b", Port: 2},
		},
		Groups: []mihomo.EntryGroup{{ID: "g1", Name: "fast", HubUser: "hxe-g-1"}},
	})
	service.router.allowLocal = true
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = listener.Close() })
	go func() { _ = service.server.Serve(ctx, listener) }()
	return &harness{service: service, address: listener.Addr().String(), secret: tenant.Secret, users: users, store: memory}
}

func socksRoundTrip(t *testing.T, address, user, pass, target string) (string, error) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := socksClientConnect(conn, user, pass, target); err != nil {
		return "", err
	}
	_, _ = io.WriteString(conn, "hi\n")
	line, err := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimSpace(line), err
}

func TestEntryDirectOverSOCKS(t *testing.T) {
	h := newHarness(t, []string{"direct"})
	echo := echoServer(t)
	got, err := socksRoundTrip(t, h.address, "acme-pool-direct", h.secret, echo)
	if err != nil || got != "echo:hi" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	if _, err := socksRoundTrip(t, h.address, "acme-pool-direct", "wrong", echo); err == nil {
		t.Fatal("wrong secret was accepted")
	}
	if _, err := socksRoundTrip(t, h.address, "acme-pool-sub", h.secret, echo); err == nil {
		t.Fatal("a pool outside the tenant was accepted")
	}
}

func TestEntryStickySlotUsesOneNode(t *testing.T) {
	h := newHarness(t, nil)
	echo := echoServer(t)
	first := ""
	for attempt := 0; attempt < 4; attempt++ {
		got, err := socksRoundTrip(t, h.address, "acme-pool-sub-session-s1", h.secret, echo)
		if err != nil || got != "echo:hi" {
			t.Fatalf("round trip = %q, %v", got, err)
		}
		user := <-h.users
		if first == "" {
			first = user
		} else if user != first {
			t.Fatalf("sticky slot moved from %s to %s", first, user)
		}
	}
	got, err := socksRoundTrip(t, h.address, "acme-pool-subresi", h.secret, echo)
	if err != nil || got != "echo:hi" {
		t.Fatalf("subresi round trip = %q, %v", got, err)
	}
	if user := <-h.users; user != "hxe-n-2" {
		t.Fatalf("subresi went through %s, want the declared residential node", user)
	}
	if _, err := socksRoundTrip(t, h.address, "acme-pool-sub-cc-de", h.secret, echo); err == nil {
		t.Fatal("a country with no node was routed")
	}
}

func TestEntryGroupOverHTTPConnect(t *testing.T) {
	h := newHarness(t, []string{"group:fast"})
	echo := echoServer(t)
	conn, err := net.Dial("tcp", h.address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	tunnel, err := httpClientConnect(conn, "acme-group-fast", h.secret, echo)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(tunnel, "hi\n")
	line, _ := bufio.NewReader(tunnel).ReadString('\n')
	if strings.TrimSpace(line) != "echo:hi" {
		t.Fatalf("tunnel answered %q", line)
	}
	if user := <-h.users; user != "hxe-g-1" {
		t.Fatalf("group went through %s", user)
	}
}

func TestEntryHTTPAuthRequired(t *testing.T) {
	h := newHarness(t, nil)
	conn, err := net.Dial("tcp", h.address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d", response.StatusCode)
	}
	conn2, _ := net.Dial("tcp", h.address)
	defer conn2.Close()
	auth := base64.StdEncoding.EncodeToString([]byte("acme-pool-vip:" + h.secret))
	_, _ = io.WriteString(conn2, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Authorization: Basic "+auth+"\r\n\r\n")
	response, err = http.ReadResponse(bufio.NewReader(conn2), nil)
	if err != nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("bad dialect status = %v, %v", response, err)
	}
}

func TestEntryUsageFlush(t *testing.T) {
	h := newHarness(t, []string{"direct"})
	echo := echoServer(t)
	if _, err := socksRoundTrip(t, h.address, "acme-pool-direct", h.secret, echo); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.service.Tenants().Flush(context.Background())
		items, _ := h.store.ListEntryTenants(context.Background())
		if len(items) == 1 && items[0].TotalBytes > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("usage never reached the store")
}

func TestRenderClashKeepsNamesStable(t *testing.T) {
	h := newHarness(t, nil)
	tenants, _ := h.service.Tenants().List(context.Background())
	token, err := h.service.ShareToken(tenants[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := h.service.Subscription(tenants[0].ID, token, ClashOptions{Host: "proxy.example", Port: 7890, Slots: 2})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := h.service.Subscription(tenants[0].ID, token, ClashOptions{Host: "proxy.example", Port: 7890, Slots: 2})
	if string(first) != string(second) {
		t.Fatal("two renders of the same profile differ")
	}
	for _, want := range []string{"SUB-US-01", "SUB-JP-02", "acme-pool-sub-cc-us-session-sub_us_1-ttl-1440"} {
		if !strings.Contains(string(first), want) {
			t.Fatalf("profile is missing %q:\n%s", want, first)
		}
	}
	if _, err := h.service.Subscription(tenants[0].ID, "bad", ClashOptions{Host: "h", Port: 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad token error = %v", err)
	}
}

func TestEntryDirectRefusesLocalTargets(t *testing.T) {
	h := newHarness(t, []string{"direct"})
	h.service.router.allowLocal = false
	echo := echoServer(t)
	if _, err := socksRoundTrip(t, h.address, "acme-pool-direct", h.secret, echo); err == nil {
		t.Fatal("the direct pool reached a loopback service")
	}
}

func TestEntryGroupNeedsGrant(t *testing.T) {
	h := newHarness(t, nil)
	echo := echoServer(t)
	if _, err := socksRoundTrip(t, h.address, "acme-group-fast", h.secret, echo); err == nil {
		t.Fatal("a tenant without a group grant used a proxy group")
	}
	if _, err := socksRoundTrip(t, h.address, "acme-pool-sub-group-fast", h.secret, echo); err == nil {
		t.Fatal("pool-sub with a group bypassed the group grant")
	}
}

func TestEntryDisablingTenantClosesItsConnections(t *testing.T) {
	h := newHarness(t, []string{"direct"})
	echo := echoServer(t)
	conn, err := net.DialTimeout("tcp", h.address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := socksClientConnect(conn, "acme-pool-direct", h.secret, echo); err != nil {
		t.Fatal(err)
	}
	tenants, _ := h.store.ListEntryTenants(context.Background())
	disabled := false
	if _, err := h.service.Tenants().Update(context.Background(), tenants[0].ID, TenantRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(conn); err != nil && !errors.Is(err, io.EOF) {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("the connection of a disabled tenant stayed open")
		}
	}
}

func TestEntryPlainHTTPForwardsOneRequest(t *testing.T) {
	h := newHarness(t, []string{"direct"})
	seen := make(chan string, 4)
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	go func() {
		conn, err := target.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			request, err := http.ReadRequest(reader)
			if err != nil {
				return
			}
			seen <- request.Header.Get("Proxy-Authorization")
			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
		}
	}()
	conn, err := net.DialTimeout("tcp", h.address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("acme-pool-direct:"+h.secret))
	address := target.Addr().String()
	request := "GET http://" + address + "/a HTTP/1.1\r\nHost: " + address + "\r\nProxy-Authorization: " + auth + "\r\n\r\n"
	_, _ = io.WriteString(conn, request+request)
	_, _ = io.ReadAll(conn)
	close(seen)
	count := 0
	for header := range seen {
		count++
		if header != "" {
			t.Fatal("the proxy credentials reached the target")
		}
	}
	if count != 1 {
		t.Fatalf("target saw %d requests, want 1", count)
	}
}
