import { useMemo, useState, type JSX } from "react";
import { Layers, Search } from "lucide-react";
import { InputGroup, InputGroupAddon, InputGroupInput, StateDot, TreeNav, type TreeNode, EmptyState } from "@hx/ui";
import type { Env } from "../../lib/api";
import { isRunning, type EnvQuery } from "../../lib/env-query";
import type { Kernel, KernelView } from "../../lib/kernels";
import { branchIds, filterTree } from "../../lib/tree";
import { FacetItem, FacetSection, KernelFacet, LaneFacet, StatusFacet, TagFacet } from "./RailFacets";

/** 分组栏底部的四色图例: 运行中, API 占用, 待重启, 已停止 (含未启动), 计数是全部浏览器. */
function Overview({ envs }: { envs: Env[] }): JSX.Element {
	const rows: { label: string; n: number; tone: "ok" | "warn" | "idle" | "busy" }[] = [
		{ label: "运行中", n: envs.filter(isRunning).length, tone: "ok" },
		{ label: "API 占用", n: envs.filter((e) => e.in_use).length, tone: "busy" },
		{ label: "待重启", n: envs.filter((e) => e.pending_restart).length, tone: "warn" },
		{ label: "已停止", n: envs.filter((e) => !isRunning(e)).length, tone: "idle" },
	];
	return (
		<div className="flex shrink-0 flex-col gap-1 border-t border-border pt-2">
			{rows.map((r) => (
				<span key={r.label} className="flex items-center justify-between px-1 t-aux">
					<span className="flex items-center gap-2"><StateDot tone={r.tone} label={r.label} /><span aria-hidden="true">{r.label}</span></span>
					<span className="tabular-nums text-muted-foreground">{r.n}</span>
				</span>
			))}
		</div>
	);
}

/**
 * 分组栏, 多维导航: 顶部搜索 (同时过滤文件夹树与各分区条目), 全部, 然后五个可折叠分区:
 * 按文件夹 (树, 人类组下第一项是 "未分类 (根目录)" 带占比), 按状态, 按内核, 按标签, 按通道. 底部四色图例.
 * 文件夹与通道切换范围 (hash 里的 scope), 状态, 内核, 标签是筛选 (hash 里的 status, kernel, tag), 与工具条同一套条件.
 * 状态, 内核, 标签的计数按当前范围算. 宽屏放在 CollapsibleRail 里, 窄屏放在左侧 Sheet 里, 两处共用.
 */
export function FolderRail(props: {
	nodes: TreeNode[];
	all: Env[];
	inside: Env[];
	selectedId: string;
	onSelect: (id: string) => void;
	query: EnvQuery;
	onFilter: (patch: Partial<EnvQuery>) => void;
	kernels: Kernel[];
	view: (e: Env) => KernelView;
}): JSX.Element {
	const [text, setText] = useState("");
	const [expanded, setExpanded] = useState<string[]>(["h", "a"]);
	const shown = useMemo(() => filterTree(props.nodes, text), [props.nodes, text]);
	const open = text.trim() ? branchIds(shown) : expanded;
	const q = text.trim().toLowerCase();
	const match = (label: string): boolean => !q || label.toLowerCase().includes(q);
	const toggle = (id: string, isOpen: boolean): void =>
		setExpanded((cur) => (isOpen ? [...new Set([...cur, id])] : cur.filter((x) => x !== id)));
	const facets = { envs: props.inside, query: props.query, match, onFilter: props.onFilter };
	const plain = props.query.status === "all" && !props.query.kernel && !props.query.tag;

	return (
		<div className="flex h-full min-w-0 flex-col gap-2" data-rail="">
			<InputGroup>
				<InputGroupAddon><Search className="size-4" /></InputGroupAddon>
				<InputGroupInput value={text} onChange={(e) => setText(e.target.value)} placeholder="搜索文件夹, 内核, 标签" aria-label="搜索分组" />
			</InputGroup>
			<div className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto overscroll-contain pr-1">
				<FacetItem
					label="全部浏览器"
					count={props.all.length}
					active={props.selectedId === "all" && plain}
					mark={<Layers className="size-4" />}
					onClick={() => props.onSelect("all")}
				/>
				<FacetSection title="按文件夹" summary={props.nodes.reduce((n, x) => n + (x.children?.length ?? 0), 0) + " 个"} defaultOpen>
					{shown.length ? (
						<TreeNav nodes={shown} expandedIds={open} onToggle={toggle} selectedId={props.selectedId} onSelect={(n) => props.onSelect(n.id)} ariaLabel="浏览器分组" />
					) : (
						<EmptyState compact text="没有匹配的文件夹" hint="换个关键字, 或清空搜索" />
					)}
				</FacetSection>
				<StatusFacet {...facets} />
				<KernelFacet {...facets} kernels={props.kernels} view={props.view} />
				<TagFacet {...facets} />
				<LaneFacet all={props.all} selectedId={props.selectedId} onSelect={props.onSelect} />
			</div>
			<Overview envs={props.all} />
		</div>
	);
}
