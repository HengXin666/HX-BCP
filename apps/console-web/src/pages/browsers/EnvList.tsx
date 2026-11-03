import { useCallback, useEffect, useState, type JSX } from "react";
import { Pagination } from "@hx/ui";
import type { Env } from "../../lib/api";
import { chainOf, upstreamKey } from "../../lib/chain";
import type { EnvQuery, QueryResult, SortKey } from "../../lib/env-query";
import type { ExitChecks } from "../../lib/hooks";
import { ChainSheet } from "./ChainSheet";
import { DetailSheet } from "./DetailSheet";
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

/**
 * 分页条: sticky bottom-0 贴视口底部, 首屏就能看到, 不用滚到表格末尾. 不用 fixed, 免得盖住分组栏.
 * 不用负 margin 撑满卡片 (会让父级 scrollWidth 多出 14px). 只有一页时也显示范围, 让人知道一共多少.
 */
function EnvFooter(props: { page: QueryResult; size: number; onPage: (n: number) => void }): JSX.Element {
	const { page, total, pageCount } = props.page;
	const from = total ? (page - 1) * props.size + 1 : 0;
	const to = Math.min(page * props.size, total);
	return (
		<div className="sticky bottom-0 z-20 flex flex-wrap items-center gap-2 border-t border-border/60 bg-card px-3 py-2" data-env-footer="">
			<span className="t-aux tabular-nums text-muted-foreground">{"第 " + from + " - " + to + " 个, 共 " + total + " 个"}</span>
			{pageCount > 1 ? <Pagination className="ml-auto mx-0 w-auto" page={page} pageCount={pageCount} onPageChange={props.onPage} /> : null}
		</div>
	);
}

/**
 * 浏览器列表 (@container: 表格的次要列按本容器宽度出现): 宽屏表格, 窄屏卡片, 底部吸底分页条.
 * 两个抽屉分工: 点行 (或操作格的 "详情" 图标) = 详情抽屉 DetailSheet (五页签); 点链路列的链路条 = 链路抽屉 ChainSheet. 两者互斥.
 * 出口检测由 BrowsersPage 对全部上游统一跑 (结果全局共享), 这里只读结果.
 */
export function EnvList(props: {
	page: QueryResult;
	query: EnvQuery;
	selected: ReadonlySet<string>;
	onSelect: (ids: string[], checked: boolean) => void;
	onSort: (col: SortKey) => void;
	onAction: (a: EnvAction, env: Env) => void;
	onPage: (n: number) => void;
	checks: ExitChecks;
}): JSX.Element {
	const wide = useWide();
	const rows = props.page.rows;
	const checks = props.checks;
	const [openId, setOpenId] = useState("");
	const results = checks.results;
	const segsOf = useCallback((e: Env) => chainOf(e, results[upstreamKey(e.proxy)]), [results]);
	const onOpen = useCallback((e: Env) => (setDetail(null), setOpenId(e.id)), []);
	const opened = rows.find((e) => e.id === openId) ?? null;
	// 详情抽屉按 id 记, 换页或筛掉后仍用最后一份数据 (抽屉内自己轮询 /status), 关掉才清
	const [detail, setDetail] = useState<Env | null>(null);
	const onRow = useCallback((e: Env) => (setOpenId(""), setDetail(e)), []);
	const detailEnv = detail ? rows.find((e) => e.id === detail.id) ?? detail : null;
	const onAction = useCallback((a: EnvAction, e: Env) => (a === "detail" ? onRow(e) : props.onAction(a, e)), [onRow, props.onAction]);
	return (
		<div className="@container flex min-w-0 flex-col">
			{wide ? (
				<EnvTable rows={rows} query={props.query} selected={props.selected} onSelect={props.onSelect} onSort={props.onSort} onAction={onAction} chainOf={segsOf} onOpen={onOpen} onRow={onRow} />
			) : (
				<EnvCards rows={rows} selected={props.selected} onSelect={props.onSelect} onAction={onAction} chainOf={segsOf} onOpen={onOpen} />
			)}
			<EnvFooter page={props.page} size={props.query.size} onPage={props.onPage} />
			<ChainSheet env={opened} onClose={() => setOpenId("")} checks={checks} />
			<DetailSheet env={detailEnv} onClose={() => setDetail(null)} segs={detailEnv ? segsOf(detailEnv) : []} onChain={() => detailEnv && onOpen(detailEnv)} />
		</div>
	);
}
