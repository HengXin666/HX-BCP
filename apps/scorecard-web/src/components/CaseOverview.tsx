import { DataTable, type DataTableColumn, StatCard, StatGrid, StateDot } from "@hx/ui";
import { type CaseRecord, hasScore, type KernelRecord, unmeasuredCount } from "../lib/record.ts";
import { kernelDot, scoreText } from "./labels.ts";

type Props = { record: CaseRecord; selected: string; onSelect: (kernel: string) => void };

/** 本次测评总表: 哪些内核有分, 哪些未测以及原因, 点一行看该内核详情 */
export function CaseOverview({ record, selected, onSelect }: Props) {
	const scored = record.kernels.filter(hasScore);
	const blocked = scored.filter((kernel) => kernel.verdict === "block");
	const columns: DataTableColumn<KernelRecord>[] = [
		{ key: "label", header: "内核", render: (row) => <span className={row.kernel === selected ? "font-semibold" : ""}>{row.label}</span> },
		{ key: "state", header: "状态", render: (row) => <StateDot {...kernelDot(row)} /> },
		{ key: "score", header: "分数", numeric: true, align: "right", render: (row) => scoreText(row.score) },
		{ key: "pass", header: "过", numeric: true, align: "right", render: (row) => row.sites.filter((site) => site.verdict === "pass").length },
		{ key: "fail", header: "不过", numeric: true, align: "right", render: (row) => row.sites.filter((site) => site.verdict === "fail").length },
		{ key: "miss", header: "未测站", numeric: true, align: "right", render: (row) => unmeasuredCount(row) },
		{ key: "reason", header: "未测原因", render: (row) => row.reason || "" },
	];
	return (
		<div className="flex flex-col gap-4">
			<StatGrid columns={4}>
				<StatCard label="内核数" value={record.kernels.length} />
				<StatCard label="有分" value={scored.length} tone={scored.length ? "good" : "muted"} />
				<StatCard label="未测" value={record.kernels.length - scored.length} tone="muted" />
				<StatCard label="阻断" value={blocked.length} tone={blocked.length ? "bad" : "muted"} />
			</StatGrid>
			<DataTable title="本次各内核" columns={columns} rows={record.kernels} rowKey={(row) => row.kernel} onRowClick={(row) => onSelect(row.kernel)} emptyText="这次没有任何内核的记录" />
		</div>
	);
}
