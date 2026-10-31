import { Alert, AlertDescription, AlertTitle, Badge, DataTable, type DataTableColumn, StatCard, StatGrid } from "@hx/ui";
import { useMemo, useState } from "react";
import type { CaseDiff, KernelDiff, Trend } from "../lib/diff.ts";
import { CELL, deltaText, scoreText, TREND } from "./labels.ts";
import { Reveal } from "./Reveal.tsx";

/** 未测站数从上次到这次, 缺一边写横线 */
function missText(row: KernelDiff): string {
	const side = (value: number | null) => (value === null ? "-" : String(value));
	return `${side(row.unmeasuredBefore)} -> ${side(row.unmeasuredAfter)}`;
}

/** 结论翻转的站, 例如 CreepJS 过 -> 不过 */
function flipText(row: KernelDiff): string {
	return row.flips.map((flip) => `${flip.name} ${CELL[flip.from].text} -> ${CELL[flip.to].text}`).join(", ");
}

const ORDER: Record<Trend, number> = { worse: 0, better: 1, same: 2, new: 3, gone: 4, first: 5 };

/** 分差着色, 只在真正有升降时染色 */
function deltaClass(delta: number | null): string {
	if (delta === null || delta === 0) return "";
	return delta > 0 ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400";
}

/** 与上次测评对比: 变差的排最前, 让需要看的东西自己浮上来 */
export function CompareView({ diff }: { diff: CaseDiff }) {
	const [changedOnly, setChangedOnly] = useState(false);
	const rows = useMemo(() => {
		const kept = changedOnly ? diff.kernels.filter((row) => row.trend === "better" || row.trend === "worse") : diff.kernels;
		return [...kept].sort((left, right) => ORDER[left.trend] - ORDER[right.trend]);
	}, [diff.kernels, changedOnly]);
	if (diff.first) {
		return (
			<Alert>
				<AlertTitle>首次测评, 无可比</AlertTitle>
				<AlertDescription>这个 PR 之前没有测评记录, 从下一次开始显示升降.</AlertDescription>
			</Alert>
		);
	}
	const columns: DataTableColumn<KernelDiff>[] = [
		{ key: "label", header: "内核", render: (row) => row.label },
		{ key: "trend", header: "升降", render: (row) => <Badge variant={TREND[row.trend].variant}>{TREND[row.trend].text}</Badge> },
		{ key: "before", header: "上次", numeric: true, align: "right", render: (row) => scoreText(row.before) },
		{ key: "after", header: "本次", numeric: true, align: "right", render: (row) => scoreText(row.after) },
		{ key: "delta", header: "分差", numeric: true, align: "right", render: (row) => <span className={deltaClass(row.delta)}>{deltaText(row.delta)}</span> },
		{ key: "miss", header: "未测站", render: missText },
		{ key: "flips", header: "结论变化的站", render: (row) => flipText(row) || "无" },
	];
	const delta = diff.unmeasuredDelta ?? 0;
	return (
		<div className="flex flex-col gap-4">
			<Reveal>
				<StatGrid columns={4}>
					<StatCard label="变好" value={diff.better} tone={diff.better ? "good" : "muted"} hint="分数上升" />
					<StatCard label="变差" value={diff.worse} tone={diff.worse ? "bad" : "muted"} hint="分数下降" />
					<StatCard label="没变" value={diff.same} tone="muted" hint="分数持平" />
					<StatCard label="未测站数变化" value={delta > 0 ? `+${delta}` : delta} tone={delta > 0 ? "bad" : delta < 0 ? "good" : "muted"} hint="正数表示更多站没测到" />
				</StatGrid>
			</Reveal>
			<Reveal delay={60}>
				<div className="flex flex-wrap items-center justify-between gap-3">
					<h3 className="text-sm font-semibold">对比第 {diff.previousNo} 次</h3>
					<button
						type="button"
						onClick={() => setChangedOnly((value) => !value)}
						className={`cursor-pointer rounded-lg border px-3 py-1.5 text-xs transition-colors duration-150 ${changedOnly ? "bg-muted font-medium" : "hover:bg-muted/50"}`}
					>
						{changedOnly ? "显示全部内核" : "只看有变化的"}
					</button>
				</div>
			</Reveal>
			<Reveal delay={100}>
				<DataTable bare columns={columns} rows={rows} rowKey={(row) => row.kernel} emptyText="这次没有任何内核发生变化" />
			</Reveal>
		</div>
	);
}
