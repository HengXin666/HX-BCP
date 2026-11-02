/**
 * 链路判定: 一个环境的四段 (身份 -> 缓存 -> 出口 -> 目标) 各自是什么状态.
 * 只读后端已上报的字段, 拿不到的段写 "未上报", 不推测. 架构见 docs/architecture/overview.md.
 */
import type { CacheStats, Env, EnvProxy } from "./api";
import type { ExitCheck } from "./api-write";
import { isRunning } from "./env-query";

/** ok 通, warn 已配置但未确认, bad 故障, off 未挂载, idle 环境未运行, na 后端未上报. */
export type SegState = "ok" | "warn" | "bad" | "off" | "idle" | "na";

export type SegKey = "env" | "cache" | "exit" | "target";

export type Seg = { key: SegKey; name: string; state: SegState; value: string };

export const STATE_TEXT: Record<SegState, string> = {
	ok: "正常",
	warn: "待确认",
	bad: "故障",
	off: "未挂载",
	idle: "未运行",
	na: "未上报",
};

export const SEG_NAME: Record<SegKey, string> = { env: "浏览器", cache: "缓存", exit: "出口", target: "目标" };

/** 每段换掉或停掉会影响什么, 依据: 身份与出口分离, 缓存只对静态资源生效, 运行中改配置需重启 (pending_restart). */
export const SEG_IMPACT: Record<SegKey, string> = {
	env: "改指纹只改变 \"看起来像谁\", 不换出口; 运行中修改要重启才生效.",
	cache: "停掉只少了省流, 指纹与出口不变; 缓存在浏览器层 (CDP Fetch), 不终结 TLS.",
	exit: "换出口会换 IP; 位置模式为 match_ip 时, 下次检测会按新 IP 对齐时区与语言, 运行中要重启.",
	target: "目标站点由页面决定, 控制台不改它.",
};

const UPSTREAM_TYPES = new Set(["http", "https", "socks5"]);

/** 环境是否挂了上游代理 (http, https, socks5 且有主机). system 走系统代理, 另算. */
export function hasUpstream(p: EnvProxy | undefined): boolean {
	return !!p && UPSTREAM_TYPES.has(p.type ?? "") && !!p.host;
}

/** 上游的唯一键, 不含凭据: type://host:port. 没挂上游返回空串. */
export function upstreamKey(p: EnvProxy | undefined): string {
	return hasUpstream(p) ? (p?.type ?? "") + "://" + (p?.host ?? "") + ":" + String(p?.port ?? 0) : "";
}

/** 运行时里的缓存挂载状态 (runtime.hxcp_status), 环境未运行时为空串. */
export function hxcpOf(env: Env): string {
	const v = env.runtime?.hxcp_status;
	return typeof v === "string" ? v : "";
}

function envSeg(env: Env): Seg {
	const fp = env.fingerprint ?? {};
	const value = [fp.kernel || fp.browser, fp.os, fp.timezone].filter(Boolean).join(" / ") || "指纹未上报";
	return { key: "env", name: SEG_NAME.env, state: isRunning(env) || env.in_use ? "ok" : "idle", value };
}

function cacheSeg(env: Env): Seg {
	const s = hxcpOf(env);
	const base = { key: "cache" as const, name: SEG_NAME.cache };
	if (s === "running") return { ...base, state: "ok", value: "已挂载, 引擎运行中" };
	if (s === "failed" || s === "dead") return { ...base, state: "bad", value: "挂载失败 (" + s + ")" };
	if (!env.hxcp_enabled) return { ...base, state: "off", value: "未挂载" };
	if (!isRunning(env)) return { ...base, state: "idle", value: "已配置, 环境未运行" };
	return { ...base, state: "warn", value: "已配置, 引擎未上报状态" };
}

function exitSeg(env: Env, check: ExitCheck | undefined): Seg {
	const p = env.proxy;
	const base = { key: "exit" as const, name: SEG_NAME.exit };
	if (p?.type === "system") return { ...base, state: "warn", value: "系统代理 (控制台看不到地址)" };
	if (!hasUpstream(p)) return { ...base, state: "off", value: "未挂载上游, 直连出网" };
	const where = upstreamKey(p);
	if (check) return check.ok ? { ...base, state: "ok", value: where + " -> " + (check.ip ?? "") } : { ...base, state: "bad", value: where + " 不通" };
	const ip = env.last_region?.ip;
	return { ...base, state: "warn", value: where + (ip ? ", 上次出口 " + ip : ", 未检测") };
}

function targetSeg(env: Env): Seg {
	const url = env.launch?.urls?.[0];
	return { key: "target", name: SEG_NAME.target, state: "na", value: url ? "启动页 " + url + ", 当前页面未上报" : "当前页面未上报" };
}

/** 一个环境的四段链路. check 是本页做过的出口检测结果 (没测过为 undefined). */
export function chainOf(env: Env, check?: ExitCheck): Seg[] {
	return [envSeg(env), cacheSeg(env), exitSeg(env, check), targetSeg(env)];
}

/** 命中率 0..1; 引擎没上报计数时返回 null (不当成 0). */
export function hitRate(s: CacheStats | undefined): number | null {
	const e = s?.engine;
	if (!e) return null;
	const total = (e.hits ?? 0) + (e.misses ?? 0);
	return total ? (e.hits ?? 0) / total : null;
}

const UNITS = ["B", "KB", "MB", "GB", "TB"];
const K = 1024;

/** 字节数转可读文本, 缺值写未上报. */
export function fmtBytes(n: number | undefined | null): string {
	if (n === undefined || n === null || n < 0) return "未上报";
	let v = n;
	let i = 0;
	while (v >= K && i < UNITS.length - 1) {
		v /= K;
		i += 1;
	}
	return (i ? v.toFixed(1) : String(v)) + " " + UNITS[i];
}

/** 0..1 转百分比, null 写未上报. */
export function fmtPct(r: number | null | undefined): string {
	return r === null || r === undefined ? "未上报" : (r * 100).toFixed(1) + "%";
}

/** 全部环境的链路分布: 挂缓存, 挂上游, 直连, 以及每个上游被多少环境使用. */
export function distribution(envs: Env[]): { total: number; cache: number; upstream: number; direct: number; ups: Map<string, { proxy: EnvProxy; n: number }> } {
	const ups = new Map<string, { proxy: EnvProxy; n: number }>();
	let cache = 0;
	let direct = 0;
	for (const e of envs) {
		if (e.hxcp_enabled || hxcpOf(e) === "running") cache += 1;
		const k = upstreamKey(e.proxy);
		if (!k) {
			if (e.proxy?.type !== "system") direct += 1;
			continue;
		}
		const cur = ups.get(k);
		ups.set(k, { proxy: cur?.proxy ?? e.proxy ?? null, n: (cur?.n ?? 0) + 1 });
	}
	let upstream = 0;
	for (const v of ups.values()) upstream += v.n;
	return { total: envs.length, cache, upstream, direct, ups };
}

/** 环境的出口状态 (供列表筛选): 没挂上游 = direct; 挂了但还没检测结果 = unchecked; 否则按最近一次检测 ok 或 bad. */
export function exitStateOf(env: Env, results: Record<string, ExitCheck>): "ok" | "bad" | "unchecked" | "direct" {
	const k = upstreamKey(env.proxy);
	if (!k) return "direct";
	const r = results[k];
	return !r ? "unchecked" : r.ok ? "ok" : "bad";
}
