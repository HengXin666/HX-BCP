import { Badge, Progress, SegmentedControl, StatCard, StatGrid, StateDot } from "@hx/ui";
import { useMemo, useState } from "react";
import { type CaseRecord, hasScore, type KernelRecord, unmeasuredCount } from "../lib/record.ts";
import { Expandable } from "./Expandable.tsx";
import { KernelDetail } from "./KernelDetail.tsx";
import { CELL, kernelDot } from "./labels.ts";
import { CountUp, Reveal } from "./Reveal.tsx";

type SortKey = "default" | "score" | "missing";
type Filter = "all" | "scored" | "unmeasured";

const SORT_ITEMS = [
	{ key: "default" as const, label: "原始顺序" },
	{ key: "score" as const, label: "按分数" },
	{ key: "missing" as const, label: "按未测数" },
];
const FILTER_ITEMS = [
	{ key: "all" as const, label: "全部" },
	{ key: "scored" as const, label: "只看有分" },
	{ key: "unmeasured" as const, label: "只看未测" },
];

/** 有分的排前面, 同分按未测站数少优先; 未测的内核沉到末尾 */
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

/** 内核整体的一句话结论, 未测优先 */
function conclusion(row: KernelRecord): { text: string; variant: "success" | "destructive" | "secondary" | "warning" } {
	if (row.reason) return { text: "未测", variant: "secondary" };
	if (row.verdict === "pass") return { text: "通过", variant: "success" };
	if (row.verdict === "block") return { text: "阻断", variant: "destructive" };
	return { text: "未完成", variant: "warning" };
}

/** 本次测评: 统计抬头 + 可排序可筛选的内核列表, 每个内核可展开看逐站结论 */
export function CaseOverview({ record }: { record: CaseRecord }) {
	const [sort, setSort] = useState<SortKey>("default");
	const [filter, setFilter] = useState<Filter>("all");
	const [expanded, setExpanded] = useState("");
	const scored = record.kernels.filter(hasScore);
	const blocked = scored.filter((kernel) => kernel.verdict === "block");
	const rows = useMemo(() => {
		const kept = record.kernels.filter((kernel) => {
			if (filter === "scored") return hasScore(kernel);
			if (filter === "unmeasured") return !hasScore(kernel);
			return true;
		});
		return sortKernels(kept, sort);
	}, [record.kernels, sort, filter]);
	return (
		<div className="flex flex-col gap-4">
			<Reveal>
				<StatGrid columns={4}>
					<StatCard label="内核数" value={record.kernels.length} />
					<StatCard label="有分" value={scored.length} tone={scored.length ? "good" : "muted"} hint={`共 ${record.kernels.length} 个`} />
					<StatCard label="未测" value={record.kernels.length - scored.length} tone="muted" hint="不计入分数" />
					<StatCard label="阻断" value={blocked.length} tone={blocked.length ? "bad" : "muted"} hint="高风险项" />
				</StatGrid>
			</Reveal>
			<Reveal delay={60}>
				<div className="flex flex-wrap items-center gap-3">
					<SegmentedControl items={FILTER_ITEMS} value={filter} onChange={(value) => setFilter(value)} />
					<SegmentedControl items={SORT_ITEMS} value={sort} onChange={(value) => setSort(value)} />
					<span className="text-muted-foreground text-xs">
						{rows.length} / {record.kernels.length}
					</span>
				</div>
			</Reveal>
			<div className="flex flex-col gap-2">
				{rows.map((row, index) => {
					const state = conclusion(row);
					const open = expanded === row.kernel;
					return (
						<Reveal key={row.kernel} delay={index * 40}>
							<Expandable
								expanded={open}
								onToggle={() => setExpanded(open ? "" : row.kernel)}
								title={
									<>
										<span>{row.label}</span>
										<StateDot {...kernelDot(row)} />
										<Badge variant={state.variant}>{state.text}</Badge>
									</>
								}
								subtitle={row.reason || row.ua || "未取到 UA, 不代填"}
								trailing={
									<span className="flex items-center gap-3">
										<Progress value={row.score ?? 0} tone={row.score === null ? "primary" : "success"} className="w-24" />
										<span className={`w-14 text-right font-mono text-sm ${row.score === null ? "text-muted-foreground" : ""}`}>
											{row.score === null ? "未测" : <CountUp value={row.score} />}
										</span>
									</span>
								}
							>
								<KernelDetail kernel={row} />
							</Expandable>
						</Reveal>
					);
				})}
				{rows.length === 0 ? <p className="text-muted-foreground py-8 text-center text-sm">这个筛选下没有内核</p> : null}
			</div>
		</div>
	);
}
