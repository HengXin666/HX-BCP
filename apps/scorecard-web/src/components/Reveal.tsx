import { useEffect, useState } from "react";
import type { ReactNode } from "react";

/**
 * 挂载后再显示内容, 给 CSS 过渡一个起始帧。
 * 直接渲染时 opacity 从 0 到 1 不会触发过渡, 因为浏览器把两次样式合并成一帧。
 */
export function Reveal({ children, delay = 0 }: { children: ReactNode; delay?: number }) {
	const [shown, setShown] = useState(false);
	useEffect(() => {
		const timer = window.setTimeout(() => setShown(true), delay);
		return () => window.clearTimeout(timer);
	}, [delay]);
	return (
		<div className={`transition-all duration-500 ease-out ${shown ? "translate-y-0 opacity-100" : "translate-y-2 opacity-0"}`}>
			{children}
		</div>
	);
}

/** 数字从 0 涨到目标值, 用于刷新时让分数变化被看见 */
export function CountUp({ value, digits = 1, suffix = "" }: { value: number; digits?: number; suffix?: string }) {
	const [shown, setShown] = useState(0);
	useEffect(() => {
		let frame = 0;
		const total = 24;
		const step = () => {
			frame += 1;
			const eased = 1 - (1 - frame / total) ** 3;
			setShown(value * Math.min(1, eased));
			if (frame < total) requestAnimationFrame(step);
		};
		requestAnimationFrame(step);
		return () => {
			frame = total;
		};
	}, [value]);
	return (
		<span className="tabular-nums">
			{shown.toFixed(digits)}
			{suffix}
		</span>
	);
}
