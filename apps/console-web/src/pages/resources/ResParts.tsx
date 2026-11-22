/** 资源三页共用的小件: 工具条 (搜索 + 右侧按钮), 空态, 行内图标按钮, 删除确认, 表单行, 时间格式. */
import type { JSX, ReactNode } from "react";
import { Plus, Search } from "lucide-react";
import { Button, EmptyState, Input, confirmDialog, toast } from "@hx/ui";
import type { Result } from "../../lib/api";
import { optimistic } from "../../lib/cache";

export function ResToolbar(props: { query: string; onQuery: (q: string) => void; placeholder: string; count: number; children?: ReactNode }): JSX.Element {
	return (
		<div className="flex flex-wrap items-center gap-2" data-res-toolbar="">
			<div className="relative w-full max-w-xs">
				<Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
				<Input className="pl-8" value={props.query} placeholder={props.placeholder} onChange={(e) => props.onQuery(e.target.value)} />
			</div>
			<span className="t-aux tabular-nums text-muted-foreground" data-res-count={props.count}>{"共 " + props.count + " 条"}</span>
			<div className="ml-auto flex flex-wrap items-center gap-2">{props.children}</div>
		</div>
	);
}

/**
 * 资源表的空态, 三页同一套: 有搜索词时是 "没有匹配" 加清除搜索, 库为空时是一句用途说明加新建按钮.
 */
export function ResEmpty(props: { kind: string; query: string; onClear: () => void; hint: string; onCreate: () => void }): JSX.Element {
	if (props.query.trim())
		return <EmptyState text={"没有匹配的" + props.kind} hint="换个关键字试试" action={<Button size="sm" variant="outline" onClick={props.onClear}>清除搜索</Button>} />;
	return <EmptyState text={props.kind + "库是空的"} hint={props.hint} action={<Button size="sm" onClick={props.onCreate}><Plus className="size-4" />{"新建" + props.kind}</Button>} />;
}

export function IconBtn(props: { label: string; onClick: () => void; children: ReactNode; disabled?: boolean }): JSX.Element {
	return (
		<Button size="icon-sm" variant="ghost" title={props.label} aria-label={props.label} disabled={props.disabled} onClick={props.onClick}>
			{props.children}
		</Button>
	);
}

/**
 * 删除前确认, 确认后执行并提示结果. 返回是否删掉了.
 * 给了 drop (列表缓存键与行 id) 时走乐观更新: 确认后该行立即从列表消失, 失败回滚 (见 lib/cache.optimistic).
 */
export async function confirmRemove(kind: string, name: string, remove: () => Promise<Result<unknown>>, drop?: { key: string; id: string }): Promise<boolean> {
	const yes = await confirmDialog({ title: "删除" + kind + " " + name + "?", message: "删除后不可恢复.", confirmLabel: "删除", cancelLabel: "保留", variant: "danger" });
	if (!yes) return false;
	const r = drop ? await optimistic<{ id: string }[], unknown>(drop.key, (list) => list.filter((x) => x.id !== drop.id), remove) : await remove();
	if (r.ok) toast.success("已删除 " + name);
	else toast.error("删除失败: " + r.error);
	return r.ok;
}

/** 提交写请求并提示, 成功返回 true. */
export async function submit(label: string, fn: () => Promise<Result<unknown>>): Promise<boolean> {
	const r = await fn();
	if (r.ok) toast.success(label + " 完成");
	else toast.error(label + " 失败: " + r.error);
	return r.ok;
}

export function Row(props: { label: string; hint?: string; children: ReactNode }): JSX.Element {
	return (
		<label className="flex flex-col gap-1 t-aux text-muted-foreground">
			{props.label}
			{props.children}
			{props.hint ? <span className="t-aux text-muted-foreground/80">{props.hint}</span> : null}
		</label>
	);
}

export function fmtTime(ms: number | undefined): string {
	return ms ? new Date(ms).toLocaleString() : "-";
}

/** 逗号或空白分隔转数组, 去重去空. */
export function splitList(text: string): string[] {
	return [...new Set(text.split(/[,\s]+/).map((t) => t.trim()).filter(Boolean))];
}

/** 列表按关键字过滤 (不分大小写, 匹配任一字段). */
export function matchAny(q: string, ...fields: (string | undefined)[]): boolean {
	const k = q.trim().toLowerCase();
	return !k || fields.some((f) => (f ?? "").toLowerCase().includes(k));
}
