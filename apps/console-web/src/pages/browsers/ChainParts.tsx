/** 链路的视觉零件: 段图标, 段节点, 段间连线, 状态角标, 键值行. 表格链路条, 详情面板, 状态页共用. */
import type { JSX, ReactNode } from "react";
import { Crosshair, Database, Fingerprint, Globe, type LucideIcon } from "lucide-react";
import { Badge } from "@hx/ui";
import { STATE_TEXT, type SegKey, type SegState } from "../../lib/chain";

export const SEG_ICON: Record<SegKey, LucideIcon> = { env: Fingerprint, cache: Database, exit: Globe, target: Crosshair };

/** 节点外观: 正常绿, 待确认琥珀, 故障红, 未挂载虚线灰, 未运行实线灰, 未上报点线灰. */
const NODE: Record<SegState, string> = {
	ok: "border-success/50 bg-success/10 text-success",
	warn: "border-warning/50 bg-warning/10 text-warning",
	bad: "border-destructive/60 bg-destructive/10 text-destructive",
	off: "border-dashed border-border text-muted-foreground/60",
	idle: "border-border bg-muted/40 text-muted-foreground",
	na: "border-dotted border-border text-muted-foreground/70",
};

const BADGE: Record<SegState, "success" | "warning" | "destructive" | "secondary" | "outline"> = {
	ok: "success",
	warn: "warning",
	bad: "destructive",
	off: "outline",
	idle: "secondary",
	na: "outline",
};

/** 段节点: 圆角方块里一个图标, 颜色与边框表达状态. data-seg 与 data-state 供验收脚本读. */
export function SegNode(props: { k: SegKey; state: SegState; size?: "xs" | "sm" | "md" }): JSX.Element {
	const Icon = SEG_ICON[props.k];
	const box = props.size === "md" ? "size-8 rounded-lg" : props.size === "xs" ? "size-5 rounded" : "size-6 rounded-md";
	return (
		<span
			className={"flex shrink-0 items-center justify-center border transition-colors duration-150 " + box + " " + NODE[props.state]}
			data-seg={props.k}
			data-state={props.state}
		>
			<Icon className={props.size === "md" ? "size-4" : props.size === "xs" ? "size-3" : "size-4"} aria-hidden />
		</span>
	);
}

/** 段间连线: 两端都正常是实线绿, 后一段未挂载是虚线, 任一端故障是红, 其余灰. */
export function Wire(props: { from: SegState; to: SegState; vertical?: boolean; label?: string; short?: boolean }): JSX.Element {
	const bad = props.from === "bad" || props.to === "bad";
	const live = props.from === "ok" && props.to === "ok";
	const color = bad ? "border-destructive/70" : live ? "border-success/70" : "border-border";
	const style = props.to === "off" || props.from === "off" ? "border-dashed" : "border-solid";
	if (props.vertical) {
		return (
			<div className="flex items-stretch gap-3 pl-7" aria-hidden>
				<span className={"min-h-6 border-l-2 " + color + " " + style} />
				{props.label ? <span className="self-center py-1 font-mono t-aux text-muted-foreground">{props.label}</span> : null}
			</div>
		);
	}
	return <span className={"h-0 shrink-0 border-t-2 " + (props.short ? "w-2 " : "w-3 ") + color + " " + style} aria-hidden />;
}

/** 状态角标, 文字固定为 STATE_TEXT. */
export function StateBadge(props: { state: SegState; text?: string }): JSX.Element {
	return (
		<Badge variant={BADGE[props.state]} className="shrink-0" data-state={props.state}>
			{props.text ?? STATE_TEXT[props.state]}
		</Badge>
	);
}

/** 键值网格: 左标签右值, 值单行截断并给 title; 值缺失写未上报 (灰). */
export function KV(props: { rows: [string, ReactNode | string | number | null | undefined][] }): JSX.Element {
	return (
		<dl className="grid grid-cols-[6.5rem_minmax(0,1fr)] gap-x-3 gap-y-2 t-aux">
			{props.rows.map(([k, v]) => {
				const missing = v === null || v === undefined || v === "";
				return (
					<div key={k} className="contents">
						<dt className="text-muted-foreground">{k}</dt>
						<dd className={"min-w-0 truncate font-mono tabular-nums " + (missing ? "text-muted-foreground/70" : "text-foreground")} title={typeof v === "string" ? v : undefined}>
							{missing ? "未上报" : v}
						</dd>
					</div>
				);
			})}
		</dl>
	);
}

/** 一段的卡片: 头部 图标 + 段名 + 一句当前值 + 状态角标, 身体是数字, 底部一句 "换掉会影响什么". */
export function SegCard(props: { k: SegKey; title: string; state: SegState; summary: string; impact: string; action?: ReactNode; children: ReactNode }): JSX.Element {
	return (
		<section className="rounded-xl border border-border bg-card transition-colors duration-150 hover:border-border" data-seg-card={props.k} data-state={props.state}>
			<header className="flex items-center gap-3 px-4 pt-4">
				<SegNode k={props.k} state={props.state} size="md" />
				<div className="min-w-0 flex-1">
					<div className="flex items-center gap-2">
						<span className="t-card">{props.title}</span>
						<StateBadge state={props.state} />
					</div>
					<p className="truncate t-aux text-muted-foreground" title={props.summary}>{props.summary}</p>
				</div>
				{props.action}
			</header>
			<div className="px-4 py-3 pl-14">{props.children}</div>
			<footer className="border-t border-border/50 px-4 py-2 pl-14 t-aux text-muted-foreground">{props.impact}</footer>
		</section>
	);
}
