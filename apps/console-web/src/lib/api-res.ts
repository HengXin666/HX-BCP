/**
 * 资源页 (代理, 扩展, 模板) 与详情抽屉的接口. 读写都走 8325 的 /api/v1 信封; 失败返回 ok=false, 页面写 "未上报" 或报错.
 * 字段名与后端 models.Proxy / Extension / Template 一致.
 */
import { API, getEnvelope, type Result } from "./api";
import { sendCompat } from "./http";
import { KEYS, invalidate, keyed, type KeyedLoader } from "./cache";

const enc = encodeURIComponent;

/** JSON 写请求, 统一走 http.ts (超时, 统一错误对象); 写请求不重试. */
export function call<T>(method: string, path: string, body?: unknown): Promise<Result<T>> {
	return sendCompat<T>(method, path, body);
}

/** 写请求并在成功后失效相关缓存. */
async function writeRes<T>(p: Promise<Result<T>>, ...keys: string[]): Promise<Result<T>> {
	const r = await p;
	if (r.ok) invalidate(...keys);
	return r;
}

function list<T>(path: string, signal?: AbortSignal): Promise<Result<T[]>> {
	return getEnvelope<{ list: T[] }>(API + path, signal).then((r) => (r.ok ? { ok: true, data: r.data?.list ?? [] } : r));
}

/** 带缓存键的列表读, 资源页用. 写操作后失效对应键即可触发重拉. */
function cachedList<T>(key: string, path: string, freshMs = 10000): KeyedLoader<T[]> {
	return keyed<T[]>(key, freshMs, (s) => list<T>(path, s));
}

// ---------- 代理库 ----------
export type ProxyItem = {
	id: string;
	label: string;
	type: string;
	host: string;
	port: number;
	username?: string;
	password?: string;
	country?: string;
	asn?: string;
	latency_ms?: number;
	checked_at?: number;
};
export type ProxyBody = { label: string; type: string; host: string; port: number; username: string; password: string };

const proxyList = cachedList<ProxyItem>(KEYS.proxies, "/proxies");
export const proxyApi = {
	list: proxyList,
	create: (b: ProxyBody) => writeRes(call<ProxyItem>("POST", "/proxies", b), KEYS.proxies),
	update: (id: string, b: Partial<ProxyBody>) => writeRes(call<ProxyItem>("PUT", "/proxies/" + enc(id), b), KEYS.proxies),
	remove: (id: string) => writeRes(call("DELETE", "/proxies/" + enc(id)), KEYS.proxies),
};

// ---------- 扩展 ----------
export type ExtensionItem = { id: string; name: string; path: string; categories: string[]; platforms: string[]; bind_env_ids: string[]; created_at: number };
export type ExtensionBody = { name: string; path: string; categories: string[]; platforms: string[] };

const extList = cachedList<ExtensionItem>(KEYS.extensions, "/extensions");
export const extensionApi = {
	list: extList,
	create: (b: ExtensionBody) => writeRes(call<ExtensionItem>("POST", "/extensions", b), KEYS.extensions),
	update: (id: string, b: Partial<ExtensionBody>) => writeRes(call<ExtensionItem>("PUT", "/extensions/" + enc(id), b), KEYS.extensions),
	remove: (id: string) => writeRes(call("DELETE", "/extensions/" + enc(id)), KEYS.extensions),
	/** 后端把请求体整个当 env_ids 数组读, 覆盖式绑定: 不在数组里的环境会被解绑. */
	bind: (id: string, envIds: string[]) => writeRes(call("POST", "/extensions/" + enc(id) + "/bind", envIds), KEYS.extensions),
};

// ---------- 模板 ----------
export type TemplateItem = {
	id: string;
	name: string;
	description: string;
	fingerprint: Record<string, unknown>;
	proxy_strategy: Record<string, unknown>;
	extensions: string[];
	launch: Record<string, unknown>;
	created_at: number;
};
export type TemplateBody = { name: string; description: string; fingerprint: Record<string, unknown>; launch: Record<string, unknown> };

