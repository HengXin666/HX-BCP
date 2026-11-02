import { useState, type JSX } from "react";
import { ChevronDown, Folder, Lock } from "lucide-react";
import { Checkbox, Collapsible, CollapsibleContent, CollapsibleTrigger, EmptyState } from "@hx/ui";
import type { Env } from "../../lib/api";
import type { Seg } from "../../lib/chain";
import { ChainStrip } from "./ChainStrip";
import { KernelChip } from "./KernelBits";
import { ActionCell, NameCell, StatusCell, TagsCell, proxyLabel, selectable, whenShort, whereLabel, type EnvAction } from "./EnvCells";

/** 卡片展开后的一行: 左标签右值, 值单行截断. */
function Field(props: { label: string; children: JSX.Element | string }): JSX.Element {
	return (
		<>
			<dt className="text-muted-foreground">{props.label}</dt>
			<dd className="min-w-0 truncate leading-5">{props.children}</dd>
		</>
	);
}

/** 一张卡: 第一行 勾选 + 名称 + 展开箭头, 第二行 状态 + 位置, 第三行 链路条 (点开看每段); 点开显示代理, 标签, 最近打开, id 与全部操作. */
function EnvCard(props: { env: Env; picked: boolean; segs: Seg[]; onOpen: () => void; onPick: (v: boolean) => void; onAction: (a: EnvAction, env: Env) => void }): JSX.Element {
	const { env } = props;
	const [open, setOpen] = useState(false);
	return (
		<li className="rounded-xl border border-border bg-card transition-colors hover:bg-accent/30" data-env-card="">
			<Collapsible open={open} onOpenChange={setOpen}>
				<div className="flex items-start gap-2 px-3 py-3">
					<span className="flex h-5 items-center">
						{selectable(env) ? (
							<Checkbox checked={props.picked} onCheckedChange={(v) => props.onPick(v === true)} aria-label={"选择 " + env.name} />
						) : (
							<Lock className="size-4 text-muted-foreground" aria-label="API 组只读" />
						)}
					</span>
					<CollapsibleTrigger asChild>
						<button type="button" className="flex min-w-0 flex-1 flex-col gap-2 text-left" aria-label={(open ? "收起 " : "展开 ") + env.name}>
							<span className="flex w-full min-w-0 items-center gap-2 leading-5">
								<span className="min-w-0 flex-1"><NameCell env={env} /></span>
								<ChevronDown className={"size-4 shrink-0 text-muted-foreground transition-transform duration-150" + (open ? " rotate-180" : "")} />
							</span>
							<span className="flex w-full min-w-0 items-center gap-3 t-aux leading-5">
								<StatusCell env={env} />
								<KernelChip env={env} />
								<span className="flex min-w-0 items-center gap-1 text-muted-foreground" data-env-where="">
									<Folder className="size-4 shrink-0" />
									<span className="truncate">{whereLabel(env)}</span>
								</span>
							</span>
						</button>
					</CollapsibleTrigger>
				</div>
				<div className="-mt-1 flex items-center gap-2 pb-2 pl-8 pr-3 t-aux text-muted-foreground">
					<ChainStrip segs={props.segs} label={env.name} onOpen={props.onOpen} />
					<span className="min-w-0 truncate">{props.segs.filter((s) => s.state !== "off").length} / 4 段在链路上</span>
				</div>
				<CollapsibleContent>
					<dl className="grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-2 gap-y-2 border-t border-border/60 px-3 py-3 t-aux">
						<Field label="代理"><span className="font-mono">{proxyLabel(env)}</span></Field>
						<Field label="标签"><TagsCell tags={env.tags ?? []} /></Field>
						<Field label="最近打开">{whenShort(env.last_open_at)}</Field>
						<Field label="id"><span className="font-mono">{env.id}</span></Field>
					</dl>
					<div className="flex justify-end border-t border-border/60 px-2 py-2">
						<ActionCell env={env} onAction={props.onAction} />
					</div>
				</CollapsibleContent>
			</Collapsible>
		</li>
	);
}

/** 窄屏列表: 顶部 全选本页, 下面一行一张卡. 替代 "隐藏列" 的做法, 每行都看得到名称, 状态, 位置. */
export function EnvCards(props: {
	rows: Env[];
	selected: ReadonlySet<string>;
	onSelect: (ids: string[], checked: boolean) => void;
	onAction: (a: EnvAction, env: Env) => void;
	chainOf: (env: Env) => Seg[];
	onOpen: (env: Env) => void;
}): JSX.Element {
	const pickable = props.rows.filter(selectable).map((e) => e.id);
	const picked = pickable.filter((id) => props.selected.has(id)).length;
	const head = picked === 0 ? false : picked === pickable.length ? true : "indeterminate";
	if (!props.rows.length) return <EmptyState text="没有符合条件的浏览器" hint="换个关键字, 或在侧栏与工具条里放宽筛选" />;
	return (
		<div className="flex flex-col gap-2">
			<label className="flex items-center gap-2 px-3 t-aux text-muted-foreground">
				<Checkbox checked={head} disabled={!pickable.length} onCheckedChange={(v) => props.onSelect(pickable, v === true)} />
				全选本页人类组 ({pickable.length})
			</label>
			<ul className="flex flex-col gap-2">
				{props.rows.map((e) => (
					<EnvCard key={e.id} env={e} segs={props.chainOf(e)} onOpen={() => props.onOpen(e)} picked={props.selected.has(e.id)} onPick={(v) => props.onSelect([e.id], v)} onAction={props.onAction} />
				))}
			</ul>
		</div>
	);
}
