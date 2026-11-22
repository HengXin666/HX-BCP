/**
 * 全局快捷键. 键名写法: "mod+k" (Ctrl 或 Cmd), "shift+?", 单键 "n", 两段序列 "g b" (先按 g, 1 秒内再按 b).
 * 焦点在输入框, 文本域, 可编辑区里时只响应带 mod 的组合键, 不抢打字.
 */
import { useEffect, useRef } from "react";

export const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/i.test(navigator.platform);
export const MOD_LABEL = IS_MAC ? "⌘" : "Ctrl";

/** 事件目标是否是正在打字的地方. */
export function isTyping(t: EventTarget | null): boolean {
	const el = t as HTMLElement | null;
	if (!el || !el.tagName) return false;
	return el.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName) || el.getAttribute("role") === "combobox";
}

function keyName(e: KeyboardEvent): string {
	const parts: string[] = [];
	if (e.metaKey || e.ctrlKey) parts.push("mod");
	if (e.altKey) parts.push("alt");
	const k = e.key.length === 1 ? e.key.toLowerCase() : e.key.toLowerCase();
	if (e.shiftKey && k.length > 1) parts.push("shift");
	parts.push(k);
	return parts.join("+");
}

const SEQ_MS = 1000;

/** 注册一组快捷键, 组件卸载即注销. bindings 每次渲染取最新值, 不用 memo. */
export function useHotkeys(bindings: Record<string, (e: KeyboardEvent) => void>, enabled = true): void {
	const ref = useRef(bindings);
	ref.current = bindings;
	useEffect(() => {
		if (!enabled) return;
		let prefix = "";
		let timer: ReturnType<typeof setTimeout> | null = null;
		const on = (e: KeyboardEvent): void => {
			const name = keyName(e);
			const typing = isTyping(e.target);
			if (typing && !name.startsWith("mod+")) return;
			const seq = prefix ? prefix + " " + name : "";
			const hit = (seq && ref.current[seq]) || ref.current[name];
			if (prefix) {
				prefix = "";
				if (timer) clearTimeout(timer);
			}
			if (hit) {
				e.preventDefault();
				hit(e);
				return;
			}
			if (Object.keys(ref.current).some((k) => k.startsWith(name + " "))) {
				prefix = name;
				timer = setTimeout(() => (prefix = ""), SEQ_MS);
			}
		};
		window.addEventListener("keydown", on);
		return () => {
			window.removeEventListener("keydown", on);
			if (timer) clearTimeout(timer);
		};
	}, [enabled]);
}