const tplList = cachedList<TemplateItem>(KEYS.templates, "/templates");
export const templateApi = {
	list: tplList,
	create: (b: TemplateBody) => writeRes(call<TemplateItem>("POST", "/templates", b), KEYS.templates),
	update: (id: string, b: Partial<TemplateBody>) => writeRes(call<TemplateItem>("PUT", "/templates/" + enc(id), b), KEYS.templates),
	remove: (id: string) => writeRes(call("DELETE", "/templates/" + enc(id)), KEYS.templates),
	/** 后端语义: 按模板新建 count 个环境 (上限 50). 新建环境后失效环境与文件夹缓存. */
	spawn: (id: string, count: number) => writeRes(call<{ count: number }>("POST", "/templates/" + enc(id) + "/apply?count=" + count), KEYS.envs, KEYS.folders),
};

// ---------- 单个环境的详情 ----------
/** GET /fingerprint/check: 在跑的浏览器里实测出来的值. */
export type FpObserved = {
	ua?: string;
	platform?: string;
	language?: string;
	langs?: string[];
	cores?: number;
	memory?: number;
	tz?: string;
	screen?: string;
	dpr?: number;
	webgl?: string;
	webdriver?: boolean;
};
export type CookieItem = { name: string; domain: string; path?: string; expires?: number; httpOnly?: boolean; secure?: boolean; session?: boolean };

const envPath = (id: string, tail: string): string => "/environments/" + enc(id) + tail;

export const detailApi = {
	fpCheck: (id: string, s?: AbortSignal) => getEnvelope<FpObserved>(API + envPath(id, "/fingerprint/check"), s),
	cookies: (id: string, s?: AbortSignal) => getEnvelope<{ total: number; cookies: CookieItem[] }>(API + envPath(id, "/cookies"), s),
	hxcpStart: (id: string) => writeRes(call<{ pid: number; status: string }>("POST", envPath(id, "/hxcp/start")), KEYS.env(id)),
	hxcpStop: (id: string) => writeRes(call<{ status: string }>("POST", envPath(id, "/hxcp/stop")), KEYS.env(id)),
	clearCache: (id: string, types: string[]) => call<{ cleared: string[] }>("POST", envPath(id, "/cache/clear"), { types }),
	/** 后端是 POST (要带可选口令), 返回整个账号包 JSON. 环境必须在跑. */
	exportPack: (id: string, password: string) => call<Record<string, unknown>>("POST", envPath(id, "/account-pack/export"), { password }),
};

/** 把 JSON 存成文件下载, 不经过后端第二次. */
export function downloadJson(name: string, data: unknown): void {
	const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: "application/json" }));
	const a = document.createElement("a");
	a.href = url;
	a.download = name;
	a.click();
	setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// ---------- 导入 ----------
/** 导入: 现有 Firefox profile 目录 (copy 复制, move 移动, reference 原地引用) 或账号包 JSON (导出时可加口令). 都新建一个人类组环境. */
export const importApi = {
	profile: (body: { profile_dir: string; mode: string; name: string }) => writeRes(call<{ id: string; name: string }>("POST", "/environments/import-profile", body), KEYS.envs, KEYS.folders),
	pack: (body: { pack: Record<string, unknown>; password: string; name: string }) => writeRes(call<{ id: string; name: string }>("POST", "/account-packs/import", body), KEYS.envs, KEYS.folders),
};

// ---------- 系统 ----------
export type LogEntry = { at: number; action: string; detail: Record<string, unknown> };
export type Orphan = { pid: number; env_id: string; cmdline: string };
export type Settings = {
	chromium_path: string;
	firefox_path: string;
	data_dir: string;
	cdp_base_port: number;
	api_port: number;
	api_key: string;
	proxy_check_before_launch: boolean;
	headless_default: boolean;
	default_homepage: string;
	auto_match_timezone: boolean;
	dark_mode: boolean;
	default_kernel?: string;
	human_loopback?: boolean;
};

export const systemApi = {
	logs: (limit: number, signal?: AbortSignal) => list<LogEntry>("/logs?limit=" + limit, signal),
	orphans: (signal?: AbortSignal) => getEnvelope<{ count: number; orphans: Orphan[] }>(API + "/orphans", signal),
	cleanup: () => writeRes(call<{ killed: number[]; count: number }>("POST", "/orphans/cleanup"), KEYS.orphans, KEYS.envs),
	settings: (signal?: AbortSignal) => getEnvelope<Settings>(API + "/settings", signal),
	saveSettings: (patch: Partial<Settings>) => writeRes(call<Settings>("PUT", "/settings", patch), KEYS.settings),
};
