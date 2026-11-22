/**
 * 外部接入卡: GET /access 返回 Agent 接入所需的一切 (地址, 头, 密钥, 可调接口, MCP 工具), POST /access/rotate-key 换密钥.
 * 两个接口都只对人类通道开放 (lane_guard 拒 Agent). 密钥只在页面内存里, 不进本地存储与缓存之外的地方.
 */
import { API, getEnvelope, type Result } from "./api";
import { KEYS, invalidate, keyed } from "./cache";
import { sendCompat } from "./http";

export type AccessInfo = {
	base: string;
	api_key: string;
	headers: Record<string, string>;
	project_rule: string;
	agent_paths: string[];
	mcp: { url: string; tools: string[] };
	human_loopback: boolean;
	rules: string[];
};

export const accessInfo = keyed<AccessInfo>(KEYS.settings + ":access", 10000, (signal) => getEnvelope<AccessInfo>(API + "/access", signal));

export const accessApi = {
	rotate: async (): Promise<Result<{ api_key: string }>> => {
		const r = await sendCompat<{ api_key: string }>("POST", "/access/rotate-key");
		if (r.ok) invalidate(KEYS.settings);
		return r;
	},
	setLoopback: async (on: boolean): Promise<Result<unknown>> => {
		const r = await sendCompat<unknown>("PUT", "/settings", { human_loopback: on });
		if (r.ok) invalidate(KEYS.settings);
		return r;
	},
};

/** 给外部程序的示例: curl, Python httpx, MCP 客户端配置. project 为空时用占位. */
export function snippets(a: AccessInfo, project: string, reveal: boolean): Record<"curl" | "python" | "mcp", string> {
	const key = reveal ? a.api_key : "<api_key>";
	const p = project || "my-project";
	return {
		curl: ["curl -s " + a.base + "/api/v1/environments \\", "  -H 'x-api-key: " + key + "' \\", "  -H 'x-bcp-project: " + p + "'"].join("\n"),
		python: [
			"import httpx",
			"",
			"bcp = httpx.Client(base_url=\"" + a.base + "/api/v1\",",
			"                   headers={\"x-api-key\": \"" + key + "\", \"x-bcp-project\": \"" + p + "\"})",
			"env = bcp.post(\"/environments\", json={\"name\": \"job-1\"}).json()[\"data\"]",
			"run = bcp.post(f\"/environments/{env['id']}/start\").json()[\"data\"]",
			"print(run[\"cdp_http\"])  # 交给 Playwright / DrissionPage 接管",
		].join("\n"),
		mcp: JSON.stringify({ mcpServers: { "hx-browser": { type: "http", url: a.mcp.url, headers: { "x-api-key": key } } } }, null, 2),
	};
}
