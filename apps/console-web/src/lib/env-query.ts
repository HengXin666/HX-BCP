/** 浏览器表的搜索, 筛选, 排序, 分页: 纯函数, 输入环境数组与条件, 输出当前页. 条件与 hash query 互转. */
import type { Env } from "./api";

/** stopped = 打开过, 现在没在跑; never = 从未打开 (last_open_at 为空). */
export type StatusFilter = "all" | "running" | "stopped" | "never" | "busy" | "restart";
/** 出口筛选: bad = 挂了上游且最近一次检测不通, ok = 通, unchecked = 挂了上游但还没测, direct = 没挂上游 (直连或系统代理). */
export type ExitFilter = "all" | "bad" | "ok" | "unchecked" | "direct";
export type ExitState = Exclude<ExitFilter, "all">;
export type SortKey = "last" | "name" | "opens" | "created";
export type SortDir = "asc" | "desc";

export type EnvQuery = {
	q: string;
	status: StatusFilter;
	tag: string;
	/** 内核筛选: 空 = 全部, fam:blink 或 fam:gecko = 整族, inherit = 未指定内核的旧环境, 其余 = 内核 id. */
	kernel: string;
	exit: ExitFilter;
	sort: SortKey;
	dir: SortDir;
	page: number;
	size: number;
};

export const PAGE_SIZES = [25, 50, 100] as const;
const STATUSES: StatusFilter[] = ["all", "running", "stopped", "never", "busy", "restart"];
const EXITS: ExitFilter[] = ["all", "bad", "ok", "unchecked", "direct"];
const SORTS: SortKey[] = ["last", "name", "opens", "created"];

export const DEFAULT_QUERY: EnvQuery = { q: "", status: "all", tag: "", kernel: "", exit: "all", sort: "last", dir: "desc", page: 1, size: 50 };

/** 运行态: runtime 为 null 或缺省表示未启动. */
export function isRunning(env: Env): boolean {
	return env.runtime !== null && env.runtime !== undefined;
}

/** 从 hash query 读条件, 非法值回默认. */
export function readQuery(p: URLSearchParams): EnvQuery {
	const pick = <T extends string>(v: string | null, all: T[], d: T): T => (all.includes(v as T) ? (v as T) : d);
	const size = Number(p.get("size"));
	const page = Number(p.get("page"));
	return {
		q: p.get("q") ?? "",
		status: pick(p.get("status"), STATUSES, DEFAULT_QUERY.status),
		tag: p.get("tag") ?? "",
		kernel: p.get("kernel") ?? "",
		exit: pick(p.get("exit"), EXITS, DEFAULT_QUERY.exit),
		sort: pick(p.get("sort"), SORTS, DEFAULT_QUERY.sort),
		dir: p.get("dir") === "asc" ? "asc" : "desc",
		page: Number.isInteger(page) && page > 0 ? page : 1,
		size: (PAGE_SIZES as readonly number[]).includes(size) ? size : DEFAULT_QUERY.size,
	};
}

/** 条件写回 hash query, 与默认值相同的项省略. */
export function writeQuery(q: EnvQuery): Record<string, string> {
	const out: Record<string, string> = {};
	for (const key of Object.keys(q) as (keyof EnvQuery)[]) {
		if (q[key] !== DEFAULT_QUERY[key]) out[key] = String(q[key]);
	}
	return out;
}

/** 一个环境的可搜索文本: 名称, id, 代理主机, 标签, 文件夹, 项目. */
function haystack(env: Env): string {
	const p = env.proxy;
	return [env.name, env.id, p?.host ?? "", ...(env.tags ?? []), env.folder, env.project].join(" ").toLowerCase();
}

export function matchStatus(env: Env, s: StatusFilter): boolean {
	if (s === "running") return isRunning(env);
	if (s === "stopped") return !isRunning(env) && Boolean(env.last_open_at);
	if (s === "never") return !isRunning(env) && !env.last_open_at;
	if (s === "busy") return env.in_use;
	if (s === "restart") return Boolean(env.pending_restart);
	return true;
}

function sortValue(env: Env, key: SortKey): number | string {
	if (key === "name") return env.name.toLowerCase();
	if (key === "opens") return env.open_count ?? 0;
	if (key === "created") return env.created_at ?? 0;
	return env.last_open_at ?? 0;
}

/** 当前范围内全部标签, 按出现次数降序. */
export function tagsOf(envs: Env[]): string[] {
	const count = new Map<string, number>();
	for (const e of envs) for (const t of e.tags ?? []) count.set(t, (count.get(t) ?? 0) + 1);
	return [...count.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([t]) => t);
}

/** 内核筛选用到的三项, 由 lib/kernels.kernelView 给出 (这里不依赖它, 免得循环引用). */
export type KernelKey = { id: string; family: string; how: string };

function matchKernel(k: KernelKey | null, want: string): boolean {
	if (!want || !k) return true;
	if (want.startsWith("fam:")) return k.family === want.slice(4);
	if (want === "inherit") return k.how === "继承";
	return k.id === want;
}

export type QueryResult = { rows: Env[]; total: number; page: number; pageCount: number };

/** 过滤, 排序, 截取当前页; 页码越界时夹到最后一页. */
export function applyQuery(envs: Env[], q: EnvQuery, kernelOf?: (env: Env) => KernelKey, exitOf?: (env: Env) => ExitState): QueryResult {
	const words = q.q.trim().toLowerCase().split(/\s+/).filter(Boolean);
	const hit = envs.filter(
		(e) =>
			matchStatus(e, q.status) &&
			matchKernel(kernelOf ? kernelOf(e) : null, q.kernel) &&
			(q.exit === "all" || !exitOf || exitOf(e) === q.exit) &&
			(q.tag === "" || (e.tags ?? []).includes(q.tag)) &&
			(words.length === 0 || words.every((w) => haystack(e).includes(w))),
	);
	const sign = q.dir === "asc" ? 1 : -1;
	hit.sort((a, b) => {
		const x = sortValue(a, q.sort);
		const y = sortValue(b, q.sort);
		if (x === y) return a.name.localeCompare(b.name);
		return (x < y ? -1 : 1) * sign;
	});
	const pageCount = Math.max(1, Math.ceil(hit.length / q.size));
	const page = Math.min(q.page, pageCount);
	return { rows: hit.slice((page - 1) * q.size, page * q.size), total: hit.length, page, pageCount };
}
