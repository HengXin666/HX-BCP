package nodeparse

import (
	"net"
	"strconv"
	"strings"
)

// A proxy list is the fourth way a consumer hands this control plane a set of
// endpoints: a flat, one-endpoint-per-line document exported by a proxy vendor,
// a checker, or a scraper — as opposed to a subscription document, which is a
// rendered client config.
//
// It has no container header, so it cannot be declared; it is recognized by its
// per-line shape. Two shapes exist in practice, and they are handled in two
// different places on purpose:
//
//  1. Decorated share URIs — the endpoint is a share URI, but the line carries
//     an export annotation. A "|" column can appear *after* the authority
//     ("OK|http|http://u:p@h:p|1.2.3.4") and a comment fragment can carry one
//     ("socks5://184.178.172.13:4145#住宅-风险81% | AS22773 - Cox"), where the
//     annotation lands inside the fragment and makes the percent sign an invalid
//     escape, so url.Parse rejects the whole line. Such a list used to import
//     zero nodes. This shape keeps the ordinary "uri-list" format and is
//     repaired inside the URI line parser, so a decorated list still flows
//     through the same dedup / refresh / export path as any subscription.
//  2. Bare endpoints — "host:port", "host:port:user:password". There is no
//     scheme to recognize, so the whole document is the unit of detection rather
//     than one line, and the format is reported as "proxy-list".
//
// The decoration stripping is deliberately conservative: a share URI that parses
// on its own is never touched, so "#香港节点" survives as a node name. Only a
// line the URI parser already rejected is retried in a weakened form.
//
// See .agents/notes/implemented/feature/2026-09-26-proxy-list-pool-subscription-source.md

// ProxyListFormat is the DetectedFormat reported for a bare endpoint list.
const ProxyListFormat = "proxy-list"

// parseProxyListShareURI retries a share-URI line that the ordinary parser
// rejected. It reports the original parse error when nothing works, so the
// caller keeps reporting one failure per unusable line instead of a generic
// "bad line".
func parseProxyListShareURI(line string) (Node, error) {
	node, originalErr := parseURI(line)
	if originalErr == nil {
		return node, nil
	}
	for _, candidate := range proxyListColumns(line) {
		if !strings.Contains(candidate, "://") {
			continue
		}
		if node, err := parseURI(candidate); err == nil {
			return node, nil
		}
	}
	return Node{}, originalErr
}

