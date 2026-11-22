/**
 * hxcp.api 的读写接口与类型. 只发相对路径, 页面在 / 或 /proxy/<name>/ 下都能用.
 * API 部署到非 loopback 时要 Bearer, 令牌只放在本标签页的 sessionStorage.
 */
export interface EngineStats {
	hits?: number;
	misses?: number;
	passed?: number;
	filled?: number;
	skipped?: number;
	bytes_served?: number;
	bytes_network?: number;
	errors?: number;
	tunnels?: number;
	decrypted?: number;
	revalidated?: number;
	reasons?: Record<string, number>;
	saved_ratio?: number;
}
export interface Stats {
	engine: EngineStats;
	engine_state: string;
	running?: number;
	instances?: number;
	store: { entries: number; bytes: number; hits: number; bytes_served: number; root: string };
	flows: { enabled?: boolean; records?: number; written?: number; dropped?: number; root?: string };
}
export interface Flow {
	kind: string;
	verdict?: string;
	url: string;
	method?: string;
	status?: number;
	size?: number;
	encoded?: number;
	type?: string;
	mime?: string;
	client?: string;
	via?: string;
	tls?: string;
	alpn?: string[];
	http_version?: string;
	ms?: number;
	at: number;
	req_headers?: Record<string, string>;
	resp_headers?: Record<string, string>;
	post_data?: string;
	body_ref?: { sha256?: string; size?: number; dropped?: string };
}
export interface Entry {
	key: string;
	url: string;
	host: string;
	type: string;
	status: number;
	size: number;
	hit_count: number;
	source: string;
	stored_at: number;
	fresh_until: number;
	etag?: string;
}
export interface HostRow {
	host: string;
	entries: number;
	bytes: number;
	hits: number;
	saved: number;
	conns: number;
	clients: string[];
	last: number;
	mode: "decrypt" | "tunnel";
	reason: string;
}
export interface Rules {
	decrypt: string[];
	never_decrypt: string[];
	decrypt_all: boolean;
	updated_at?: number;
}
export interface Listener {
	name: string;
	mode: string;
	port: number;
	host: string;
	upstream: string;
	upstream_auth: boolean;
}
export interface Access {
	running: boolean;
	proxy_url: string;
	listeners: Listener[];
	decrypting: boolean;
	rules: Rules;
	ca: { exists: boolean; path: string };
	snippets: Record<string, string>;
	notes: Record<string, string>;
}
export interface Client {
	client: string;
	kinds: string[];
	state: string;
	updated_at: number;
}
export type TlsMode = "decrypt" | "tunnel" | "never";

const TOKEN_KEY = "hxcp-token";

function token(): string {
	try {
		return sessionStorage.getItem(TOKEN_KEY) ?? "";
	} catch {
		return "";
	}
}

export function setToken(value: string): void {
	try {
		sessionStorage.setItem(TOKEN_KEY, value.trim());
	} catch {
		/* 隐私模式下写不进就算了, 下次再问 */
	}
}

export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string,
	) {
		super(message);
	}
}

export async function call<T>(path: string, init: { method?: string; body?: unknown } = {}): Promise<T> {
	const headers: Record<string, string> = { accept: "application/json" };
	if (init.body !== undefined) headers["content-type"] = "application/json";
	const t = token();
	if (t) headers.authorization = "Bearer " + t;
	const r = await fetch(path.replace(/^\//, ""), {
		method: init.method ?? "GET",
		headers,
		body: init.body === undefined ? undefined : JSON.stringify(init.body),
	});
	if (!r.ok) {
		const text = await r.text().catch(() => "");
		throw new ApiError(r.status, "HTTP " + r.status + (text ? " " + text.slice(0, 160) : ""));
	}
	return (await r.json()) as T;
}

export const api = {
	stats: () => call<Stats>("stats"),
	access: () => call<Access>("access"),
	clients: () => call<{ clients: Client[] }>("clients"),
	hosts: () => call<{ hosts: HostRow[] }>("hosts?limit=500"),
	rules: () => call<Rules>("rules"),
	putRules: (r: Rules) => call<Rules>("rules", { method: "PUT", body: r }),
	setHost: (host: string, mode: TlsMode) => call<Rules>("rules/host", { method: "POST", body: { host, mode } }),
	flows: (q: string) => call<{ count: number; flows: Flow[] }>("flows?" + q),
	flowBody: (sha: string, offset: number) =>
		call<{ text: string; size: number; returned: number; eof: boolean; offset: number }>(
			"flow/body?sha256=" + encodeURIComponent(sha) + "&offset=" + offset + "&limit=131072",
		),
	entries: (q: string, order: string) =>
		call<{ count: number; entries: Entry[] }>("entries?limit=500&order=" + order + (q ? "&q=" + encodeURIComponent(q) : "")),
	body: (url: string) => call<{ sha256: string; size: number; preview: string }>("body?url=" + encodeURIComponent(url)),
	deleteEntry: (url: string) => call<{ deleted: number }>("entry?url=" + encodeURIComponent(url), { method: "DELETE" }),
	purge: () => call<{ purged: number }>("purge", { method: "POST" }),
};
