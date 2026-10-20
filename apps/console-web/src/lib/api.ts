/** 控制台的后端访问: 浏览器环境 (8325) 与缓存代理 (8898) 两个只读视图. */

export type Env = { name: string; seed?: string; running?: boolean; proxy?: string };

export type CacheStats = {
	hits?: number;
	misses?: number;
	bytesSaved?: number;
	entries?: number;
};

export type Verdict = "green" | "yellow" | "red" | "unknown";

/** 统一结果: 服务未启动时返回 ok=false 而不是抛异常, 页面显示"未连接". */
export type Result<T> = { ok: true; data: T } | { ok: false; error: string };

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<Result<T>> {
	try {
		const res = await fetch(path, { signal, headers: { Accept: "application/json" } });
		if (!res.ok) return { ok: false, error: "HTTP " + res.status };
		return { ok: true, data: (await res.json()) as T };
	} catch (err) {
		return { ok: false, error: String((err as Error)?.message ?? err) };
	}
}

/** 浏览器侧: 环境列表. */
export function listEnvs(signal?: AbortSignal): Promise<Result<Env[]>> {
	return getJSON<{ environments?: Env[] }>("/api/browser/environments", signal).then((r) =>
		r.ok ? { ok: true, data: r.data.environments ?? [] } : r,
	);
}

/** 缓存代理侧: 命中与省流统计. */
export function cacheStats(signal?: AbortSignal): Promise<Result<CacheStats>> {
	return getJSON<CacheStats>("/api/cache/stats", signal);
}

/** 指纹自检结论: 由 packages/fingerprint-audit 写入, 控制台只展示. */
export function fingerprintVerdict(signal?: AbortSignal): Promise<Result<{ verdict: Verdict; checked?: number }>> {
	return getJSON<{ verdict: Verdict; checked?: number }>("/api/browser/fingerprint/verdict", signal);
}
