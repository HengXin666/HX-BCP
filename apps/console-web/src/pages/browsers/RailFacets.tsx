import { useState, type JSX, type ReactNode } from "react";
import { Bot, ChevronRight, Tag, Users } from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger, StateDot, EmptyState } from "@hx/ui";
import type { Env } from "../../lib/api";
import { matchStatus, type EnvQuery, type StatusFilter } from "../../lib/env-query";
import { FAMILY, shortName, type Kernel, type KernelView } from "../../lib/kernels";

/** 一个可点的条目: 左侧标记, 名称 (单行截断), 右侧计数. 选中时浅底 + 左侧色条 + 加粗, 再点一次取消. */
export function FacetItem(props: { label: string; count: number; active: boolean; onClick: () => void; mark?: JSX.Element; dim?: boolean; note?: string }): JSX.Element {
	return (
		<button
			type="button"
			onClick={props.onClick}
			aria-pressed={props.active}
			data-facet-item=""
			data-active={String(props.active)}
			className={
				"flex h-7 w-full min-w-0 items-center gap-2 rounded-md border-l-2 px-2 text-left t-body transition-colors duration-150 " +
				(props.active ? "border-primary bg-primary/10 font-medium text-foreground" : "border-transparent text-muted-foreground hover:bg-accent/50 hover:text-foreground") +
				(props.dim ? " opacity-60" : "")
			}
		>
			<span className="flex w-4 shrink-0 justify-center">{props.mark}</span>
			<span className="min-w-0 flex-1 truncate">{props.label}</span>
			{props.note ? <span className="shrink-0 t-aux text-muted-foreground">{props.note}</span> : null}
			<span className="min-w-6 shrink-0 text-right t-aux tabular-nums" data-facet-count="">{props.count}</span>
		</button>
	);
}

/** 可折叠分区: 标题行带条目数, 折叠后只占一行. */
export function FacetSection(props: { title: string; summary: string; defaultOpen?: boolean; children: ReactNode }): JSX.Element {
	const [open, setOpen] = useState(props.defaultOpen ?? false);
	return (
		<Collapsible open={open} onOpenChange={setOpen} data-facet-section={props.title}>
			<CollapsibleTrigger asChild>
				<button type="button" className="flex h-7 w-full items-center gap-2 rounded-md px-1 t-micro tracking-wider text-muted-foreground hover:bg-accent/40 hover:text-foreground" aria-expanded={open}>
					<ChevronRight className={"size-4 shrink-0 text-muted-foreground transition-transform duration-150" + (open ? " rotate-90" : "")} />
					<span className="flex-1 text-left">{props.title}</span>
					<span className="t-aux font-normal text-muted-foreground">{props.summary}</span>
				</button>
			</CollapsibleTrigger>
			<CollapsibleContent className="flex flex-col gap-1 pb-1 pl-1">{props.children}</CollapsibleContent>
		</Collapsible>
	);
}

const STATUS_ROWS: { value: StatusFilter; label: string; tone: "ok" | "warn" | "idle" | "busy" }[] = [
	{ value: "running", label: "运行中", tone: "ok" },
	{ value: "stopped", label: "已停止 (打开过)", tone: "idle" },
	{ value: "never", label: "未启动 (从未打开)", tone: "idle" },
	{ value: "busy", label: "API 占用", tone: "warn" },
	{ value: "restart", label: "待重启", tone: "warn" },
];

type Facets = {
	envs: Env[];
	query: EnvQuery;
	match: (label: string) => boolean;
	onFilter: (patch: Partial<EnvQuery>) => void;
};

/** 按状态: 五项, 计数来自当前范围, 判据与工具条同一个 matchStatus. */
export function StatusFacet(p: Facets): JSX.Element {
	const rows = STATUS_ROWS.filter((r) => p.match(r.label));
	return (
		<FacetSection title="按状态" summary={rows.length + " 项"} defaultOpen>
			{rows.map((r) => (
				<FacetItem key={r.value} label={r.label} count={p.envs.filter((e) => matchStatus(e, r.value)).length} active={p.query.status === r.value}
					mark={<StateDot tone={r.tone} label={r.label} />} onClick={() => p.onFilter({ status: p.query.status === r.value ? "all" : r.value })} />
			))}
		</FacetSection>
	);
}

/** 按内核: 每个内核一项 (族色圆点, 未安装置灰), 外加 "未指定, 继承设置". 列表来自 GET /api/v1/kernels. */
export function KernelFacet(p: Facets & { kernels: Kernel[]; view: (e: Env) => KernelView }): JSX.Element {
	const views = p.envs.map(p.view);
	const pick = (v: string): void => p.onFilter({ kernel: p.query.kernel === v ? "" : v });
	const rows = p.kernels.filter((k) => p.match(k.label) || p.match(shortName(k.label)));
	const inherit = views.filter((v) => v.how === "继承").length;
	return (
		<FacetSection title="按内核" summary={p.kernels.filter((k) => k.available).length + " / " + p.kernels.length + " 已装"} defaultOpen>
			{rows.map((k) => (
				<FacetItem key={k.id} label={shortName(k.label)} count={views.filter((v) => v.id === k.id).length} active={p.query.kernel === k.id} dim={!k.available}
					note={k.available ? "" : "未安装"} mark={<span className={"size-2 rounded-full " + FAMILY[k.family].dot} />} onClick={() => pick(k.id)} />
			))}
			<FacetItem label="未指定, 继承设置" count={inherit} active={p.query.kernel === "inherit"} mark={<span className="size-2 rounded-full border border-dashed border-muted-foreground" />} onClick={() => pick("inherit")} />
		</FacetSection>
	);
}

/** 按标签: 当前范围内出现过的标签, 按个数降序. */
export function TagFacet(p: Facets): JSX.Element {
	const count = new Map<string, number>();
	for (const e of p.envs) for (const t of e.tags ?? []) count.set(t, (count.get(t) ?? 0) + 1);
	const rows = [...count.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).filter(([t]) => p.match(t));
	return (
		<FacetSection title="按标签" summary={count.size + " 种"}>
			{rows.length ? rows.map(([t, n]) => (
				<FacetItem key={t} label={t} count={n} active={p.query.tag === t} mark={<Tag className="size-3" />} onClick={() => p.onFilter({ tag: p.query.tag === t ? "" : t })} />
			)) : <EmptyState compact text="没有标签" hint="在行的更多菜单里加标签" />}
		</FacetSection>
	);
}

/** 按通道: 人类组与 API 组, 点击切换范围 (与文件夹树的组节点同一个范围). */
export function LaneFacet(props: { all: Env[]; selectedId: string; onSelect: (id: string) => void }): JSX.Element {
	const n = (lane: string): number => props.all.filter((e) => e.lane === lane).length;
	return (
		<FacetSection title="按通道" summary="2 项">
			<FacetItem label="人类组 (可写)" count={n("human")} active={props.selectedId === "h"} mark={<Users className="size-3" />} onClick={() => props.onSelect("h")} />
			<FacetItem label="API 组 (只读)" count={n("agent")} active={props.selectedId === "a"} mark={<Bot className="size-3" />} onClick={() => props.onSelect("a")} />
		</FacetSection>
	);
}
