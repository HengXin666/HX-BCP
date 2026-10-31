import { type ReactNode, useState } from "react";

type Props = {
	title: ReactNode;
	subtitle?: ReactNode;
	trailing?: ReactNode;
	expanded: boolean;
	onToggle: () => void;
	children: ReactNode;
};

/**
 * 可展开的内核卡片。收起时只显示标题行, 展开时内容高度渐变并淡入。
 * 高度用 grid-template-rows 0fr 到 1fr 过渡, 比猜一个 max-height 数值稳。
 */
export function Expandable({ title, subtitle, trailing, expanded, onToggle, children }: Props) {
	return (
		<div className="bg-card overflow-hidden rounded-xl border transition-colors duration-200">
			<button
				type="button"
				aria-expanded={expanded}
				onClick={onToggle}
				className="hover:bg-muted/40 flex w-full cursor-pointer items-center gap-3 px-4 py-3 text-left transition-colors duration-150"
			>
				<span aria-hidden className={`text-muted-foreground shrink-0 transition-transform duration-300 ${expanded ? "rotate-90" : ""}`}>
					<svg viewBox="0 0 16 16" className="size-3.5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
						<title>展开</title>
						<path d="M6 3l5 5-5 5" />
					</svg>
				</span>
				<span className="min-w-0 flex-1">
					<span className="flex items-center gap-2 font-medium">{title}</span>
					{subtitle ? <span className="text-muted-foreground mt-0.5 block truncate text-xs">{subtitle}</span> : null}
				</span>
				{trailing ? <span className="shrink-0">{trailing}</span> : null}
			</button>
			<div className={`grid transition-[grid-template-rows] duration-300 ease-out ${expanded ? "grid-rows-[1fr]" : "grid-rows-[0fr]"}`}>
				<div className="overflow-hidden">
					<div className={`px-4 pb-4 transition-opacity duration-300 ${expanded ? "opacity-100" : "opacity-0"}`}>{children}</div>
				</div>
			</div>
		</div>
	);
}
