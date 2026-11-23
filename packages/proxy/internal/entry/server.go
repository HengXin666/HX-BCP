package entry

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HengXin666/hx-pxy/internal/store"
)

const (
	handshakeTimeout = 15 * time.Second
	idleTimeout      = 10 * time.Minute
	maximumConns     = 4096
	maximumPerTenant = 1024
	usageChunk       = 256 << 10
)

// Server is the unified entry: HTTP proxy and SOCKS5 on one port, routed by the
// username dialect.
type Server struct {
	tenants *Tenants
	router  *Router
	logger  *slog.Logger
	slots   chan struct{}
	active  atomic.Int64
	served  atomic.Int64
	failed  atomic.Int64

	connMutex sync.Mutex
	byTenant  map[string]map[net.Conn]struct{}
}

// NewServer builds an entry server.
func NewServer(tenants *Tenants, router *Router, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		tenants: tenants, router: router, logger: logger,
		slots: make(chan struct{}, maximumConns), byTenant: map[string]map[net.Conn]struct{}{},
	}
}

// Serve accepts connections until the listener closes.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	var group sync.WaitGroup
	defer group.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			var temporary interface{ Temporary() bool }
			if errors.As(err, &temporary) && temporary.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		select {
		case s.slots <- struct{}{}:
		default:
			_ = conn.Close()
			continue
		}
		group.Add(1)
		go func() {
			defer group.Done()
			defer func() { <-s.slots }()
			s.handle(ctx, conn)
		}()
	}
}

// Stats is a point-in-time count of entry connections.
type Stats struct {
	Active int64 `json:"active"`
	Served int64 `json:"served"`
	Failed int64 `json:"failed"`
}

// Stats returns the connection counters.
func (s *Server) Stats() Stats {
	return Stats{Active: s.active.Load(), Served: s.served.Load(), Failed: s.failed.Load()}
}

func (s *Server) handle(ctx context.Context, client net.Conn) {
	s.active.Add(1)
	defer s.active.Add(-1)
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(handshakeTimeout))
	reader := bufio.NewReader(client)
	first, err := reader.Peek(1)
	if err != nil {
		return
	}
	if first[0] == 0x05 {
		s.handleSOCKS(ctx, client, reader)
		return
	}
	s.handleHTTP(ctx, client, reader)
}

func (s *Server) handleSOCKS(ctx context.Context, client net.Conn, reader *bufio.Reader) {
	var tenant store.EntryTenantRecord
	var selector Selector
	target, err := socksServerHandshake(reader, client, func(user, pass string) error {
		var authErr error
		selector, tenant, authErr = s.authenticate(user, pass)
		return authErr
	})
	if err != nil {
		s.failed.Add(1)
		return
	}
	if !s.track(tenant.ID, client) {
		s.failed.Add(1)
		socksReply(client, socksNotAllowed)
		return
	}
	defer s.untrack(tenant.ID, client)
	upstream, info, err := s.router.Dial(ctx, tenant.ID, selector, target)
	if err != nil {
		s.failed.Add(1)
		s.logger.Info("entry route failed", "component", "entry", "tenant", tenant.Name, "pool", info.Pool, "target", target, "error", err)
		socksReply(client, socksFailureCode(err))
		return
	}
	socksReply(client, socksSucceeded)
	s.relay(client, reader, upstream, tenant, info)
}

func (s *Server) handleHTTP(ctx context.Context, client net.Conn, reader *bufio.Reader) {
	request, err := http.ReadRequest(reader)
	if err != nil {
		s.failed.Add(1)
		return
	}
	user, pass, ok := parseProxyAuthorization(request.Header.Get("Proxy-Authorization"))
	if !ok {
		writeHTTPStatus(client, http.StatusProxyAuthRequired, "proxy authentication required")
		s.failed.Add(1)
		return
	}
	selector, tenant, err := s.authenticate(user, pass)
	if err != nil {
		status := http.StatusProxyAuthRequired
		if errors.Is(err, ErrPoolDenied) || errors.Is(err, ErrQuotaExceeded) || errors.Is(err, ErrInvalidUsername) {
			status = http.StatusForbidden
		}
		writeHTTPStatus(client, status, err.Error())
		s.failed.Add(1)
		return
	}
	target := request.Host
	if request.Method != http.MethodConnect {
		if request.URL == nil || request.URL.Host == "" || !strings.EqualFold(request.URL.Scheme, "http") {
			writeHTTPStatus(client, http.StatusBadRequest, "absolute http:// URL required")
			s.failed.Add(1)
			return
		}
		target = request.URL.Host
	}
	if _, _, splitErr := net.SplitHostPort(target); splitErr != nil {
		if request.Method == http.MethodConnect {
			target = net.JoinHostPort(target, "443")
		} else {
			target = net.JoinHostPort(target, "80")
		}
	}
	if !s.track(tenant.ID, client) {
		s.failed.Add(1)
		writeHTTPStatus(client, http.StatusTooManyRequests, "too many connections for the tenant")
		return
	}
	defer s.untrack(tenant.ID, client)
	upstream, info, err := s.router.Dial(ctx, tenant.ID, selector, target)
	if err != nil {
		s.failed.Add(1)
		s.logger.Info("entry route failed", "component", "entry", "tenant", tenant.Name, "pool", info.Pool, "target", target, "error", err)
		writeHTTPStatus(client, http.StatusBadGateway, err.Error())
		return
	}
	if request.Method == http.MethodConnect {
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			_ = upstream.Close()
			return
		}
		s.relay(client, reader, upstream, tenant, info)
		return
	}
	request.Header.Del("Proxy-Authorization")
	request.Header.Del("Proxy-Connection")
	request.Header.Set("Connection", "close")
	request.Close = true
	request.RequestURI = ""
	if err := request.Write(upstream); err != nil {
		_ = upstream.Close()
		writeHTTPStatus(client, http.StatusBadGateway, err.Error())
		return
	}
	// One request per connection: whatever the client sends next may carry its
	// credentials or name another host, so it is never forwarded.
	s.relay(client, nil, upstream, tenant, info)
}

