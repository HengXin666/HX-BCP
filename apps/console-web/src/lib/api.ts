/** 控制台的后端读接口, 全部走 8325 的 /api/v1 (缓存统计由 8325 转 8898). 写接口在 api-write.ts. 请求层在 http.ts, 读缓存在 cache.ts. */
import { API, HUMAN_HEADERS, getEnvelopeCompat, getRawCompat, type Envelope, type Result } from "./http";
import { KEYS, cachedRead, keyed } from "./cache";

export { API, HUMAN_HEADERS, type Envelope, type Result };
export type { HttpError, HttpErrorKind, HttpResult } from "./http";

/** 页面所在目录的路径, 例如 / 或 /proxy/hx-browser/, 后端据此给会话 cookie 定 Path. */
export const PAGE_BASE = new URL(".", window.location.href).pathname;

export type EnvProxy = { type?: string; host?: string; port?: number; username?: string; password?: string; has_password?: boolean } | null;

/** 指纹里控制台会展示的字段, 名字与后端 models.Fingerprint 一致. */
export type EnvFingerprint = {
	browser?: string;
	kernel?: string;
	os?: string;
	user_agent?: string;
	language?: string;
	timezone?: string;
	resolution?: string;
	webgl_vendor?: string;
	webgl_renderer?: string;
	hardware_concurrency?: number;
	device_memory?: number;
	webrtc_mode?: string;
	kernel_version?: string;
	device_type?: string;
	color_depth?: number;
	canvas_noise?: boolean;
	audio_noise?: boolean;
	webgl_noise?: boolean;
	webrtc_public_ip?: string;
	headless?: boolean;
	tls_mode?: string;
	dark_mode?: boolean | null;
};

/** 按出口 IP 最近一次对齐到的区域, 后端 Environment.last_region. */
export type LastRegion = { ip?: string; country?: string; timezone?: string; checked_at?: number };

/** 防泄漏状态, 后端 leakproof.status: 放开的泄漏面, 其中人为确认过的, 本机做不到的及原因. */
export type LeakStatus = { exposures: string[]; acked: string[]; unavoidable: Record<string, string>; guarded: boolean };

export type Lane = "human" | "agent";

export type Env = {
	id: string;
	name: string;
	lane: Lane;
	project: string;
	folder: string;
	in_use: boolean;
	in_use_since: number;
	readonly: boolean;
	last_open_at?: number;
	group?: string;
	platform?: string;
	proxy?: EnvProxy;
	fingerprint?: EnvFingerprint | null;
	hxcp_enabled?: boolean;
	last_region?: LastRegion;
	location_mode?: string;
	launch?: { urls?: string[]; extra_args?: string[] };
	notes?: string;
	runtime?: Record<string, unknown> | null;
	visible?: boolean;
	visible_reason?: string;
	window_title?: string;
	open_count?: number;
	tags?: string[];
	created_at?: number;
	pending_restart?: boolean;
	prefs?: { isolation?: string; webrtc?: string } | null;
	leak_ack?: string[];
	leak?: LeakStatus;
};

/** 分组键 -> 文件夹名列表, 分组键是 human 或 agent:<项目名>, 判据在 packages/lane 的 group_key. */
export type FolderMap = Record<string, string[]>;

export type EnvPage = { list: Env[]; total: number; page: number; page_size: number };

/** 8898 /stats 的 engine 段 (引擎不在跑且无落盘运行态时为 null), 字段见 cache-proxy engine.Stats. */
export type EngineStats = {
	hits?: number;
	misses?: number;
	passed?: number;
	filled?: number;
	errors?: number;
	bytes_served?: number;
	bytes_network?: number;
	saved_ratio?: number;
};

export type CacheStats = {
	engine?: EngineStats | null;
	engine_state?: string;
	store?: { entries?: number; bytes?: number; hits?: number; bytes_served?: number };
	flows?: { records?: number };
	events?: number;
};

export type Verdict = "green" | "yellow" | "red" | "unknown";

