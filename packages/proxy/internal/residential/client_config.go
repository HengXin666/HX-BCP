package residential

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ClashConfig renders a session-scoped configuration for Clash-compatible
// clients. The returned profile contains only the public WS endpoint and the
// session credential; residential provider credentials never leave HX.
func ClashConfig(session ClientSession) ([]byte, error) {
	if session.ProxyEndpoint == nil {
		return nil, errors.New("residential session has no public client endpoint")
	}
	if session.ProxyPassword == "" {
		return nil, errors.New("residential session credential is unavailable")
	}

	endpoint := session.ProxyEndpoint
	name := "HX-Residential-" + session.SessionID
	proxy := map[string]any{
		"name":   name,
		"server": endpoint.Server,
		"port":   endpoint.Port,
	}
	switch endpoint.Type {
	case "vless-ws":
		proxy["type"] = "vless"
		proxy["uuid"] = session.ProxyPassword
		proxy["encryption"] = "none"
		proxy["network"] = "ws"
		proxy["udp"] = true
		addWebSocketFields(proxy, endpoint)
	case "vmess-ws":
		proxy["type"] = "vmess"
		proxy["uuid"] = session.ProxyPassword
		proxy["alterId"] = 0
		proxy["cipher"] = "auto"
		proxy["network"] = "ws"
		addWebSocketFields(proxy, endpoint)
	case "trojan-ws":
		proxy["type"] = "trojan"
		proxy["password"] = session.ProxyPassword
		proxy["network"] = "ws"
		addWebSocketFields(proxy, endpoint)
	case "http-connect":
		proxy["type"] = "http"
		proxy["username"] = session.ProxyUsername
		proxy["password"] = session.ProxyPassword
		if endpoint.TLS {
			proxy["tls"] = true
		}
	case "socks5":
		proxy["type"] = "socks5"
		proxy["username"] = session.ProxyUsername
		proxy["password"] = session.ProxyPassword
	default:
		return nil, fmt.Errorf("unsupported residential client endpoint %q", endpoint.Type)
	}

	document := map[string]any{
		"proxies": []map[string]any{proxy},
		"proxy-groups": []map[string]any{{
			"name":    "HX-Residential",
			"type":    "select",
			"proxies": []string{name},
		}},
		"rules": []string{"MATCH,HX-Residential"},
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode Clash residential config: %w", err)
	}
	return encoded, nil
}

func addWebSocketFields(proxy map[string]any, endpoint *ClientProxyEndpoint) {
	proxy["tls"] = endpoint.TLS
	if endpoint.SNI != "" {
		proxy["servername"] = endpoint.SNI
	}
	proxy["ws-opts"] = map[string]any{
		"path": endpoint.Path,
		"headers": map[string]string{
			"Host": endpoint.Server,
		},
	}
}
