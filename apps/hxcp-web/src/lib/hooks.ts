/** 轮询读接口: 首次立即读, 之后按间隔重读; 401 时问一次令牌. */
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, setToken } from "./api";

export interface Poll<T> {
	data: T | null;
	error: string;
	loading: boolean;
	reload: () => void;
}

let asking = false;

function askToken(e: unknown): void {
	if (!(e instanceof ApiError) || e.status !== 401 || asking) return;
	asking = true;
	const t = window.prompt("这个 API 需要 token (Bearer), 只保存在本标签页:");
	asking = false;
	if (t) setToken(t);
}

export function usePoll<T>(fn: () => Promise<T>, intervalMs = 5000, deps: unknown[] = []): Poll<T> {
	const [data, setData] = useState<T | null>(null);
	const [error, setError] = useState("");
	const [loading, setLoading] = useState(true);
	const [tick, setTick] = useState(0);
	const fnRef = useRef(fn);
	fnRef.current = fn;
	const reload = useCallback(() => setTick((v) => v + 1), []);
	useEffect(() => {
		let alive = true;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const run = (): void => {
			fnRef
				.current()
				.then((d) => alive && (setData(d), setError("")))
				.catch((e: unknown) => {
					askToken(e);
					if (alive) setError(e instanceof Error ? e.message : String(e));
				})
				.finally(() => {
					if (!alive) return;
					setLoading(false);
					if (intervalMs > 0) timer = setTimeout(run, intervalMs);
				});
		};
		run();
		return () => {
			alive = false;
			if (timer) clearTimeout(timer);
		};
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [tick, intervalMs, ...deps]);
	return { data, error, loading, reload };
}
