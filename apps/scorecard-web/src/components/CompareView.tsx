import { Alert, AlertDescription, AlertTitle, Badge, DataTable, type DataTableColumn, StatCard, StatGrid } from "@hx/ui";
import type { CaseDiff, KernelDiff } from "../lib/diff.ts";
import { CELL, deltaText, scoreText, TREND } from "./labels.ts";

/** 未测站数从上次到这次, 缺一边写横线 */
function missText(row: KernelDiff): string {
	const side = (value: number | null) => (value === null ? "-" : String(value));
	return `${side(row.unmeasuredBefore)} -> ${side(row.unmeasuredAfter)}`;
}

/** 结论翻转的站, 例如 CreepJS 过 -> 不过 */
function flipText(row: KernelDiff): string {
	return row.flips.map((flip) => `${flip.name} ${CELL[flip.from].text} -> ${CELL[flip.to].text}`).join(", ");
}

/** 与上次测评对比: 每内核分数升降, 翻转的站, 未测数变化, 首次测评只说无可比 */
export function CompareView({ diff }: { diff: CaseDiff }) {
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
		{ key: "delta", header: "分差", numeric: true, align: "right", render: (row) => deltaText(row.delta) },
		{ key: "miss", header: "未测站", render: missText },
		{ key: "flips", header: "结论变化的站", render: (row) => flipText(row) || "无" },
	];
	const delta = diff.unmeasuredDelta ?? 0;
	return (
		<div className="flex flex-col gap-4">
			<StatGrid columns={4}>
				<StatCard label="变好" value={diff.better} tone={diff.better ? "good" : "muted"} />
				<StatCard label="变差" value={diff.worse} tone={diff.worse ? "bad" : "muted"} />
				<StatCard label="没变" value={diff.same} tone="muted" />
				<StatCard label="未测站数变化" value={delta > 0 ? `+${delta}` : delta} tone={delta > 0 ? "bad" : delta < 0 ? "good" : "muted"} />
			</StatGrid>
			<DataTable title={`对比第 ${diff.previousNo} 次`} columns={columns} rows={diff.kernels} rowKey={(row) => row.kernel} />
		</div>
	);
}
