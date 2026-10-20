import { useEffect, useState } from "react";
import type { Result } from "./api";

/** 拉一次数据并按 5 秒轮询, 返回最新结果与是否还在加载. */
export function usePoll<T>(load: (signal: AbortSignal) => Promise<Result<T>>, intervalMs = 5000) {
	const [state, setState] = useState<{ result: Result<T> | null; loading: boolean }>({
		result: null,
		loading: true,
	});

	useEffect(() => {
		const ctl = new AbortController();
		let timer: ReturnType<typeof setTimeout> | null = null;
		let alive = true;

		const pull = (): void => {
			load(ctl.signal)
				.then((result) => {
					if (alive) setState({ result, loading: false });
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
	}, [load, intervalMs]);

	return state;
}
