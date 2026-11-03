import type { JSX, ReactNode, SyntheticEvent } from "react";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { Button, Checkbox, DataTable, EmptyState, type DataTableColumn } from "@hx/ui";
import type { Env } from "../../lib/api";
import type { EnvQuery, SortKey } from "../../lib/env-query";
import type { Seg } from "../../lib/chain";
import { ChainStrip } from "./ChainStrip";
import { KernelChip } from "./KernelBits";
import { ActionCell, NameCell, StatusCell, TagsCell, selectable, whenShort, whereLabel, type EnvAction } from "./EnvCells";

export { selectable, type EnvAction } from "./EnvCells";

/**
 * 列宽 (1440 宽屏, 表格容器约 970px 时的实测目标): 勾选 36, 名称 >= 220 且吃掉剩余, 状态 ~60, 内核 <= 90, 链路 <= 120,
 * 位置 ~100, 标签 (最多 2 个 +N), 最近打开 ~90, 操作 <= 120. 单元格左右内边距统一 8px (DataTable 默认 12px).
 * 次要列按表格容器宽度逐级出现 (容器查询 @container, 不看视口: 视口宽不等于表格宽, 左边还有导航与分组栏),
 * 名称列不设 px 下限 (取整累积会溢出 1px), 阈值让名称在常见宽度下约 220 且表格不比容器宽: 名称 + 状态 + 内核 + 操作约 500px 永远显示,
 * 链路 >= 40rem (Tailwind v4 写法 @min-[40rem]), 位置 >= 46rem, 最近打开 >= 52rem, 标签 >= 58rem.
 * 表头吸顶: th 带 sticky top-0, 另有 styles.css 里 .env-table th 的普通 CSS 兜底; 滚动祖先是视口 (外层没有 overflow 盒子).
 */
const PAD = "px-2";
const CELL = "h-10 py-0 t-body whitespace-nowrap " + PAD;
const TH = "sticky top-0 z-10 h-10 py-0 bg-card t-micro text-muted-foreground " + PAD;
export const TABLE_CLASS = "env-table";
// 类名写全, 不拼接: Tailwind 只扫描源码里完整出现的类名
const CHAIN_COL = CELL + " hidden @min-[40rem]:table-cell";
const WHERE_COL = CELL + " hidden @min-[46rem]:table-cell";
const LAST_COL = CELL + " hidden @min-[52rem]:table-cell";
const TAGS_COL = CELL + " hidden @min-[58rem]:table-cell";

const stop = (e: SyntheticEvent): void => e.stopPropagation();

/** 行内可点的格子 (勾选, 链路, 操作) 拦下点击与按键, 不让它冒泡成 "点行开详情". */
function Stop({ children }: { children: ReactNode }): JSX.Element {
	return <span className="contents" onClick={stop} onKeyDown={stop}>{children}</span>;
}

/** 可点的排序表头: 再点一次同一列切换升降序. @hx/ui DataTable 没有内置排序, 见 .review/hx-ui-issue.md. */
function SortHeader(props: { label: string; col: SortKey; query: EnvQuery; onSort: (col: SortKey) => void }): JSX.Element {
	const on = props.query.sort === props.col;
	const Icon = !on ? ArrowUpDown : props.query.dir === "asc" ? ArrowUp : ArrowDown;
	return (
		<Button variant="ghost" size="xs" className="-ml-2 t-micro text-muted-foreground hover:text-foreground" onClick={() => props.onSort(props.col)}>
			{props.label}
			<Icon className={"size-4" + (on ? "" : " opacity-40")} />
		</Button>
	);
}

/** 浏览器表 (点行空白处打开右侧详情抽屉): 勾选, 名称, 状态, 内核, 链路 (四段, 点开看详情), 位置, 标签, 最近打开, 操作. 行高统一 40px, 表头 12px 大写加宽字距, 名称单行截断. */
export function EnvTable(props: {
	rows: Env[];
	query: EnvQuery;
	selected: ReadonlySet<string>;
	onSelect: (ids: string[], checked: boolean) => void;
	onSort: (col: SortKey) => void;
	onAction: (a: EnvAction, env: Env) => void;
	chainOf: (env: Env) => Seg[];
	onOpen: (env: Env) => void;
	onRow: (env: Env) => void;
}): JSX.Element {
	const pickable = props.rows.filter(selectable).map((e) => e.id);
	const picked = pickable.filter((id) => props.selected.has(id)).length;
	const headState = picked === 0 ? false : picked === pickable.length ? true : "indeterminate";

	const columns: DataTableColumn<Env>[] = [
		{
			key: "pick",
			width: "2.25rem",
			cellClassName: CELL,
			header: (
				<Checkbox
					checked={headState}
					disabled={pickable.length === 0}
					onCheckedChange={(v) => props.onSelect(pickable, v === true)}
					aria-label="选择本页人类组浏览器"
				/>
			),
			render: (e) =>
				selectable(e) ? (
					<Stop><Checkbox checked={props.selected.has(e.id)} onCheckedChange={(v) => props.onSelect([e.id], v === true)} aria-label={"选择 " + e.name} /></Stop>
				) : null,
		},
		{ key: "name", headerClassName: "w-full max-w-0", cellClassName: CELL + " w-full max-w-0", header: <SortHeader label="名称" col="name" query={props.query} onSort={props.onSort} />, render: (e) => <NameCell env={e} /> },
		{ key: "status", cellClassName: CELL, header: "状态", render: (e) => <StatusCell env={e} /> },
		{ key: "kernel", width: "5.5rem", cellClassName: CELL, header: <span title="具体内核 (族色: 天蓝 Chromium 系, 橙 Firefox 系; 虚线框 = 旧环境继承设置路径)">内核</span>, render: (e) => <KernelChip env={e} /> },
		{
			key: "chain",
			width: "7rem",
			header: <span title="浏览器 - 缓存 - 出口 - 目标">链路</span>,
			headerClassName: CHAIN_COL,
			cellClassName: CHAIN_COL + " py-0",
			render: (e) => <Stop><ChainStrip segs={props.chainOf(e)} label={e.name} onOpen={() => props.onOpen(e)} /></Stop>,
		},
		{
			key: "where",
			header: "位置",
			headerClassName: WHERE_COL,
			cellClassName: WHERE_COL + " max-w-[6.5rem] truncate text-muted-foreground",
			render: whereLabel,
		},
		{ key: "tags", header: "标签", headerClassName: TAGS_COL, cellClassName: TAGS_COL, render: (e) => <TagsCell tags={e.tags ?? []} /> },
		{
			key: "last",
			header: <SortHeader label="最近打开" col="last" query={props.query} onSort={props.onSort} />,
			headerClassName: LAST_COL,
			cellClassName: LAST_COL + " t-aux text-muted-foreground",
			render: (e) => whenShort(e.last_open_at),
		},
		{ key: "act", header: "", align: "right", width: "7rem", cellClassName: CELL, render: (e) => <Stop><ActionCell env={e} onAction={props.onAction} /></Stop> },
	];

	// 表头吸顶: DataTable 的 thead 已是 sticky, 每个 th 也设 sticky + 不透明底, 滚动中行文字不会从表头下透出来
	const pinned = columns.map((c) => ({ ...c, headerClassName: TH + " " + (c.headerClassName ?? ""), cellClassName: c.cellClassName ?? CELL }));
	return <DataTable columns={pinned} rows={props.rows} rowKey={(e) => e.id} onRowClick={props.onRow} empty={<EmptyState text="没有符合条件的浏览器" hint="换个关键字, 或在侧栏与工具条里放宽筛选" />} tableClassName={TABLE_CLASS} bare />;
}
