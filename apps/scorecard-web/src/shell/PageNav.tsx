import { useMemo } from "react";
import { casePage } from "../lib/data.ts";
import type { CaseLoad } from "./CaseContext.tsx";

/** 页面之间的链接; 每个页面在导航里高亮自己, 不做单页切换 */
const PAGES = [
	{ id: "overview", label: "总览" },
	{ id: "now", label: "本次各内核" },
	{ id: "compare", label: "与上次对比" },
	{ id: "kernel", label: "单内核详情" },
	{ id: "artifacts", label: "图与产物" },
] as const;

export type PageId = (typeof PAGES)[number]["id"];

/** 同一次测评下某个页面的地址; 总览就是那次的根路径 */
export function pageHref(target: PageId, no: number): string {
	const base = casePage(no);
	return target === "overview" ? base : `${base}${target}/`;
}

/** 悬停时先取目标页要用的数据, 点下去时缓存已经热了 */
function warm(target: PageId, no: number): void {
	const base = casePage(no);
	const href = target === "overview" ? base : `${base}${target}/`;
	const link = document.createElement("link");
	link.rel = "prefetch";
	link.href = href;
	document.head.appendChild(link);
}

/** 顶部的页面列表, 当前页高亮; 这些是真实链接, 刷新和直链都能用 */
export function PageNav({ current, load }: { current: PageId; load: CaseLoad }) {
	const items = useMemo(() => PAGES.map((page) => ({ ...page, href: pageHref(page.id, load.selected) })), [load.selected]);
	return (
		<nav className="border-border flex flex-wrap items-center gap-1 border-b pb-2">
			{items.map((page) => (
				<a
					key={page.id}
					href={page.href}
					aria-current={page.id === current ? "page" : undefined}
					onMouseEnter={() => warm(page.id, load.selected)}
					className={`rounded-lg px-3 py-1.5 text-sm transition-colors duration-150 ${
						page.id === current ? "bg-muted font-medium" : "text-muted-foreground hover:bg-muted/50"
					}`}
				>
					{page.label}
				</a>
			))}
		</nav>
	);
}
