import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle, DataTable, type DataTableColumn, StateDot } from "@hx/ui";
import type { KernelRecord, SiteCell } from "../lib/record.ts";
import { CELL, kernelDot, scoreText } from "./labels.ts";

/** 单个内核展开: 实测 UA, 采集时间, 未测原因, 以及每个站的结论 */
export function KernelDetail({ kernel }: { kernel: KernelRecord }) {
	const columns: DataTableColumn<SiteCell>[] = [
		{ key: "name", header: "检测站", render: (row) => row.name },
		{ key: "verdict", header: "结论", render: (row) => <Badge variant={CELL[row.verdict].variant}>{CELL[row.verdict].text}</Badge> },
		{ key: "score", header: "分", numeric: true, align: "right", render: (row) => (row.score === null ? "" : scoreText(row.score)) },
		{ key: "note", header: "未测原因或记录", render: (row) => row.note },
	];
	return (
		<Card>
			<CardHeader>
				<CardTitle className="flex items-center gap-3">
					{kernel.label}
					<StateDot {...kernelDot(kernel)} />
					<span className="text-muted-foreground text-sm">{scoreText(kernel.score)}</span>
				</CardTitle>
				<CardDescription>采集时间 {kernel.capturedAt || "未记录"}</CardDescription>
			</CardHeader>
			<CardContent className="flex flex-col gap-3">
				<div className="text-sm">
					<div className="text-muted-foreground">实测 UA</div>
					<code className="break-all">{kernel.ua || "未取到 UA, 不代填"}</code>
				</div>
				{kernel.reason ? <div className="text-sm">未测原因 {kernel.reason}</div> : null}
				<DataTable bare columns={columns} rows={kernel.sites} rowKey={(row) => row.id} emptyText="没有站点记录" />
			</CardContent>
		</Card>
	);
}
