/** 控制台的写接口: 只作用于人类组. api 组的浏览器由后端按通道拒绝写入, 判据在 packages/lane. */
import { API, errorText, HUMAN_HEADERS, type Envelope, type Result } from "./api";

/** 发一次 JSON 写请求, HTTP 失败或信封 code 不为 0 都返回 ok=false. */
async function send<T>(method: string, path: string, body?: unknown): Promise<Result<T>> {
	try {
		const res = await fetch(API + path, {
			method,
			headers: { ...HUMAN_HEADERS, "Content-Type": "application/json" },
			body: body === undefined ? undefined : JSON.stringify(body),
		});
		if (!res.ok) return { ok: false, error: await errorText(res) };
		const env = (await res.json()) as Envelope<T>;
		if (env.code !== 0) return { ok: false, error: env.msg ?? "code " + String(env.code) };
		return { ok: true, data: env.data as T };
	} catch (err) {
		return { ok: false, error: String((err as Error)?.message ?? err) };
	}
}


const enc = encodeURIComponent;

/** 浏览器写操作. 删除是移入最近删除, 可恢复. */
export const envApi = {
	/** 新建与编辑的请求体由 lib/fp-form.ts 的 bodyOf 生成: 留空的字段不发, 后端补默认; 指纹段由后端按字段合并. */
	create: (body: Record<string, unknown>) => send<{ id: string }>("POST", "/environments", body),
	update: (id: string, body: Record<string, unknown>) => send("PUT", "/environments/" + enc(id), body),
	setKernel: (id: string, fingerprint: { kernel: string; browser: string }) => send("PUT", "/environments/" + enc(id), { fingerprint }),
	rename: (id: string, name: string) => send("PUT", "/environments/" + enc(id), { name }),
	move: (id: string, folder: string) => send("PUT", "/environments/" + enc(id), { folder }),
	remove: (id: string) => send("DELETE", "/environments/" + enc(id)),
	start: (id: string) => send("POST", "/environments/" + enc(id) + "/start"),
	stop: (id: string) => send("POST", "/environments/" + enc(id) + "/stop"),
	setTags: (id: string, tags: string[]) => send("PUT", "/environments/" + enc(id), { tags }),
};

export type BatchOutcome = { done: number; failed: { id: string; error: string }[] };
export type BatchOp = "start" | "stop" | "move" | "tags" | "delete";

/**
 * 批量操作走一次 POST /environments/batch (body 带 action, ids, 移动给 folder, 加标签给 tags), 后端逐个调单条处理器 (并发 4)
 * 并按 id 回报成败. 后端旧版本只认批量新建, 对带 action 的 body 会回 {count: 0, ids: []} 且 code 0: 这种回包按整批失败处理,
 * 提示重启服务, 绝不当成功.
 */
export async function batchOp(action: BatchOp, ids: string[], extra: { folder?: string; tags?: string[] } = {}): Promise<Result<BatchOutcome & { results: { id: string; ok: boolean; error: string }[] }>> {
	const r = await send<{ action?: string; done: number; failed: { id: string; error: string }[]; results: { id: string; ok: boolean; error: string }[] }>("POST", "/environments/batch", { action, ids, ...extra });
	if (!r.ok) return r;
	if (r.data?.action !== action) return { ok: false, error: "后端 /environments/batch 还是旧版 (只批量新建), 重启 8325 后再试" };
	return r as Result<BatchOutcome & { results: { id: string; ok: boolean; error: string }[] }>;
}

/**
 * 对多个浏览器逐个执行同一写操作, 最多 4 个并发. 后端没有批量启停与删除接口
 * (POST /environments/batch 是批量新建), 所以批量条在前端循环, 每个请求仍过通道与归属判定.
 */
export async function runBatch(ids: string[], call: (id: string) => Promise<Result<unknown>>): Promise<BatchOutcome> {
	const out: BatchOutcome = { done: 0, failed: [] };
	const queue = [...ids];
	const worker = async (): Promise<void> => {
		for (let id = queue.shift(); id !== undefined; id = queue.shift()) {
			const r = await call(id);
			if (r.ok) out.done += 1;
			else out.failed.push({ id, error: r.error });
		}
	};
	await Promise.all(Array.from({ length: Math.min(4, ids.length) }, worker));
	return out;
}

/** 文件夹写操作, 只作用于调用方自己的分组 (控制台即人类组). */
export const folderApi = {
	create: (name: string) => send<{ name: string }>("POST", "/folders", { name }),
	rename: (from: string, to: string) => send<{ name: string }>("PUT", "/folders/" + enc(from), { name: to }),
	remove: (name: string) => send("DELETE", "/folders/" + enc(name)),
};

/** POST /proxies/check 的结果: ok 时有出口 IP, 国家, 城市, 运营商, 延迟; 失败时有 error. 字段见后端 proxy.check_proxy. */
export type ExitCheck = { ok: boolean; ip?: string; country?: string; region?: string; city?: string; isp?: string; asn?: string; latency_ms?: number; error?: string };

/** 用环境自己的代理配置探一次出口 (经后端本地 relay, 与浏览器走同一条路). 只读探测, 不改任何环境. */
export function checkExit(proxy: { type?: string; host?: string; port?: number; username?: string; password?: string }): Promise<Result<ExitCheck>> {
	return send<ExitCheck>("POST", "/proxies/check", {
		type: proxy.type ?? "http",
		host: proxy.host ?? "",
		port: proxy.port ?? 0,
		username: proxy.username ?? "",
		password: proxy.password ?? "",
	});
}
