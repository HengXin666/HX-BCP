import { Badge, DataTable, type DataTableColumn } from "@hx/ui";
import type { KernelRecord, SiteCell } from "../lib/record.ts";
import { CELL, scoreText } from "./labels.ts";

/** 站点结论的色块, 让一列文字变成一眼能扫的进度带 */
function verdictBar(kernel: KernelRecord) {
	return (
		<div className="flex gap-1" role="img" aria-label="各站结论分布">
			{kernel.sites.map((site) => (
				<span
					key={site.id}
					title={`${site.name} ${CELL[site.verdict].text}`}
					className={`h-1.5 w-full rounded-full ${CELL[site.verdict].bar}`}
				/>
			))}
		</div>
	);
}

const columns: DataTableColumn<SiteCell>[] = [
	{ key: "name", header: "检测站", render: (row) => row.name },
	{ key: "verdict", header: "结论", render: (row) => <Badge variant={CELL[row.verdict].variant}>{CELL[row.verdict].text}</Badge> },
	{ key: "score", header: "分", numeric: true, align: "right", render: (row) => (row.score === null ? "" : scoreText(row.score)) },
	{ key: "note", header: "未测原因或记录", render: (row) => <span className="text-muted-foreground text-xs">{row.note}</span> },
];

/** 单个内核展开后的内容: 实测 UA, 采集时间, 未测原因, 逐站结论 */
export function KernelDetail({ kernel }: { kernel: KernelRecord }) {
	return (
		<div className="flex flex-col gap-3">
			{verdictBar(kernel)}
			<div className="text-sm">
				<div className="text-muted-foreground text-xs">实测 UA</div>
				<code className="break-all text-xs">{kernel.ua || "未取到 UA, 不代填"}</code>
			</div>
			<div className="text-muted-foreground flex flex-wrap gap-x-6 gap-y-1 text-xs">
				<span>采集时间 {kernel.capturedAt || "未记录"}</span>
				<span>分数 {scoreText(kernel.score)}</span>
			</div>
			{kernel.reason ? <div className="text-sm">未测原因 {kernel.reason}</div> : null}
			<DataTable bare columns={columns} rows={kernel.sites} rowKey={(row) => row.id} emptyText="没有站点记录" />
		</div>
	);
}