// proxyListColumns expands one line into the candidates worth attempting, in an
// order that preserves the most information: every "|" column as written first,
// then the same columns with their "#" fragment and "|" tail removed.
//
// The as-written pass comes first on purpose. A line whose fragment is a plain
// node name ("socks5://h:p#HK-01") must keep that name, and it does: the first
// pass succeeds and the second never runs. The weakened pass exists only for
// lines the first pass could not parse at all.
func proxyListColumns(line string) []string {
	columns := strings.Split(line, "|")
	candidates := make([]string, 0, len(columns)*2)
	for _, column := range columns {
		if candidate := strings.TrimSpace(column); candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	for _, column := range columns {
		trimmed := strings.TrimSpace(column)
		if index := strings.IndexByte(trimmed, '#'); index >= 0 {
			trimmed = strings.TrimSpace(trimmed[:index])
		}
		if trimmed != "" && !containsString(candidates, trimmed) {
			candidates = append(candidates, trimmed)
		}
	}
	return candidates
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// parseProxyList imports a document whose lines are bare endpoints. It runs only
// after every other container format and base64 have been ruled out, and it
// returns zero nodes when the document does not look like a list at all, so it
// can never claim a document that belongs to another format.
func parseProxyList(text string) Result {
	result := Result{DetectedFormat: ProxyListFormat, Nodes: make([]Node, 0, 16)}
	for index, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		node, ok := parseProxyListEndpointLine(line)
		if !ok {
			result.Failures = append(result.Failures, Failure{Index: index, Reason: "line is not a proxy endpoint"})
			continue
		}
		result.Nodes = append(result.Nodes, node)
	}
	return result
}

// parseProxyListEndpointLine resolves one line of a document that also contains
// bare endpoints. A line may still carry a scheme: a vendor that mixes schemes in
// one file is telling us the protocol, and ignoring that would silently downgrade
// every SOCKS5 endpoint in the file to HTTP.
func parseProxyListEndpointLine(line string) (Node, bool) {
	for _, candidate := range proxyListColumns(line) {
		if strings.Contains(candidate, "://") {
			if node, err := parseURI(candidate); err == nil {
				return node, true
			}
			continue
		}
		if node, ok := parseProxyListEndpoint(candidate); ok {
			return node, true
		}
	}
	return Node{}, false
}

// parseProxyListEndpoint imports "host:port" and "host:port:user:password".
//
// A bare endpoint carries no protocol. It defaults to HTTP because that is what
// "host:port" means to every plain HTTP client (`curl -x host:port`); guessing
// SOCKS from the port number would be wrong often enough to be worse than a
// documented default. A list whose endpoints are not HTTP carries the scheme, and
// those lines take the URI path above.
func parseProxyListEndpoint(value string) (Node, bool) {
	if value == "" || strings.ContainsAny(value, " \t/@") {
		return Node{}, false
	}
	if server, portText, err := net.SplitHostPort(value); err == nil {
		port, parseErr := strconv.Atoi(portText)
		if parseErr != nil {
			return Node{}, false
		}
		return newProxyListNode("http", server, port, "", "")
	}
	// "host:port:user:password" — the layout several vendors emit for
	// authenticated endpoints. It is restricted to an IP literal host because
	// four colon-separated fields are otherwise ambiguous with IPv6.
	parts := strings.Split(value, ":")
	if len(parts) != 4 || net.ParseIP(parts[0]) == nil || parts[2] == "" || parts[3] == "" {
		return Node{}, false
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return Node{}, false
	}
	return newProxyListNode("http", parts[0], port, parts[2], parts[3])
}

func newProxyListNode(protocol, server string, port int, username, password string) (Node, bool) {
	if port < 1 || port > 65535 || !validProxyListServer(server) {
		return Node{}, false
	}
	canonical := map[string]any{"type": protocol, "server": server, "port": port}
	if username != "" {
		canonical["username"] = username
	}
	if password != "" {
		canonical["password"] = password
	}
	node, err := finalize(net.JoinHostPort(server, strconv.Itoa(port)), protocol, canonical)
	if err != nil {
		return Node{}, false
	}
	return node, true
}

// validProxyListServer accepts what a dialer can be pointed at: an IP literal or
// a hostname. The check is intentionally shallow — a bad hostname fails at dial
// time and is the consumer's business — but it is what stops an arbitrary
// sentence from being read as an endpoint.
func validProxyListServer(server string) bool {
	server = strings.TrimSpace(server)
	if server == "" || len(server) > 253 {
		return false
	}
	return !strings.ContainsAny(server, " \t\r\n?#[]{}")
}

// hasBareEndpointLines reports whether a document contains at least one line
// that names an endpoint without a scheme. Such a document is not a pure share
// URI list, and treating it as one would silently drop those lines: the URI line
// parser skips anything without "://" instead of failing on it.
//
// This is a detection predicate, not a validator. It deliberately says nothing
// about whether the line is a *usable* endpoint; a document of pure prose gets
// past it and is rejected by looksLikeProxyList instead, with the ordinary
// "format is not supported" error rather than a misleading failure dump.
func hasBareEndpointLines(text string) bool {
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "://") {
			continue
		}
		return true
	}
	return false
}

// proxyListEndpointRatio is the share of non-empty lines that must resolve to an
// endpoint for a document to be treated as a bare list. Below it the document is
// left alone so the ordinary "format is not supported" error is reported, rather
// than a misleading per-line failure dump.
const proxyListEndpointRatio = 0.5

func looksLikeProxyList(text string) bool {
	total, resolved := 0, 0
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		total++
		if _, ok := parseProxyListEndpointLine(line); ok {
			resolved++
		}
	}
	return total > 0 && float64(resolved) >= float64(total)*proxyListEndpointRatio
}
