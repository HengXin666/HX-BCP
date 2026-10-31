import { Badge, Card, CardContent, CardHeader, CardTitle, DataTable, type DataTableColumn, HxRadarChart, Separator } from "@hx/ui";
import { useMemo, useState } from "react";
import { CELL, kernelDot, scoreText } from "../components/labels.ts";
import type { SiteCell } from "../lib/record.ts";
import { useCase } from "../shell/CaseContext.tsx";
import { bootPage } from "../shell/mount.tsx";
import { PageNav } from "../shell/PageNav.tsx";

const SITE_COLUMNS: DataTableColumn<SiteCell>[] = [
	{ key: "name", header: "检测站", render: (row) => row.name },
	{ key: "verdict", header: "结论", render: (row) => <Badge variant={CELL[row.verdict].variant}>{CELL[row.verdict].text}</Badge> },
	{ key: "score", header: "分", numeric: true, align: "right", render: (row) => (row.score === null ? "" : scoreText(row.score)) },
	{ key: "note", header: "说明", render: (row) => <span className="text-muted-foreground text-xs">{row.note}</span> },
];

/** 从地址里的 ?kernel= 取要看哪个内核, 取不到就用第一个 */
function pickKernel(): string {
	if (typeof window === "undefined") return "";
	return new URLSearchParams(window.location.search).get("kernel") ?? "";
}

/** 单内核详情: 左侧选内核, 右侧该内核的逐站结论与它的站点雷达 */
function KernelPage() {
	const load = useCase();
	const initial = pickKernel();
	const [chosen, setChosen] = useState(initial);
	const kernel = load.current.kernels.find((item) => item.kernel === chosen) ?? load.current.kernels[0];
	const radar = useMemo(() => {
		if (!kernel) return [];
		return kernel.sites.filter((site) => site.score !== null).map((site) => ({ site: site.name, 分数: site.score ?? 0 }));
	}, [kernel]);
	if (!kernel) {
		return (
			<main className="mx-auto max-w-5xl p-6">
				<PageNav current="kernel" load={load} />
				<p className="text-muted-foreground py-10 text-center text-sm">这次没有任何内核</p>
			</main>
		);
	}
	return (
		<main className="mx-auto flex max-w-6xl flex-col gap-5 p-6">
			<h1 className="text-2xl font-semibold">单内核详情</h1>
			<PageNav current="kernel" load={load} />
			<div className="grid gap-5 md:grid-cols-[200px_1fr]">
				<aside className="flex flex-col gap-1">
					{load.current.kernels.map((item) => (
						<a
							key={item.kernel}
							href={`?kernel=${item.kernel}`}
							onClick={(event) => {
								event.preventDefault();
								setChosen(item.kernel);
							}}
							aria-current={item.kernel === kernel.kernel ? "true" : undefined}
							className={`flex items-center justify-between gap-2 rounded-lg px-3 py-2 text-sm transition-colors duration-150 ${
								item.kernel === kernel.kernel ? "bg-muted font-medium" : "hover:bg-muted/50"
							}`}
						>
							<span className="truncate">{item.label}</span>
							<span className="text-muted-foreground shrink-0 font-mono text-xs">{item.reason ? "未测" : scoreText(item.score)}</span>
						</a>
					))}
				</aside>
				<section className="flex flex-col gap-4">
					<div className="flex flex-wrap items-center gap-3">
						<h2 className="text-lg font-semibold">{kernel.label}</h2>
						<Badge variant="outline">{kernelDot(kernel).label}</Badge>
						<span className="font-mono text-sm">{scoreText(kernel.score)}</span>
					</div>
					<div>
						<div className="text-muted-foreground text-xs">实测 UA</div>
						<code className="break-all text-xs">{kernel.ua || "未取到 UA, 不代填"}</code>
					</div>
					{kernel.reason ? <p className="text-sm">未测原因 {kernel.reason}</p> : null}
					<Separator />
					{radar.length ? (
						<Card>
							<CardHeader>
								<CardTitle className="text-sm">各站分数</CardTitle>
							</CardHeader>
							<CardContent>
								<HxRadarChart data={radar} series={[{ dataKey: "分数", name: "分数" }]} angleKey="site" height={260} />
							</CardContent>
						</Card>
					) : null}
					<DataTable bare columns={SITE_COLUMNS} rows={kernel.sites} rowKey={(row) => row.id} emptyText="没有站点记录" />
				</section>
			</div>
		</main>
	);
}

bootPage(KernelPage);
