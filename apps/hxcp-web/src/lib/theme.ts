/** 主题: 深色, 浅色, 跟随系统. 落到 html 的 data-mode, 由 @hx/ui tokens.css 的两套令牌接管, 组件不感知. */
import { useEffect } from "react";
import { readPref, usePref } from "./prefs";

export type ThemeMode = "dark" | "light" | "system";
const KEY = "theme";
const LIGHT_QUERY = "(prefers-color-scheme: light)";

export const THEME_LABEL: Record<ThemeMode, string> = { dark: "深色", light: "浅色", system: "跟随系统" };

/** 把模式解析成实际的 dark 或 light. */
export function resolveTheme(mode: ThemeMode): "dark" | "light" {
	if (mode !== "system") return mode;
	return window.matchMedia(LIGHT_QUERY).matches ? "light" : "dark";
}

/** 立即应用一次 (挂载前调用, 避免首屏闪色). */
export function applyTheme(mode: ThemeMode = readPref<ThemeMode>(KEY, "dark")): void {
	document.documentElement.dataset.mode = resolveTheme(mode);
}

/** 当前主题与切换函数; 跟随系统时监听系统配色变化. */
export function useTheme(): { mode: ThemeMode; resolved: "dark" | "light"; setMode: (m: ThemeMode) => void } {
	const [mode, setMode] = usePref<ThemeMode>(KEY, "dark");
	useEffect(() => {
		applyTheme(mode);
		if (mode !== "system") return;
		const mq = window.matchMedia(LIGHT_QUERY);
		const on = (): void => applyTheme("system");
		mq.addEventListener("change", on);
		return () => mq.removeEventListener("change", on);
	}, [mode]);
	return { mode, resolved: resolveTheme(mode), setMode };
}
