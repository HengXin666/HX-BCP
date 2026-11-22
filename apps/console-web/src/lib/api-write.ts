/** 控制台的写接口: 只作用于人类组. api 组的浏览器由后端按通道拒绝写入, 判据在 packages/lane. */
import { type Result } from "./api";
import { sendCompat } from "./http";
import { KEYS, invalidate } from "./cache";

/** 发一次 JSON 写请求, 统一走 http.ts (超时, 统一错误对象, 请求取消). 写请求默认不重试. */
function send<T>(method: string, path: string, body?: unknown): Promise<Result<T>> {
	return sendCompat<T>(method, path, body);
}

/** 写成功后让相关读缓存作废, 订阅的 usePoll 立即重拉. 失败不动缓存. */
async function write<T>(p: Promise<Result<T>>, ...keys: string[]): Promise<Result<T>> {
	const r = await p;
	if (r.ok) invalidate(...keys);
	return r;
}


const enc = encodeURIComponent;

/** 浏览器写操作. 删除是移入最近删除, 可恢复. */
export const envApi = {
	/** 新建与编辑的请求体由 lib/fp-form.ts 的 bodyOf 生成: 留空的字段不发, 后端补默认; 指纹段由后端按字段合并. */
	create: (body: Record<string, unknown>) => write(send<{ id: string }>("POST", "/environments", body), KEYS.envs, KEYS.folders),
	update: (id: string, body: Record<string, unknown>) => write(send("PUT", "/environments/" + enc(id), body), KEYS.envs, KEYS.env(id)),
	setKernel: (id: string, fingerprint: { kernel: string; browser: string }) => write(send("PUT", "/environments/" + enc(id), { fingerprint }), KEYS.envs, KEYS.env(id)),
	rename: (id: string, name: string) => write(send("PUT", "/environments/" + enc(id), { name }), KEYS.envs, KEYS.env(id)),
	move: (id: string, folder: string) => write(send("PUT", "/environments/" + enc(id), { folder }), KEYS.envs, KEYS.folders, KEYS.env(id)),
	remove: (id: string) => write(send("DELETE", "/environments/" + enc(id)), KEYS.envs, KEYS.folders, KEYS.env(id)),
	start: (id: string) => write(send("POST", "/environments/" + enc(id) + "/start"), KEYS.envs, KEYS.env(id), KEYS.login),
	stop: (id: string) => write(send("POST", "/environments/" + enc(id) + "/stop"), KEYS.envs, KEYS.env(id), KEYS.login),
	setTags: (id: string, tags: string[]) => write(send("PUT", "/environments/" + enc(id), { tags }), KEYS.envs, KEYS.env(id)),
};

export type BatchOutcome = { done: number; failed: { id: string; error: string }[] };
export type BatchOp = "start" | "stop" | "move" | "tags" | "delete";

/**
 * 批量操作走一次 POST /environments/batch (body 带 action, ids, 移动给 folder, 加标签给 tags), 后端逐个调单条处理器 (并发 4)
 * 并按 id 回报成败. 后端旧版本只认批量新建, 对带 action 的 body 会回 {count: 0, ids: []} 且 code 0: 这种回包按整批失败处理,
 * 提示重启服务, 绝不当成功.
 */
export type BatchItem = { id: string; ok: boolean; error: string };
export type BatchReply = BatchOutcome & { action?: string; results: BatchItem[] };

export async function batchOp(action: BatchOp, ids: string[], extra: { folder?: string; tags?: string[] } = {}): Promise<Result<BatchReply>> {
	const r = await send<BatchReply>("POST", "/environments/batch", { action, ids, ...extra });
	if (!r.ok) return r;
	if (r.data?.action !== action) return { ok: false, error: "后端 /environments/batch 还是旧版 (只批量新建), 重启 8325 后再试" };
	invalidate(KEYS.envs, KEYS.folders, KEYS.login);
	return r;
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
	create: (name: string) => write(send<{ name: string }>("POST", "/folders", { name }), KEYS.folders),
	rename: (from: string, to: string) => write(send<{ name: string }>("PUT", "/folders/" + enc(from), { name: to }), KEYS.folders, KEYS.envs),
	remove: (name: string) => write(send("DELETE", "/folders/" + enc(name)), KEYS.folders, KEYS.envs),
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
