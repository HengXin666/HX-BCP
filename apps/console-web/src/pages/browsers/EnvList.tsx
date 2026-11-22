import { useEffect, useLayoutEffect, useRef, useState, type JSX, type RefObject } from "react";
import { Pagination } from "@hx/ui";
import type { Env } from "../../lib/api";
import type { Seg } from "../../lib/chain";
import type { EnvQuery, QueryResult, SortKey } from "../../lib/env-query";
import { EnvCards } from "./EnvCards";
import { EnvTable, type EnvAction } from "./EnvTable";

/** 宽于 md (768px) 用表格, 否则用卡片. 只渲染一种, 不在 DOM 里放两份. */
const WIDE = "(min-width: 768px)";

function useWide(): boolean {
	const [wide, setWide] = useState(() => window.matchMedia(WIDE).matches);
	useEffect(() => {
		const mq = window.matchMedia(WIDE);
		const on = (): void => setWide(mq.matches);
		mq.addEventListener("change", on);
		return () => mq.removeEventListener("change", on);
	}, []);
	return wide;
}

/** 当前抽屉那行的高亮样式. 写成字面量, Tailwind 能扫到. */
const ACTIVE_CLS = ["bg-accent/50", "shadow-[inset_3px_0_0_var(--color-primary)]"];

/**
 * 给 activeId 那一行打高亮: 表格与卡片都按 rows 顺序渲染, 用下标找到 tbody>tr 或 [data-env-card],
 * 打上 data-env-active 与高亮类, 并滚到可见. 不改子组件的 props, 换行或换页时清掉旧标记.
 */
function useActiveRow(box: RefObject<HTMLDivElement | null>, rows: Env[], activeId: string | undefined, wide: boolean): void {
	useLayoutEffect(() => {
		const root = box.current;
		if (!root) return;
		const idx = activeId ? rows.findIndex((e) => e.id === activeId) : -1;
		const items = root.querySelectorAll<HTMLElement>(wide ? "tbody > tr" : "[data-env-card]");
		const el = idx >= 0 && idx < items.length ? items[idx] : null;
		if (!el) return;
		el.dataset.envActive = "1";
		el.classList.add(...ACTIVE_CLS);
		el.scrollIntoView({ block: "nearest" });
		return () => {
			delete el.dataset.envActive;
			el.classList.remove(...ACTIVE_CLS);
		};
	}, [box, rows, activeId, wide]);
}

/**
 * 分页条: sticky bottom-0 贴视口底部, 首屏就能看到. 不用 fixed, 免得盖住分组栏.
 * 只有一页时也显示范围, 让人知道一共多少.
 */
function EnvFooter(props: { page: QueryResult; size: number; onPage: (n: number) => void }): JSX.Element {
	const { page, total, pageCount } = props.page;
	const from = total ? (page - 1) * props.size + 1 : 0;
	const to = Math.min(page * props.size, total);
	return (
		<div className="sticky bottom-0 z-20 flex flex-wrap items-center gap-2 rounded-b-xl border-t border-border/60 bg-card/95 px-3 py-2 backdrop-blur" data-env-footer="">
			<span className="t-aux tabular-nums text-muted-foreground">{"第 " + from + " - " + to + " 个, 共 " + total + " 个"}</span>
			{pageCount > 1 ? <Pagination className="ml-auto mx-0 w-auto" page={page} pageCount={pageCount} onPageChange={props.onPage} /> : null}
		</div>
	);
}

/**
 * 浏览器列表 (@container: 表格次要列按本容器宽度出现): 宽屏表格, 窄屏卡片, 底部吸底分页条.
 * 只负责展示与通知: 点行 / "详情" 图标 -> onDetail, 点链路条 -> onChain. 两个抽屉由 BrowsersPage 持有,
 * 这样命令面板的 "打开环境" 与 hash 里的 env= 能直接驱动详情, 不依赖该行在当前页.
 * activeId 是正在详情或链路抽屉里的那一行, 由 useActiveRow 高亮; 子组件 props 不变.
 */
export function EnvList(props: {
	page: QueryResult;
	query: EnvQuery;
	selected: ReadonlySet<string>;
	activeId?: string;
	onSelect: (ids: string[], checked: boolean) => void;
	onSort: (col: SortKey) => void;
	onAction: (a: EnvAction, env: Env) => void;
	onPage: (n: number) => void;
	chainOf: (env: Env) => Seg[];
	onDetail: (env: Env) => void;
	onChain: (env: Env) => void;
}): JSX.Element {
	const wide = useWide();
	const rows = props.page.rows;
	const box = useRef<HTMLDivElement>(null);
	useActiveRow(box, rows, props.activeId, wide);
	const onAction = (a: EnvAction, e: Env): void => (a === "detail" ? props.onDetail(e) : props.onAction(a, e));
	return (
		<div ref={box} data-env-list="" className="@container flex min-w-0 flex-col rounded-xl border border-border bg-card" data-active-env={props.activeId ?? ""}>
			{wide ? (
				<EnvTable
					rows={rows}
					query={props.query}
					selected={props.selected}
					onSelect={props.onSelect}
					onSort={props.onSort}
					onAction={onAction}
					chainOf={props.chainOf}
					onOpen={props.onChain}
					onRow={props.onDetail}
				/>
			) : (
				<EnvCards rows={rows} selected={props.selected} onSelect={props.onSelect} onAction={onAction} chainOf={props.chainOf} onOpen={props.onChain} />
			)}
			<EnvFooter page={props.page} size={props.query.size} onPage={props.onPage} />
		</div>
	);
}
