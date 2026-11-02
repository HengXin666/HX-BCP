import { useCallback, useEffect, useState } from "react";
import type { EnvProxy, Result } from "./api";
import { checkExit, type ExitCheck } from "./api-write";

/** 拉一次数据并按 5 秒轮询, 返回最近一次成功结果, 是否首屏加载, 最近一次轮询错误, 以及写操作后立即重拉的 reload. */
export function usePoll<T>(load: (signal: AbortSignal) => Promise<Result<T>>, intervalMs = 5000) {
	const [state, setState] = useState<{ result: Result<T> | null; loading: boolean; error: string }>({
		result: null,
		loading: true,
		error: "",
	});
	const [tick, setTick] = useState(0);
	const reload = useCallback(() => setTick((n) => n + 1), []);

	useEffect(() => {
		const ctl = new AbortController();
		let timer: ReturnType<typeof setTimeout> | null = null;
		let alive = true;

		const pull = (): void => {
			load(ctl.signal)
				.then((result) => {
					// 轮询失败时保留上一份成功数据, 只记下错误, 表格不闪成空白
					if (alive) setState((prev) => (result.ok || !prev.result?.ok ? { result, loading: false, error: "" } : { ...prev, error: result.error }));
				})
				.then(() => {
					if (alive) timer = setTimeout(pull, intervalMs);
				});
		};

		pull();
		return () => {
			alive = false;
			ctl.abort();
			if (timer) clearTimeout(timer);
		};
	}, [load, intervalMs, tick]);

	return { ...state, reload };
}

/** 出口检测结果按上游键缓存在本页: 同一个上游只测一次, 表格的链路条与详情面板共用. 并发上限 4. */
/**
 * 出口检测结果按上游共享, 并且整个控制台共用一份 (模块级仓库): 状态页测过的出口, 回到浏览器页直接可用于筛选,
 * 不用再测一遍. 同一上游正在测时不重复发请求. 键是 lib/chain.upstreamKey (不含凭据).
 */
const exitStore = {
	results: {} as Record<string, ExitCheck>,
	pending: new Set<string>() as ReadonlySet<string>,
	subs: new Set<() => void>(),
	emit(): void {
		for (const f of this.subs) f();
	},
};

async function runExitChecks(items: { key: string; proxy: EnvProxy }[]): Promise<void> {
	const todo = items.filter((i) => i.key && i.proxy && !exitStore.pending.has(i.key));
	if (!todo.length) return;
	exitStore.pending = new Set([...exitStore.pending, ...todo.map((i) => i.key)]);
	exitStore.emit();
	let next = 0;
	const worker = async (): Promise<void> => {
		while (next < todo.length) {
			const item = todo[next];
			next += 1;
			const r = await checkExit(item.proxy ?? {});
			exitStore.results = { ...exitStore.results, [item.key]: r.ok ? r.data : { ok: false, error: r.error } };
			const p = new Set(exitStore.pending);
			p.delete(item.key);
			exitStore.pending = p;
			exitStore.emit();
		}
	};
	await Promise.all([0, 1, 2, 3].map(worker));
}

export function useExitChecks() {
	const [, force] = useState(0);
	useEffect(() => {
		const f = (): void => force((n) => n + 1);
		exitStore.subs.add(f);
		return () => void exitStore.subs.delete(f);
	}, []);
	const run = useCallback(runExitChecks, []);
	return { results: exitStore.results, pending: exitStore.pending, run };
}

export type ExitChecks = ReturnType<typeof useExitChecks>;
