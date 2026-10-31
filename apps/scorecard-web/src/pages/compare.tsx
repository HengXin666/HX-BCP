import { Alert, AlertDescription, AlertTitle, Badge, DataTable, type DataTableColumn, HxBarChart } from "@hx/ui";
import { useMemo, useState } from "react";
import { scoreText, CELL, TREND } from "../components/labels.ts";
import type { KernelDiff, Trend } from "../lib/diff.ts";
import { useCase } from "../shell/CaseContext.tsx";
import { bootPage } from "../shell/mount.tsx";
import { PageNav } from "../shell/PageNav.tsx";

const ORDER: Record<Trend, number> = { worse: 0, better: 1, same: 2, new: 3, gone: 4, first: 5 };

/** 翻转的站写成一句话 */
function flipText(row: KernelDiff): string {
	return row.flips.map((flip) => `${flip.name} ${CELL[flip.from].text} -> ${CELL[flip.to].text}`).join(", ");
}

/** 与上次对比: 上方双柱图看升降, 下方表格看具体哪个站翻了 */
function ComparePage() {
	const load = useCase();
	const { diff } = load;
	const [changedOnly, setChangedOnly] = useState(false);
	const rows = useMemo(() => {
		const kept = changedOnly ? diff.kernels.filter((row) => row.trend === "better" || row.trend === "worse") : diff.kernels;
		return [...kept].sort((left, right) => ORDER[left.trend] - ORDER[right.trend]);
	}, [diff.kernels, changedOnly]);
	const chart = useMemo(
		() =>
			diff.kernels
				.filter((row) => row.before !== null || row.after !== null)
				.map((row) => ({ name: row.label, 上次: row.before ?? 0, 本次: row.after ?? 0 })),
		[diff.kernels],
	);
	const columns: DataTableColumn<KernelDiff>[] = [
		{ key: "label", header: "内核", render: (row) => row.label },
		{ key: "trend", header: "升降", render: (row) => <Badge variant={TREND[row.trend].variant}>{TREND[row.trend].text}</Badge> },
		{ key: "before", header: "上次", numeric: true, align: "right", render: (row) => scoreText(row.before) },
		{ key: "after", header: "本次", numeric: true, align: "right", render: (row) => scoreText(row.after) },
		{ key: "delta", header: "分差", numeric: true, align: "right", render: (row) => (row.delta === null ? "无可比" : row.delta > 0 ? `+${row.delta.toFixed(1)}` : row.delta.toFixed(1)) },
		{ key: "flips", header: "结论变化的站", render: (row) => flipText(row) || "无" },
	];
	if (diff.first) {
		return (
			<main className="mx-auto flex max-w-5xl flex-col gap-5 p-6">
				<h1 className="text-2xl font-semibold">与上次对比</h1>
				<PageNav current="compare" load={load} />
				<Alert>
					<AlertTitle>首次测评, 无可比</AlertTitle>
					<AlertDescription>这个 PR 之前没有测评记录, 从下一次开始显示升降.</AlertDescription>
				</Alert>
			</main>
		);
	}
	return (
		<main className="mx-auto flex max-w-5xl flex-col gap-5 p-6">
			<h1 className="text-2xl font-semibold">与上次对比</h1>
			<PageNav current="compare" load={load} />
			<div className="flex flex-wrap items-center justify-between gap-3">
				<p className="text-muted-foreground text-sm">
					对比第 {diff.previousNo} 次: {diff.better} 个变好, {diff.worse} 个变差, {diff.same} 个没变
				</p>
				<button
					type="button"
					onClick={() => setChangedOnly((value) => !value)}
					className={`cursor-pointer rounded-lg border px-3 py-1.5 text-xs transition-colors duration-150 ${changedOnly ? "bg-muted font-medium" : "hover:bg-muted/50"}`}
				>
					{changedOnly ? "显示全部内核" : "只看有变化的"}
				</button>
			</div>
			{chart.length ? (
				<HxBarChart data={chart} series={[{ dataKey: "上次", name: "上次" }, { dataKey: "本次", name: "本次" }]} xKey="name" height={220} />
			) : null}
			<DataTable bare columns={columns} rows={rows} rowKey={(row) => row.kernel} emptyText="这次没有任何内核发生变化" />
		</main>
	);
}

bootPage(ComparePage);
