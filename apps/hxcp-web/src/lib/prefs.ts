/** 界面偏好 (主题等) 存 localStorage, 同页多处订阅同一个键时一起更新. 只放界面状态, 不放任何凭据. */
import { useCallback, useEffect, useState } from "react";

const PREFIX = "hxcp.console.";
const subs = new Map<string, Set<() => void>>();

export type PrefValue = string | number | boolean;

/** 读一次偏好, 读不到或类型不对时回默认值. */
export function readPref<T extends PrefValue>(key: string, fallback: T): T {
	try {
		const raw = window.localStorage.getItem(PREFIX + key);
		if (raw === null) return fallback;
		const v = JSON.parse(raw) as unknown;
		return typeof v === typeof fallback ? (v as T) : fallback;
	} catch {
		return fallback;
	}
}

/** 写偏好并通知所有订阅者. */
export function writePref<T extends PrefValue>(key: string, value: T): void {
	try {
		window.localStorage.setItem(PREFIX + key, JSON.stringify(value));
	} catch {
		// 隐私模式写不进去时只在内存里生效
	}
	for (const f of subs.get(key) ?? []) f();
}

/** 订阅一个偏好键, 返回当前值与写入函数. */
export function usePref<T extends PrefValue>(key: string, fallback: T): [T, (v: T) => void] {
	const [value, setValue] = useState<T>(() => readPref(key, fallback));
	useEffect(() => {
		const on = (): void => setValue(readPref(key, fallback));
		const set = subs.get(key) ?? new Set<() => void>();
		set.add(on);
		subs.set(key, set);
		return () => void set.delete(on);
		// fallback 只作初值
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [key]);
	const write = useCallback((v: T) => writePref(key, v), [key]);
	return [value, write];
}

