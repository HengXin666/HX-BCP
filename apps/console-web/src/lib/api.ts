/** 控制台的后端读接口, 全部走 8325 的 /api/v1 (缓存统计由 8325 转 8898). 写接口在 api-write.ts. */

/**
 * 8325 的接口前缀, 相对页面所在目录解析: 直连是 /api/v1, 经 HX-WebX 打开是 /proxy/hx-browser/api/v1.
 * 不能写成以 / 开头, 否则子路径下会打到面板根.
 */
export const API = "api/v1";

/** 页面所在目录的路径, 例如 / 或 /proxy/hx-browser/, 后端据此给会话 cookie 定 Path. */
export const PAGE_BASE = new URL(".", window.location.href).pathname;

/** 每个请求都声明人类通道; 身份凭证是 8325 设的 HttpOnly 会话 cookie 或 dev 代理注入的令牌, 脚本看不到. */
export const HUMAN_HEADERS = { Accept: "application/json", "x-bcp-lane": "human" } as const;

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

/** 统一结果: 服务未启动或业务失败时返回 ok=false 而不是抛异常, 页面显示"未连接". */
export type Result<T> = { ok: true; data: T } | { ok: false; error: string };

export type Envelope<T> = { code?: number | string; msg?: string; data?: T };

/** HTTP 失败时的说明: 优先取后端信封里的 code 与 msg (如 400 project_required, 401 human_token_required). */
export async function errorText(res: Response): Promise<string> {
	try {
		const body = (await res.json()) as Envelope<unknown>;
		if (body.msg) return "HTTP " + res.status + " " + String(body.code ?? "") + " " + body.msg;
	} catch {
		// 非 JSON 响应只报状态码
	}
	return "HTTP " + res.status;
}

/** 读 JSON 原样返回, 只处理网络与 HTTP 状态. */
async function getRaw<T>(path: string, signal?: AbortSignal): Promise<Result<T>> {
	try {
		const res = await fetch(path, { signal, headers: HUMAN_HEADERS });
		if (!res.ok) return { ok: false, error: await errorText(res) };
		return { ok: true, data: (await res.json()) as T };
	} catch (err) {
		return { ok: false, error: String((err as Error)?.message ?? err) };
	}
}

/** 读 8325 的信封 {code,msg,data}, code 不为 0 时返回 ok=false 与 msg. */
export async function getEnvelope<T>(path: string, signal?: AbortSignal): Promise<Result<T>> {
	const r = await getRaw<Envelope<T>>(path, signal);
	if (!r.ok) return r;
	const body = r.data;
	if (body.code !== 0) return { ok: false, error: body.msg ?? "code " + String(body.code) };
	return { ok: true, data: body.data as T };
}

/** 浏览器侧: 环境列表, 取 data.list. */
export function listEnvs(signal?: AbortSignal): Promise<Result<Env[]>> {
	return getEnvelope<EnvPage>(API + "/environments?page_size=1000", signal).then((r) =>
		r.ok ? { ok: true, data: r.data?.list ?? [] } : r,
	);
}

/** 浏览器侧: 当前调用方可读的全部分组与文件夹. */
export function listFolders(signal?: AbortSignal): Promise<Result<FolderMap>> {
	return getEnvelope<FolderMap>(API + "/folders", signal);
}

/** 缓存代理侧: 引擎与存储统计, 8325 原样转发 8898 的裸 JSON, 不包信封. */
export function cacheStats(signal?: AbortSignal): Promise<Result<CacheStats>> {
	return getRaw<CacheStats>(API + "/cache/stats", signal);
}

/** 指纹自检结论: 读最新一份 fingerprint-audit 报告. */
export function fingerprintVerdict(signal?: AbortSignal): Promise<Result<VerdictReport>> {
	return getEnvelope<VerdictReport>(API + "/fingerprint/verdict", signal);
}

/** 8325 服务状态: 版本, 端口, 运行中实例数. */
export type ServiceStatus = { status: string; version: string; api_port: number; cdp_base_port: number; running: number };

export function serviceStatus(signal?: AbortSignal): Promise<Result<ServiceStatus>> {
	return getEnvelope<ServiceStatus>(API + "/status", signal);
}

/** 单个环境的最新视图 (与列表行同形), 详情面板按它轮询. */
export function envStatus(id: string, signal?: AbortSignal): Promise<Result<Env>> {
	return getEnvelope<Env>(API + "/environments/" + encodeURIComponent(id) + "/status", signal);
}

/** 环境的缓存挂载实况: running 带 pid 与 endpoint, absent 未挂, dead 进程已死. */
export type HxcpStatus = { status: "running" | "absent" | "dead" | string; pid: number; endpoint?: string };

export function hxcpStatus(id: string, signal?: AbortSignal): Promise<Result<HxcpStatus>> {
	return getEnvelope<HxcpStatus>(API + "/environments/" + encodeURIComponent(id) + "/hxcp/status", signal);
}

/** 已保存的代理 (资源页的代理库), 带上次检测的国家与延迟. */
export type SavedProxy = { id: string; label: string; type: string; host: string; port: number; country?: string; latency_ms?: number; checked_at?: number };

export function listProxies(signal?: AbortSignal): Promise<Result<SavedProxy[]>> {
	return getEnvelope<{ list: SavedProxy[] }>(API + "/proxies", signal).then((r) => (r.ok ? { ok: true, data: r.data?.list ?? [] } : r));
}
