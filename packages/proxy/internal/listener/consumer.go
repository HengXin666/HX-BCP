package listener

import "strings"

// ConsumerNodesPath is the frozen public path prefix of the programmatic node
// listing contract. Consumers call it as
//
//	GET /nodes/<share-token>
//
// The credential is the same share token as /sub/<share-token>, so the node
// listing and the human subscription are rendered from one export structure and
// cannot disagree about what exists. See docs/CONSUMER_INTEGRATION_CONTRACT.md.
//
// The decision — and what it beat, notably reusing the high-privilege /ctl/
// token — is recorded in
// .agents/notes/implemented/feature/2026-09-14-consumer-nodes-api.md.
//
// It sits in the token-addressed root namespace beside /sub/ and /ctl/, not
// under /api/v1/. That namespace separation is load-bearing: /api/v1/ is
// session-authenticated administrator space, and an earlier revision of this
// contract registered the public listing there at the same path as the
// administrator node list, which net/http's ServeMux rejects with a duplicate
// registration panic at startup.
const ConsumerNodesPath = "/nodes/"

// The vocabulary below is the contract. scripts/verify-consumer-contract.ts
// compares every token here against docs/CONSUMER_INTEGRATION_CONTRACT.md and
// .agents/skills/hx-consumer-api/SKILL.md; a token that moves in one place and
// not the others is a broken contract, not documentation drift.

// ConsumerFields is the frozen field vocabulary of the node payload.
var ConsumerFields = []string{
	"name",
	"share_path",
	"subscription_urls",
	"nodes",
	"protocol",
	"host",
	"port",
	"auth",
	"username",
	"password",
	"transport",
	"ws_path",
	"tls",
	"server_name",
	"browser_compatible",
	"uri",
}

// ConsumerProtocols is closed on purpose: a consumer that meets a value outside
// this set must fail loudly rather than guess how to dial it.
var ConsumerProtocols = []string{"mixed", "http", "socks", "vless", "vmess", "trojan"}

// ConsumerTransports is closed for the same reason.
var ConsumerTransports = []string{"tcp", "ws"}

// ConsumerSubscriptionFormats are the four renderings a consumer may point a
// client at; they mirror Render's accepted formats.
var ConsumerSubscriptionFormats = []string{"clash", "v2rayn", "sing-box", "uri"}

// ConsumerStatusCodes are the statuses a consumer may branch on. The contract
// deliberately has no retryable 404: reading the node list is a pure read.
var ConsumerStatusCodes = []int{200, 404, 405, 500}

// ConsumerNodeAuth carries the credential of one published node. It is null
// when the entry point accepts unauthenticated connections.
type ConsumerNodeAuth struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// ConsumerNode is one entry point a consumer can dial.
type ConsumerNode struct {
	Name string `json:"name"`
	// Protocol is one of ConsumerProtocols.
	Protocol string            `json:"protocol"`
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	Auth     *ConsumerNodeAuth `json:"auth"`
	// Transport is one of ConsumerTransports.
	Transport string `json:"transport"`
	// WSPath is set exactly when Transport is "ws". It is already normalized to
	// the reserved prefix and must be used verbatim.
	WSPath string `json:"ws_path,omitempty"`
	TLS    bool   `json:"tls"`
	// ServerName is the SNI / Host header, set when TLS is on.
	ServerName string `json:"server_name,omitempty"`
	// BrowserCompatible reports whether the node can be used directly as a
	// browser or plain-HTTP-client proxy. WebSocket transports always are not:
	// a browser cannot speak a WebSocket proxy.
	//
	// A TLS endpoint does NOT disqualify a node: Chromium accepts an
	// "https://host:port" --proxy-server and completes requests through it,
	// verified end to end. So this stays keyed on transport alone.
	BrowserCompatible bool `json:"browser_compatible"`
	// URI is this node's share URI, for pasting into an existing config.
	URI string `json:"uri"`
}

// ConsumerPayload is the programmatic node listing.
type ConsumerPayload struct {
	Name string `json:"name"`
	// SharePath is the /sub/<token> prefix shared by every subscription URL.
	SharePath string `json:"share_path"`
	// SubscriptionURLs always contains one entry per
	// ConsumerSubscriptionFormats, as a path relative to the control plane.
	SubscriptionURLs map[string]string `json:"subscription_urls"`
	// Nodes is stably ordered: exports in their declared order, nodes in their
	// declared order, so two reads of the same token are byte-identical.
	Nodes []ConsumerNode `json:"nodes"`
}

// NodeURIs renders the share URIs of a single node.
func (export ShareExport) NodeURIs(node ShareNode) []string {
	return shareURIs(export.Kind, node.Name, export.Host, export.Port, node.Auth, export.Transport, export.Endpoint)
}

// ConsumerPayload renders this export as the programmatic node listing.
func (export ShareExport) ConsumerPayload(sharePath string) ConsumerPayload {
	return NewShareBundle(export.Name, []ShareExport{export}).ConsumerPayload(sharePath)
}

// ConsumerPayload renders every export in the bundle as one node listing.
func (bundle ShareBundle) ConsumerPayload(sharePath string) ConsumerPayload {
	payload := ConsumerPayload{
		Name:             strings.TrimSpace(bundle.Name),
		SharePath:        sharePath,
		SubscriptionURLs: make(map[string]string, len(ConsumerSubscriptionFormats)),
		Nodes:            make([]ConsumerNode, 0, bundle.NodeCount()),
	}
	for _, format := range ConsumerSubscriptionFormats {
		payload.SubscriptionURLs[format] = sharePath + "?format=" + format
	}
	for _, export := range bundle.Exports {
		for _, node := range export.Nodes {
			payload.Nodes = append(payload.Nodes, consumerNodeFromShare(export, node))
		}
	}
	return payload
}

func consumerNodeFromShare(export ShareExport, node ShareNode) ConsumerNode {
	transport := "tcp"
	if IsAdvancedKind(export.Kind) {
		transport = "ws"
	}
	result := ConsumerNode{
		Name:              node.Name,
		Protocol:          strings.ToLower(strings.TrimSpace(export.Kind)),
		Host:              export.Host,
		Port:              export.Port,
		Transport:         transport,
		TLS:               export.Endpoint.TLS,
		BrowserCompatible: transport == "tcp",
	}
	if transport == "ws" {
		// An advanced listener is always reached through the TLS-terminating
		// reverse proxy, never plaintext.
		result.WSPath = export.Transport.WSPath
		result.TLS = true
	}
	if result.TLS {
		result.ServerName = export.Endpoint.Host
	}
	if node.Auth != nil && (node.Auth.Username != "" || node.Auth.Password != "") {
		result.Auth = &ConsumerNodeAuth{Username: node.Auth.Username, Password: node.Auth.Password}
	}
	if uris := export.NodeURIs(node); len(uris) > 0 {
		result.URI = uris[0]
	}
	return result
}
