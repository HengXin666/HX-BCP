/**
 * 会话级读缓存: 整个控制台共用一份 (模块级), 刷新页面即清空. 同一键的并发读只发一次请求, 新鲜期内切页直接复用.
 * 失效按写操作来: 写接口成功后调用 invalidate(前缀), 命中的键作废并通知订阅者 (usePoll) 立刻重拉.
 * 只缓存成功结果; 失败不进缓存, 下次照常请求.
 */
import type { Result } from "./http";

type Entry = { data: unknown; at: number };

const store = new Map<string, Entry>();
const inflight = new Map<string, Promise<Result<unknown>>>();
const subs = new Map<string, Set<() => void>>();
const patchSubs = new Map<string, Set<() => void>>();

/** 读一个键的缓存与年龄 (毫秒); 没有返回 null. */
export function peek<T>(key: string): { data: T; age: number } | null {
	const e = store.get(key);
	return e ? { data: e.data as T, age: Date.now() - e.at } : null;
}

/**
 * 带缓存地读: 新鲜期 (freshMs) 内直接返回缓存, 否则发请求; 同键请求在飞时复用同一个 Promise.
 * 首个调用方的 signal 会传给请求; 它取消时同键等待者拿到 abort 失败, 不进缓存, 下次轮询再拉.
 */
export function cachedRead<T>(key: string, freshMs: number, load: (signal?: AbortSignal) => Promise<Result<T>>, signal?: AbortSignal): Promise<Result<T>> {
	const hit = peek<T>(key);
	if (hit && hit.age < freshMs) return Promise.resolve({ ok: true, data: hit.data });
	const running = inflight.get(key) as Promise<Result<T>> | undefined;
	if (running) return running;
	const p = load(signal).then((r) => {
		if (r.ok) store.set(key, { data: r.data, at: Date.now() });
		return r;
	});
	inflight.set(key, p as Promise<Result<unknown>>);
	void p.finally(() => {
		if (inflight.get(key) === p) inflight.delete(key);
	});
	return p;
}

/** 带缓存键的读函数: usePoll 据 cacheKey 在挂载时先用缓存出数, 并订阅失效. */
export type KeyedLoader<T> = ((signal?: AbortSignal) => Promise<Result<T>>) & { cacheKey?: string };

/** 把一个无参读接口包成带缓存的读函数, 签名不变, 页面无需改动. */
export function keyed<T>(key: string, freshMs: number, load: (signal?: AbortSignal) => Promise<Result<T>>): KeyedLoader<T> {
	const fn = ((signal?: AbortSignal) => cachedRead(key, freshMs, load, signal)) as KeyedLoader<T>;
	fn.cacheKey = key;
	return fn;
}

/** 让以 prefix 开头的键全部作废并通知订阅者. prefix 为空串时清空全部. */
export function invalidate(...prefixes: string[]): void {
	const hit = new Set<string>();
	for (const k of [...store.keys(), ...subs.keys()]) {
		if (prefixes.some((p) => k === p || k.startsWith(p + ":") || p === "")) hit.add(k);
	}
	for (const k of hit) {
		store.delete(k);
		inflight.delete(k);
	}
	for (const k of hit) for (const f of subs.get(k) ?? []) f();
}

/** 订阅某键的失效; 返回退订函数. */
export function onInvalidate(key: string, f: () => void): () => void {
	let set = subs.get(key);
	if (!set) {
		set = new Set();
		subs.set(key, set);
	}
	set.add(f);
	return () => {
		set?.delete(f);
		if (set && set.size === 0) subs.delete(key);
	};
}

/**
 * 乐观更新的底层: 就地改一个键的缓存数据 (新鲜期不变) 并通知 onPatch 订阅者, 不触发重拉.
 * 返回撤销函数, 把该键恢复成改之前的那份并再通知一次. 键不在缓存里时什么都不改, 撤销也是空操作.
 */
export function patch<T>(key: string, fn: (cur: T) => T): () => void {
	const prev = store.get(key);
	if (!prev) return () => undefined;
	store.set(key, { data: fn(prev.data as T), at: prev.at });
	for (const f of patchSubs.get(key) ?? []) f();
	return () => {
		store.set(key, prev);
		for (const f of patchSubs.get(key) ?? []) f();
	};
}

/** 订阅某键的就地改写 (patch); 返回退订函数. */
export function onPatch(key: string, f: () => void): () => void {
	const set = patchSubs.get(key) ?? new Set<() => void>();
	set.add(f);
	patchSubs.set(key, set);
	return () => {
		set.delete(f);
		if (set.size === 0) patchSubs.delete(key);
	};
}

/**
 * 乐观写: 先按 next 改缓存让界面立刻显示预期结果, 再发写请求. 失败时撤销改写并让该键作废重拉, 界面回到后端真实状态;
 * 成功时由写接口自身的 invalidate 重拉校正. 只用于结果可在前端算出的写 (改名, 移动, 改标签, 删除), 启停这类以后端为准的不用.
 */
export async function optimistic<T, R>(key: string, next: (cur: T) => T, call: () => Promise<Result<R>>): Promise<Result<R>> {
	const undo = patch<T>(key, next);
	const r = await call();
	if (!r.ok) {
		undo();
		invalidate(key);
	}
	return r;
}

/** 写操作成功后按键失效. 用法: mutate(envApi 调用, "envs", "login"). 失败不失效. */
export async function mutate<T>(p: Promise<Result<T>>, ...keys: string[]): Promise<Result<T>> {
	const r = await p;
	if (r.ok) invalidate(...keys);
	return r;
}

/** 缓存键集中定义, 读写两侧引用同一处. */
export const KEYS = {
	envs: "envs",
	env: (id: string) => "env:" + id,
	folders: "folders",
	kernels: "kernels",
	settings: "settings",
	login: "login",
	cache: "cache",
	verdict: "verdict",
	status: "status",
	proxies: "proxies",
	extensions: "extensions",
	templates: "templates",
	logs: "logs",
	orphans: "orphans",
} as const;
