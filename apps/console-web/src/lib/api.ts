/** 控制台的后端访问: 浏览器环境 (8325) 与缓存代理 (8898) 两个只读视图. */

export type EnvProxy = { type?: string; host?: string; port?: number } | null;

export type Env = {
	id: string;
	name: string;
	group?: string;
	platform?: string;
	proxy?: EnvProxy;
	fingerprint?: { browser?: string; kernel?: string } | null;
	runtime?: Record<string, unknown> | null;
	visible?: boolean;
	visible_reason?: string;
	open_count?: number;
	pending_restart?: boolean;
};

export type EnvPage = { list: Env[]; total: number; page: number; page_size: number };

export type CacheStats = {
	engine?: { hits?: number; misses?: number; passed?: number; errors?: number };
	engine_state?: string;
	store?: { entries?: number; bytes?: number; hits?: number; bytes_served?: number };
	flows?: { records?: number };
	events?: number;
};

export type Verdict = "green" | "yellow" | "red" | "unknown";

export type VerdictReport = { verdict: Verdict; checked: number; at: string; evidence: unknown[] };

/** 统一结果: 服务未启动或业务失败时返回 ok=false 而不是抛异常, 页面显示"未连接". */
export type Result<T> = { ok: true; data: T } | { ok: false; error: string };

type Envelope<T> = { code?: number; msg?: string; data?: T };

/** 读 JSON 原样返回, 只处理网络与 HTTP 状态. */
async function getRaw<T>(path: string, signal?: AbortSignal): Promise<Result<T>> {
	try {
		const res = await fetch(path, { signal, headers: { Accept: "application/json" } });
		if (!res.ok) return { ok: false, error: "HTTP " + res.status };
		return { ok: true, data: (await res.json()) as T };
	} catch (err) {
		return { ok: false, error: String((err as Error)?.message ?? err) };
	}
}

/** 读 8325 的信封 {code,msg,data}, code 不为 0 时返回 ok=false 与 msg. */
async function getEnvelope<T>(path: string, signal?: AbortSignal): Promise<Result<T>> {
	const r = await getRaw<Envelope<T>>(path, signal);
	if (!r.ok) return r;
	const body = r.data;
	if (body.code !== 0) return { ok: false, error: body.msg ?? "code " + String(body.code) };
	return { ok: true, data: body.data as T };
}

/** 浏览器侧: 环境列表, 取 data.list. */
export function listEnvs(signal?: AbortSignal): Promise<Result<Env[]>> {
	return getEnvelope<EnvPage>("/api/browser/environments", signal).then((r) =>
		r.ok ? { ok: true, data: r.data?.list ?? [] } : r,
	);
}

/** 缓存代理侧: 引擎与存储统计, 8898 不包信封. */
export function cacheStats(signal?: AbortSignal): Promise<Result<CacheStats>> {
	return getRaw<CacheStats>("/api/cache/stats", signal);
}

/** 指纹自检结论: 读最新一份 fingerprint-audit 报告. */
export function fingerprintVerdict(signal?: AbortSignal): Promise<Result<VerdictReport>> {
	return getEnvelope<VerdictReport>("/api/browser/fingerprint/verdict", signal);
}
