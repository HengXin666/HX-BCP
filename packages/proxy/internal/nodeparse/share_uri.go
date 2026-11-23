package nodeparse

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func parseURI(value string) (Node, error) {
	separator := strings.Index(value, "://")
	if separator <= 0 {
		return Node{}, errors.New("invalid share URI")
	}
	protocol := strings.ToLower(value[:separator])
	switch protocol {
	case "vless", "trojan", "http", "https", "socks", "socks5", "hysteria", "hysteria2", "hy2", "anytls", "ssh", "tuic":
		return parseStandardURI(value, protocol)
	case "vmess":
		return parseVMessURI(value)
	case "ss":
		return parseShadowsocksURI(value)
	default:
		return Node{}, fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func parseStandardURI(value, protocol string) (Node, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return Node{}, errors.New("invalid share URI")
	}
	server := strings.TrimSpace(parsed.Hostname())
	port, err := strconv.Atoi(parsed.Port())
	if server == "" || err != nil || port < 1 || port > 65535 {
		return Node{}, errors.New("invalid server or port")
	}
	tlsByScheme := protocol == "https"
	switch protocol {
	case "hy2":
		protocol = "hysteria2"
	case "https":
		protocol = "http"
	case "socks":
		protocol = "socks5"
	}
	canonical := map[string]any{"type": protocol, "server": server, "port": port}
	if parsed.User != nil {
		identity := parsed.User.Username()
		password, hasPassword := parsed.User.Password()
		switch protocol {
		case "vless":
			canonical["uuid"] = identity
		case "trojan":
			canonical["password"] = identity
		case "hysteria":
			canonical["auth-str"] = identity
		case "hysteria2", "anytls":
			canonical["password"] = identity
		case "tuic":
			canonical["uuid"] = identity
			if hasPassword {
				canonical["password"] = password
			}
		default:
			canonical["username"] = identity
			if hasPassword {
				canonical["password"] = password
			}
		}
	}
	if tlsByScheme {
		canonical["tls"] = true
	}
	if query := normalizeQuery(parsed.Query()); len(query) > 0 {
		canonical["query"] = query
	}
	name, _ := url.QueryUnescape(parsed.Fragment)
	if strings.TrimSpace(name) == "" {
		name = net.JoinHostPort(server, strconv.Itoa(port))
	}
	return finalize(name, protocol, canonical)
}

func parseVMessURI(value string) (Node, error) {
	decoded, ok := decodeBase64([]byte(strings.TrimPrefix(value, "vmess://")))
	if !ok {
		return Node{}, errors.New("invalid vmess base64 payload")
	}
	var raw map[string]any
	if err := json.Unmarshal(decoded, &raw); err != nil {
		return Node{}, errors.New("invalid vmess JSON payload")
	}
	canonical := normalizeMap(raw)
	server := strings.TrimSpace(stringValue(canonical["add"]))
	port, err := integerValue(canonical["port"])
	if server == "" || err != nil || port < 1 || port > 65535 {
		return Node{}, errors.New("invalid vmess server or port")
	}
	canonical["type"], canonical["server"], canonical["port"] = "vmess", server, port
	delete(canonical, "add")
	name := strings.TrimSpace(stringValue(canonical["ps"]))
	delete(canonical, "ps")
	if name == "" {
		name = net.JoinHostPort(server, strconv.Itoa(port))
	}
	return finalize(name, "vmess", canonical)
}

func parseShadowsocksURI(value string) (Node, error) {
	payload := strings.TrimPrefix(value, "ss://")
	fragment := ""
	if index := strings.Index(payload, "#"); index >= 0 {
		fragment, payload = payload[index+1:], payload[:index]
	}
	query := ""
	if index := strings.Index(payload, "?"); index >= 0 {
		query, payload = payload[index+1:], payload[:index]
	}
	if !strings.Contains(payload, "@") {
		decoded, ok := decodeBase64([]byte(payload))
		if !ok {
			return Node{}, errors.New("invalid shadowsocks base64 payload")
		}
		payload = string(decoded)
	}
	at := strings.LastIndex(payload, "@")
	if at <= 0 {
		return Node{}, errors.New("invalid shadowsocks authority")
	}
	credentials := payload[:at]
	if !strings.Contains(credentials, ":") {
		decoded, ok := decodeBase64([]byte(credentials))
		if !ok {
			return Node{}, errors.New("invalid shadowsocks credentials")
		}
		credentials = string(decoded)
	}
	credentialSeparator := strings.Index(credentials, ":")
	if credentialSeparator <= 0 {
		return Node{}, errors.New("invalid shadowsocks credentials")
	}
	server, portText, err := net.SplitHostPort(payload[at+1:])
	if err != nil {
		return Node{}, errors.New("invalid shadowsocks server or port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return Node{}, errors.New("invalid shadowsocks port")
	}
	canonical := map[string]any{
		"type": "ss", "server": server, "port": port,
		"cipher": credentials[:credentialSeparator], "password": credentials[credentialSeparator+1:],
	}
	if query != "" {
		values, _ := url.ParseQuery(query)
		canonical["query"] = normalizeQuery(values)
	}
	name, _ := url.QueryUnescape(fragment)
	if strings.TrimSpace(name) == "" {
		name = net.JoinHostPort(server, strconv.Itoa(port))
	}
	return finalize(name, "ss", canonical)
}

func normalizeQuery(values url.Values) map[string]any {
	result := make(map[string]any, len(values))
	for key, items := range values {
		if len(items) == 1 {
			result[strings.ToLower(key)] = items[0]
		} else {
			result[strings.ToLower(key)] = append([]string(nil), items...)
		}
	}
	return result
}

func decodeBase64(value []byte) ([]byte, bool) {
	compact := strings.Map(func(character rune) rune {
		if character == '\r' || character == '\n' || character == ' ' || character == '\t' {
			return -1
		}
		return character
	}, string(value))
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := encoding.DecodeString(compact)
		if err == nil && len(decoded) > 0 {
			return decoded, true
		}
	}
	return nil, false
}
