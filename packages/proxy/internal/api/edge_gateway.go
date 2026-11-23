package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HengXin666/hx-pxy/internal/listener"
)

const maxEdgeRelayConnections = 1024

var edgeRelayTransport = &http.Transport{
	Proxy:                 nil,
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     false,
	MaxIdleConns:          128,
	MaxIdleConnsPerHost:   64,
	IdleConnTimeout:       90 * time.Second,
	ResponseHeaderTimeout: 10 * time.Second,
}

// handleEdgeRelay is deliberately narrower than a general reverse proxy. It
// only accepts WebSocket upgrades under the reserved listener namespace and
// only dials loopback Mihomo listeners returned by ListenerService.
func (s *Server) handleEdgeRelay(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeEdgeStatus(writer, http.StatusMethodNotAllowed)
		return
	}
	if !isWebSocketUpgrade(request) {
		writeEdgeStatus(writer, http.StatusUpgradeRequired)
		return
	}
	if request.URL.RawQuery != "" {
		writeEdgeStatus(writer, http.StatusNotFound)
		return
	}

	if s.edgeSlots == nil {
		s.edgeSlots = make(chan struct{}, maxEdgeRelayConnections)
	}
	select {
	case s.edgeSlots <- struct{}{}:
		defer func() { <-s.edgeSlots }()
	default:
		writeEdgeStatus(writer, http.StatusServiceUnavailable)
		return
	}

	route, err := s.resolveEdgeRoute(request)
	if err != nil {
		s.logger.WarnContext(request.Context(), "edge relay route lookup failed", "error", err)
		writeEdgeStatus(writer, http.StatusServiceUnavailable)
		return
	}
	if route == nil {
		writeEdgeStatus(writer, http.StatusNotFound)
		return
	}

	target := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(route.BindAddress, strconv.Itoa(route.Port)),
	}
	proxy := &httputil.ReverseProxy{
		Director: func(outbound *http.Request) {
			outbound.URL.Scheme = target.Scheme
			outbound.URL.Host = target.Host
			// The route has already been validated against the normalized path.
			// Replacing it also prevents encoded path variants from reaching
			// Mihomo as a different route.
			outbound.URL.Path = route.Path
			outbound.URL.RawPath = ""
		},
		Transport: edgeRelayTransport,
		ErrorHandler: func(errorWriter http.ResponseWriter, errorRequest *http.Request, proxyErr error) {
			s.logger.WarnContext(
				errorRequest.Context(),
				"edge relay connection failed",
				"listener_id", route.ID,
				"error", proxyErr,
			)
			writeEdgeStatus(errorWriter, http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(writer, request)
}

type edgeRoute struct {
	ID          string
	BindAddress string
	Port        int
	Path        string
}

func (s *Server) resolveEdgeRoute(request *http.Request) (*edgeRoute, error) {
	if s.listeners == nil {
		return nil, nil
	}
	items, err := s.listeners.List(request.Context())
	if err != nil {
		return nil, err
	}
	requestHost := normalizeEdgeHost(request.Host)
	var match *edgeRoute
	for _, item := range items {
		if !item.Enabled || !isEdgeListenerKind(item.Kind) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Transport.Type), "ws") {
			continue
		}
		path, pathErr := listener.NormalizeWebSocketPath(item.Transport.WSPath)
		if pathErr != nil || path != request.URL.Path {
			continue
		}
		if requestHost == "" || !strings.EqualFold(requestHost, normalizeEdgeHost(item.PublicEndpoint.Host)) {
			continue
		}
		bindIP := net.ParseIP(item.BindAddress)
		if bindIP == nil || !bindIP.IsLoopback() || item.Port < 1 || item.Port > 65535 {
			return nil, fmt.Errorf("listener %q has an unsafe edge relay target", item.ID)
		}
		if match != nil {
			return nil, fmt.Errorf("public WebSocket route %q is ambiguous", request.URL.Path)
		}
		match = &edgeRoute{
			ID:          item.ID,
			BindAddress: bindIP.String(),
			Port:        item.Port,
			Path:        path,
		}
	}
	return match, nil
}

func isEdgeListenerKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "vless", "vmess", "trojan":
		return true
	default:
		return false
	}
}

func isWebSocketUpgrade(request *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket") &&
		headerContainsToken(request.Header.Get("Connection"), "upgrade")
}

func headerContainsToken(value, wanted string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), wanted) {
			return true
		}
	}
	return false
}

func normalizeEdgeHost(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.TrimSuffix(strings.ToLower(value), ".")
}

func writeEdgeStatus(writer http.ResponseWriter, status int) {
	writer.Header().Set("Cache-Control", "no-store")
	http.Error(writer, http.StatusText(status), status)
}
