import { Badge, DataTable, type DataTableColumn, EmptyState, Progress, SegmentedControl, StateDot } from "@hx/ui";
import { useMemo, useState } from "react";
import type { KernelRecord, SiteCell } from "../lib/record.ts";
import { hasScore, unmeasuredCount } from "../lib/record.ts";
import { useCase } from "../shell/CaseContext.tsx";
import { bootPage } from "../shell/mount.tsx";
import { PageNav } from "../shell/PageNav.tsx";
import { CELL, kernelDot, scoreText } from "../components/labels.ts";
import { Expandable } from "../components/Expandable.tsx";

type SortKey = "default" | "score" | "missing";
type Filter = "all" | "scored" | "unmeasured";

const FILTER_ITEMS = [
	{ key: "all" as const, label: "全部" },
	{ key: "scored" as const, label: "只看有分" },
	{ key: "unmeasured" as const, label: "只看未测" },
];
const SORT_ITEMS = [
	{ key: "default" as const, label: "原始顺序" },
	{ key: "score" as const, label: "按分数" },
	{ key: "missing" as const, label: "按未测数" },
];

const SITE_COLUMNS: DataTableColumn<SiteCell>[] = [
	{ key: "name", header: "检测站", render: (row) => row.name },
	{ key: "verdict", header: "结论", render: (row) => <Badge variant={CELL[row.verdict].variant}>{CELL[row.verdict].text}</Badge> },
	{ key: "score", header: "分", numeric: true, align: "right", render: (row) => (row.score === null ? "" : scoreText(row.score)) },
	{ key: "note", header: "未测原因或记录", render: (row) => <span className="text-muted-foreground text-xs">{row.note}</span> },
];

/** 有分的排前面, 未测的沉底 */
function sortKernels(rows: KernelRecord[], key: SortKey): KernelRecord[] {
	if (key === "default") return rows;
	const copy = [...rows];
	copy.sort((left, right) => {
		if (key === "score") {
			if (left.score === null && right.score === null) return unmeasuredCount(left) - unmeasuredCount(right);
			if (left.score === null) return 1;
			if (right.score === null) return -1;
			return right.score - left.score;
		}
		return unmeasuredCount(right) - unmeasuredCount(left);
	});
	return copy;
}

/** 单个内核展开后的样子, 单独放在这里因为只有本页用 */
function KernelBody({ kernel }: { kernel: KernelRecord }) {
	return (
		<div className="flex flex-col gap-3">
			<div className="flex gap-1" role="img" aria-label="各站结论分布">
				{kernel.sites.map((site) => (
					<span key={site.id} title={`${site.name} ${CELL[site.verdict].text}`} className={`h-1.5 w-full rounded-full ${CELL[site.verdict].bar}`} />
				))}
			</div>
			<div>
				<div className="text-muted-foreground text-xs">实测 UA</div>
				<code className="break-all text-xs">{kernel.ua || "未取到 UA, 不代填"}</code>
			</div>
			<div className="text-muted-foreground flex flex-wrap gap-x-6 gap-y-1 text-xs">
				<span>采集时间 {kernel.capturedAt || "未记录"}</span>
				<span>分数 {scoreText(kernel.score)}</span>
			</div>
			{kernel.reason ? <div className="text-sm">未测原因 {kernel.reason}</div> : null}
			<DataTable bare columns={SITE_COLUMNS} rows={kernel.sites} rowKey={(row) => row.id} emptyText="没有站点记录" />
		</div>
	);
}

/** 本次各内核: 每个内核一张可展开的卡, 三档筛选两种排序 */
function NowPage() {
	const load = useCase();
	const [filter, setFilter] = useState<Filter>("all");
	const [sort, setSort] = useState<SortKey>("default");
	const [open, setOpen] = useState("");
	const rows = useMemo(() => {
		const kept = load.current.kernels.filter((item) => {
			if (filter === "scored") return hasScore(item);
			if (filter === "unmeasured") return !hasScore(item);
			return true;
		});
		return sortKernels(kept, sort);
	}, [load.current.kernels, filter, sort]);
	return (
		<main className="mx-auto flex max-w-5xl flex-col gap-5 p-6">
			<h1 className="text-2xl font-semibold">本次各内核</h1>
			<PageNav current="now" load={load} />
			<div className="flex flex-wrap items-center gap-3">
				<SegmentedControl items={FILTER_ITEMS} value={filter} onChange={setFilter} />
				<SegmentedControl items={SORT_ITEMS} value={sort} onChange={setSort} />
				<span className="text-muted-foreground text-xs">
					{rows.length} / {load.current.kernels.length}
				</span>
			</div>
			<div className="flex flex-col gap-2">
				{rows.map((kernel) => (
					<Expandable
						key={kernel.kernel}
						expanded={open === kernel.kernel}
						onToggle={() => setOpen(open === kernel.kernel ? "" : kernel.kernel)}
						title={
							<>
								<span>{kernel.label}</span>
								<StateDot {...kernelDot(kernel)} />
								<Badge variant={kernel.reason ? "secondary" : kernel.verdict === "block" ? "destructive" : "success"}>
									{kernel.reason ? "未测" : kernel.verdict === "block" ? "阻断" : kernel.verdict === "pass" ? "通过" : "未完成"}
								</Badge>
							</>
						}
						subtitle={kernel.reason || kernel.ua || "未取到 UA, 不代填"}
						trailing={
							<span className="flex items-center gap-3">
								<Progress value={kernel.score ?? 0} tone={kernel.reason ? "primary" : "success"} className="w-24" />
								<span className="w-12 text-right font-mono text-sm">{kernel.reason ? "未测" : (kernel.score ?? 0).toFixed(1)}</span>
							</span>
						}
					>
						<KernelBody kernel={kernel} />
					</Expandable>
				))}
				{rows.length === 0 ? <EmptyState text="这个筛选下没有内核" hint="换个筛选条件看看" /> : null}
			</div>
		</main>
	);
}

bootPage(NowPage);
