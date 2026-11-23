package residential

import "slices"

// Rotation modes describe how a vendor hands out new exit IPs.
const (
	// RotationSessionTemplate encodes a sticky session id in the username. A
	// new session id yields a new exit IP; the same id keeps one.
	RotationSessionTemplate = "session-template"
	// RotationPerRequest means the gateway rotates on its own per request or
	// per connection and offers no session pinning.
	RotationPerRequest = "per-request"
	// RotationAPIList means exit endpoints are fetched from a vendor HTTP API
	// rather than derived from a username.
	RotationAPIList = "api-list"
	// RotationCloudflareWorker means exit endpoints are fetched from a
	// Cloudflare Worker panel (BPB-Worker-Panel) subscription link. The panel
	// answers with base64-encoded VLESS/Trojan share URIs; each refresh of the
	// link resolves fresh Cloudflare edge addresses, so the exit only changes
	// when the consumer explicitly asks for a new node.
	RotationCloudflareWorker = "cf-worker"
	// RotationHXCFWsPxy means exit endpoints are local HTTP CONNECT listeners
	// minted by HX-CF-WsPxy. The control plane POSTs /session on a loopback
	// SessionPlane; Mihomo dials the returned 127.0.0.1 port. next maps to
	// POST /session/:id/rotate so the colo pin actually changes.
	RotationHXCFWsPxy = "hx-cf-wspxy"
)

