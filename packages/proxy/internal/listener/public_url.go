package listener

import (
	"net"
	"strconv"
	"strings"
)

// PublicPathURL builds a URL for a control-plane path or a subscription path.
// It uses the configured public endpoint, never the Mihomo bind address or
// listener port. Default HTTP(S) ports are omitted so reverse-proxy links can
// be copied directly into clients.
func PublicPathURL(endpoint PublicEndpoint, routePath string) string {
	host := strings.TrimSpace(endpoint.Host)
	routePath = strings.TrimSpace(routePath)
	if host == "" || !strings.HasPrefix(routePath, "/") {
		return ""
	}

	scheme := "http"
	defaultPort := 80
	if endpoint.TLS {
		scheme = "https"
		defaultPort = 443
	}
	port := endpoint.Port
	if port == 0 {
		port = defaultPort
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && strings.Contains(ip.String(), ":") {
		host = "[" + strings.Trim(host, "[]") + "]"
	}
	authority := host
	if port != defaultPort {
		authority = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	}
	return scheme + "://" + authority + routePath
}
