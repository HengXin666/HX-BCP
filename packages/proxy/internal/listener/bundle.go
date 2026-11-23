package listener

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ShareBundle combines independently reachable listeners into one client
// subscription. Each entry keeps its own protocol, endpoint and credentials.
type ShareBundle struct {
	Name    string
	Exports []ShareExport
}

// NewShareBundle assigns stable unique display names before rendering.
func NewShareBundle(name string, exports []ShareExport) ShareBundle {
	used := make(map[string]int)
	normalized := make([]ShareExport, 0, len(exports))
	for _, export := range exports {
		nodes := make([]ShareNode, 0, len(export.Nodes))
		for _, node := range export.Nodes {
			base := strings.TrimSpace(node.Name)
			if base == "" {
				base = "HX-PROXY"
			}
			used[base]++
			unique := base
			if used[base] > 1 {
				unique = fmt.Sprintf("%s (%d)", base, used[base])
			}
			nodes = append(nodes, ShareNode{Name: unique, Auth: node.Auth})
		}
		if len(nodes) == 0 {
			continue
		}
		normalized = append(normalized, NewShareExport(
			export.Name,
			export.Kind,
			export.Host,
			export.Port,
			nodes,
			export.Transport,
			export.Endpoint,
		))
	}
	return ShareBundle{Name: strings.TrimSpace(name), Exports: normalized}
}

func (bundle ShareBundle) NodeCount() int {
	count := 0
	for _, export := range bundle.Exports {
		count += len(export.Nodes)
	}
	return count
}

func (bundle ShareBundle) uriBody() string {
	var body strings.Builder
	for _, export := range bundle.Exports {
		body.WriteString(export.Body)
	}
	return body.String()
}

// Render returns the same four formats as a single-listener share export.
func (bundle ShareBundle) Render(format string) (body, fileName, contentType string, err error) {
	name := bundle.Name
	if name == "" {
		name = "HX-Pxy"
	}
	uriBody := bundle.uriBody()
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "v2rayn":
		return base64.StdEncoding.EncodeToString([]byte(uriBody)), sanitizeFileName(name) + ".txt", "text/plain; charset=utf-8", nil
	case "uri":
		return uriBody, sanitizeFileName(name) + ".txt", "text/plain; charset=utf-8", nil
	case "clash", "mihomo":
		proxies := make([]map[string]any, 0, bundle.NodeCount())
		names := make([]string, 0, bundle.NodeCount()+1)
		groupName := "HX-PROXY"
		for _, export := range bundle.Exports {
			for _, node := range export.Nodes {
				if node.Name == groupName {
					groupName = "HX-PROXY-GROUP"
				}
				proxies = append(proxies, export.clashProxy(node))
				names = append(names, node.Name)
			}
		}
		encoded, encodeErr := yaml.Marshal(map[string]any{
			"mode":      "rule",
			"log-level": "info",
			"allow-lan": false,
			"proxies":   proxies,
			"proxy-groups": []map[string]any{{
				"name": groupName, "type": "select", "proxies": append(names, "DIRECT"),
			}},
			"rules": []string{"MATCH," + groupName},
		})
		if encodeErr != nil {
			return "", "", "", fmt.Errorf("encode Clash subscription bundle: %w", encodeErr)
		}
		return string(encoded), sanitizeFileName(name) + ".yaml", "application/yaml; charset=utf-8", nil
	case "sing-box", "singbox":
		outbounds := make([]map[string]any, 0, bundle.NodeCount())
		for _, export := range bundle.Exports {
			for _, node := range export.Nodes {
				outbounds = append(outbounds, export.singBoxOutbound(node))
			}
		}
		encoded, encodeErr := json.MarshalIndent(map[string]any{"outbounds": outbounds}, "", "  ")
		if encodeErr != nil {
			return "", "", "", fmt.Errorf("encode sing-box subscription bundle: %w", encodeErr)
		}
		return string(encoded) + "\n", sanitizeFileName(name) + ".json", "application/json; charset=utf-8", nil
	default:
		return "", "", "", fmt.Errorf("%w: format must be v2rayn, clash, sing-box, or uri", ErrInvalid)
	}
}