export type VerdictReport = { verdict: Verdict; checked: number; at: string; evidence: unknown[] };

/** HTTP 失败时的说明: 优先取后端信封里的 code 与 msg. 已由 http.ts 的 errorLine 取代, 仅为兼容保留. @deprecated */
export async function errorText(res: Response): Promise<string> {
	try {
		const body = (await res.json()) as Envelope<unknown>;
		if (body.msg) return "HTTP " + res.status + " " + String(body.code ?? "") + " " + body.msg;
	} catch {
		// 非 JSON 响应只报状态码
	}
	return "HTTP " + res.status;
}

/** 读 8325 的信封 {code,msg,data}, code 不为 0 时返回 ok=false 与 msg. 读请求带超时与重试, 见 http.ts. */
export function getEnvelope<T>(path: string, signal?: AbortSignal): Promise<Result<T>> {
	return getEnvelopeCompat<T>(path, signal);
}

/** 读裸 JSON (不含信封). */
function getRaw<T>(path: string, signal?: AbortSignal): Promise<Result<T>> {
	return getRawCompat<T>(path, signal);
}

/** 浏览器侧: 环境列表, 取 data.list. */
export const listEnvs = keyed<Env[]>(KEYS.envs, 4000, (signal) =>
	getEnvelope<EnvPage>(API + "/environments?page_size=1000", signal).then((r): Result<Env[]> => (r.ok ? { ok: true, data: r.data?.list ?? [] } : r)),
);

/** 浏览器侧: 当前调用方可读的全部分组与文件夹. */
export const listFolders = keyed<FolderMap>(KEYS.folders, 4000, (signal) => getEnvelope<FolderMap>(API + "/folders", signal));

/** 缓存代理侧: 引擎与存储统计, 8325 原样转发 8898 的裸 JSON, 不包信封. */
export const cacheStats = keyed<CacheStats>(KEYS.cache, 4000, (signal) => getRaw<CacheStats>(API + "/cache/stats", signal));

/** 指纹自检结论: 读最新一份 fingerprint-audit 报告. */
export const fingerprintVerdict = keyed<VerdictReport>(KEYS.verdict, 25000, (signal) => getEnvelope<VerdictReport>(API + "/fingerprint/verdict", signal));

/** 8325 服务状态: 版本, 端口, 运行中实例数. */
export type ServiceStatus = { status: string; version: string; api_port: number; cdp_base_port: number; running: number };

export const serviceStatus = keyed<ServiceStatus>(KEYS.status, 4000, (signal) => getEnvelope<ServiceStatus>(API + "/status", signal));

/** 单个环境的最新视图 (与列表行同形), 详情面板按它轮询. */
export function envStatus(id: string, signal?: AbortSignal): Promise<Result<Env>> {
	return cachedRead(KEYS.env(id) + ":status", 4000, (s) => getEnvelope<Env>(API + "/environments/" + encodeURIComponent(id) + "/status", s), signal);
}

/** 环境的缓存挂载实况: running 带 pid 与 endpoint, absent 未挂, dead 进程已死. */
export type HxcpStatus = { status: "running" | "absent" | "dead" | string; pid: number; endpoint?: string };

export function hxcpStatus(id: string, signal?: AbortSignal): Promise<Result<HxcpStatus>> {
	return cachedRead(KEYS.env(id) + ":hxcp", 4000, (s) => getEnvelope<HxcpStatus>(API + "/environments/" + encodeURIComponent(id) + "/hxcp/status", s), signal);
}

/** 已保存的代理 (资源页的代理库), 带上次检测的国家与延迟. */
export type SavedProxy = { id: string; label: string; type: string; host: string; port: number; country?: string; latency_ms?: number; checked_at?: number };

export const listProxies = keyed<SavedProxy[]>(KEYS.proxies, 10000, (signal) =>
	getEnvelope<{ list: SavedProxy[] }>(API + "/proxies", signal).then((r): Result<SavedProxy[]> => (r.ok ? { ok: true, data: r.data?.list ?? [] } : r)),
);