// authenticate parses the dialect and checks the tenant secret.
func (s *Server) authenticate(user, pass string) (Selector, store.EntryTenantRecord, error) {
	selector, err := Parse(user)
	if err != nil {
		return Selector{}, store.EntryTenantRecord{}, err
	}
	tenant, err := s.tenants.Authenticate(selector, pass)
	return selector, tenant, err
}

// relay copies both ways and books the bytes to the tenant as they flow. A nil
// reader relays the upstream answer only. Residential traffic stops the moment
// the tenant's quota runs out, and a link idle for idleTimeout is closed.
func (s *Server) relay(client net.Conn, reader *bufio.Reader, upstream net.Conn, tenant store.EntryTenantRecord, info RouteInfo) {
	defer upstream.Close()
	s.served.Add(1)
	residential := info.Kind == "gateway"
	book := func(n int) bool {
		if residential {
			s.tenants.AddUsage(tenant.ID, int64(n), int64(n))
			return !s.tenants.ResidentialExceeded(tenant.ID)
		}
		s.tenants.AddUsage(tenant.ID, 0, int64(n))
		return true
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if reader != nil {
			pump(upstream, reader, client, book)
		}
		closeWrite(upstream)
	}()
	pump(client, upstream, upstream, book)
	closeWrite(client)
	if reader == nil {
		_ = upstream.Close()
	}
	<-done
}

// pump copies src to dst, refreshing the idle deadline of deadlineOn before
// each read and booking each chunk; it stops when book says so.
func pump(dst io.Writer, src io.Reader, deadlineOn net.Conn, book func(int) bool) {
	buffer := make([]byte, 32<<10)
	pendingBytes := 0
	defer func() {
		if pendingBytes > 0 {
			book(pendingBytes)
		}
	}()
	for {
		_ = deadlineOn.SetReadDeadline(time.Now().Add(idleTimeout))
		n, err := src.Read(buffer)
		if n > 0 {
			if _, writeErr := dst.Write(buffer[:n]); writeErr != nil {
				return
			}
			pendingBytes += n
			if pendingBytes >= usageChunk {
				chunk := pendingBytes
				pendingBytes = 0
				if !book(chunk) {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// track registers a client connection under its tenant, refusing it past the
// per-tenant limit.
func (s *Server) track(tenantID string, conn net.Conn) bool {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()
	conns := s.byTenant[tenantID]
	if conns == nil {
		conns = map[net.Conn]struct{}{}
		s.byTenant[tenantID] = conns
	}
	if len(conns) >= maximumPerTenant {
		return false
	}
	conns[conn] = struct{}{}
	return true
}

func (s *Server) untrack(tenantID string, conn net.Conn) {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()
	delete(s.byTenant[tenantID], conn)
	if len(s.byTenant[tenantID]) == 0 {
		delete(s.byTenant, tenantID)
	}
}

// CloseTenant closes every open connection of one tenant, so a disabled,
// rotated or deleted tenant loses access at once.
func (s *Server) CloseTenant(tenantID string) {
	s.connMutex.Lock()
	conns := make([]net.Conn, 0, len(s.byTenant[tenantID]))
	for conn := range s.byTenant[tenantID] {
		conns = append(conns, conn)
	}
	s.connMutex.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func closeWrite(conn net.Conn) {
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
		return
	}
	_ = conn.Close()
}

func writeHTTPStatus(writer io.Writer, status int, message string) {
	header := "HTTP/1.1 " + strconv.Itoa(status) + " " + http.StatusText(status) + "\r\n"
	if status == http.StatusProxyAuthRequired {
		header += "Proxy-Authenticate: Basic realm=\"hx-pxy\"\r\n"
	}
	body := message + "\n"
	header += "Content-Type: text/plain; charset=utf-8\r\nConnection: close\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	_, _ = io.WriteString(writer, header+body)
}

func socksFailureCode(err error) byte {
	switch {
	case errors.Is(err, ErrNoRoute):
		return socksHostUnreachable
	case errors.Is(err, ErrPoolDenied), errors.Is(err, ErrQuotaExceeded):
		return socksNotAllowed
	default:
		return socksGeneralFailure
	}
}