// Preset is a vendor-specific starting point for a provider configuration.
//
// Presets are an explicit registry rather than a plugin system: adding a vendor
// means appending one literal here. Verified reports whether the field values
// were confirmed against the vendor's own documentation — an unverified preset
// still works, but the operator must confirm the rendered username with the
// provider test probe before trusting it.
type Preset struct {
	Vendor            string `json:"vendor"`
	Label             string `json:"label"`
	Protocol          string `json:"protocol"`
	GatewayHost       string `json:"gateway_host"`
	GatewayPort       int    `json:"gateway_port"`
	UsernameTemplate  string `json:"username_template"`
	RotationMode      string `json:"rotation_mode"`
	SessionTTLSeconds int    `json:"session_ttl_seconds"`
	PoolSize          int    `json:"pool_size"`
	// Verified is false when the gateway syntax could not be confirmed against
	// vendor documentation. The UI surfaces this so an operator knows to verify
	// the template with a test connection before relying on it.
	Verified bool   `json:"verified"`
	DocURL   string `json:"doc_url,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

// presets is the registered vendor list, ordered for display.
var presets = []Preset{
	{
		Vendor:            "bestproxy",
		Label:             "BestProxy",
		Protocol:          "http",
		GatewayHost:       "proxy.bestproxy.com",
		GatewayPort:       2312,
		UsernameTemplate:  "{user}_area-{region}_life-{ttl}_session-{session}",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 60,
		PoolSize:          8,
		Verified:          true,
		DocURL:            "https://bestproxy.com",
		Notes: "BestProxy 动态住宅粘性账号语法（已在 hx-auto-outlook 中验证）：" +
			"`账号_area-国家_life-分钟_session-会话ID`。网关为 proxy.bestproxy.com:2312；" +
			"国家代码请填大写（如 US），系统会原样保留；life 单位为分钟，" +
			"session_ttl_seconds 建议填 30-90；客户端建立逻辑会话时系统才生成供应商会话 ID，" +
			"不同客户端会话获得独立出口 IP。保存后用「测试连接」确认出口 IP。",
	},
	{
		Vendor:            "rapidproxy",
		Label:             "RapidProxy",
		Protocol:          "http",
		GatewayHost:       "us.rapidproxy.io",
		GatewayPort:       5001,
		UsernameTemplate:  "{user}-residential-{region}-session-{session}-stime-{ttl}",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 60,
		PoolSize:          8,
		Verified:          true,
		DocURL:            "https://www.rapidproxy.io",
		Notes: "RapidProxy 动态住宅粘性账号语法（已在 hx-auto-outlook 工作凭据中验证）：" +
			"`账号-residential-国家-session-会话ID-stime-分钟`。网关默认 us.rapidproxy.io:5001；" +
			"国家/地区代码原样保留（如 US、GLOBAL）；stime 单位为分钟，官方粘性范围 1-180，" +
			"session_ttl_seconds 建议填 60-600；客户端建立逻辑会话时系统才生成会话 ID，" +
			"不同客户端会话获得独立出口 IP。保存后用「测试连接」确认出口 IP。",
	},
	{
		Vendor:            "generic-sticky",
		Label:             "通用 · 粘滞会话网关",
		Protocol:          "http",
		GatewayHost:       "",
		GatewayPort:       0,
		UsernameTemplate:  "{user}-session-{session}",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 600,
		PoolSize:          8,
		Verified:          true,
		Notes:             "适用于把 sticky session id 编码在用户名里的多数住宅代理供应商。",
	},
	{
		Vendor:            "generic-region-sticky",
		Label:             "通用 · 地区 + 粘滞会话",
		Protocol:          "http",
		GatewayHost:       "",
		GatewayPort:       0,
		UsernameTemplate:  "{user}-country-{country}-session-{session}",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 600,
		PoolSize:          8,
		Verified:          true,
		Notes:             "在粘滞会话之外再指定出口国家/地区。",
	},
	{
		Vendor:            "generic-rotating",
		Label:             "通用 · 每请求轮换网关",
		Protocol:          "http",
		GatewayHost:       "",
		GatewayPort:       0,
		UsernameTemplate:  "{user}",
		RotationMode:      RotationPerRequest,
		SessionTTLSeconds: 0,
		PoolSize:          1,
		Verified:          true,
		Notes:             "网关自行轮换出口 IP，不支持粘滞会话，只能用于透传模式。",
	},
	{
		Vendor:            "generic-socks5-sticky",
		Label:             "通用 · SOCKS5 粘滞会话",
		Protocol:          "socks5",
		GatewayHost:       "",
		GatewayPort:       0,
		UsernameTemplate:  "{user}-session-{session}",
		RotationMode:      RotationSessionTemplate,
		SessionTTLSeconds: 600,
		PoolSize:          8,
		Verified:          true,
		Notes:             "与粘滞会话网关相同，但上游走 SOCKS5。",
	},
	{
		Vendor:            "bestproxy-api",
		Label:             "BestProxy · API 提取",
		Protocol:          "http",
		GatewayHost:       "",
		GatewayPort:       0,
		UsernameTemplate:  "",
		RotationMode:      RotationAPIList,
		SessionTTLSeconds: 60,
		PoolSize:          8,
		Verified:          true,
		DocURL:            "https://bestproxy.com",
		Notes: "在 BestProxy 面板「API提取」生成 API 链接（包含 app_key 等参数），" +
			"完整粘贴到 api_url。客户端建立会话或换 IP 时实时请求新的 IP:port 节点。" +
			"该模式无需网关账号密码；请把本机公网出口 IP 加入 BestProxy 白名单。",
	},
	{
		Vendor:            "bpb-panel",
		Label:             "BPB-Worker-Panel · Cloudflare Worker",
		Protocol:          "vless",
		GatewayHost:       "",
		GatewayPort:       0,
		UsernameTemplate:  "",
		RotationMode:      RotationCloudflareWorker,
		SessionTTLSeconds: 0,
		PoolSize:          4,
		Verified:          true,
		DocURL:            "https://github.com/bia-pain-bache/BPB-Worker-Panel",
		Notes: "把 BPB-Worker-Panel 部署后的面板链接或 raw 订阅链接粘贴到 worker_url。" +
			"控制面经配置的出口代理（api_proxy_url）请求该链接，解析返回的 VLESS/Trojan 节点。" +
			"每次用户主动 next 才重新请求并轮换出口地址；TTL 为 0 时不自动刷新。",
	},
	{
		Vendor:            "hx-cf-wspxy",
		Label:             "HX-CF-WsPxy · Cloudflare colo 出口",
		Protocol:          "http",
		GatewayHost:       "",
		GatewayPort:       0,
		UsernameTemplate:  "",
		RotationMode:      RotationHXCFWsPxy,
		SessionTTLSeconds: 0,
		PoolSize:          8,
		Verified:          true,
		DocURL:            "https://github.com/HengXin666/HX-CF-WsPxy",
		Notes: "把本机 HX-CF-WsPxy 控制面 origin 填到 api_url，例如 http://127.0.0.1:2470。" +
			"控制面 POST /session 开会话，Mihomo 拨返回的 127.0.0.1 CONNECT 端口；" +
			"客户端 next 调用 POST /session/:id/rotate 换 pin（换 colo），释放时 DELETE。" +
			"WSP1 只跑在 WsPxy 与 Worker 之间，住宅客户端仍走渠道 Listener。TTL 为 0，不自动刷新。",
	},
}

// Presets returns a copy of the registered vendor presets.
func Presets() []Preset {
	return slices.Clone(presets)
}

// PresetByVendor looks up one preset by its vendor key.
func PresetByVendor(vendor string) (Preset, bool) {
	for _, preset := range presets {
		if preset.Vendor == vendor {
			return preset, true
		}
	}
	return Preset{}, false
}

// SupportedProtocols lists the upstream gateway protocols the data plane can
// dial. These map directly onto Mihomo outbound types, so no new protocol
// implementation is involved.
func SupportedProtocols() []string {
	return []string{"http", "https", "socks5"}
}

// SupportedWorkerProtocols lists the proxy protocols that a Cloudflare Worker
// panel (BPB-Worker-Panel) subscription can emit. They are full Mihomo outbound
// types with their own transport; the control plane only carries the canonical
// config through, it never implements the protocol itself.
func SupportedWorkerProtocols() []string {
	return []string{"vless", "trojan"}
}

// SupportedRotationModes lists the accepted rotation modes.
func SupportedRotationModes() []string {
	return []string{RotationSessionTemplate, RotationPerRequest, RotationAPIList, RotationCloudflareWorker, RotationHXCFWsPxy}
}
