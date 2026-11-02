import type { JSX } from "react";
import { Bot, Check, Cpu, Ellipsis, FolderInput, Lock, PanelRightOpen, Pencil, Play, RefreshCw, SlidersHorizontal, Square, Tags, Trash2, X } from "lucide-react";
import {
	Badge,
	Button,
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
	SessionStatusDot,
	Spinner,
} from "@hx/ui";
import type { Env } from "../../lib/api";
import { isRunning } from "../../lib/env-query";
import { usePending } from "./useEnvActions";

export type EnvAction = "start" | "stop" | "edit" | "rename" | "move" | "tags" | "kernel" | "delete" | "detail";

const MAX_TAGS = 2;
const HIT = "size-7";

/** 只有人类组的行可选可写, API 组的行只读. */
export function selectable(env: Env): boolean {
	return !env.readonly && env.lane === "human";
}

/** 代理显示: 没有代理写直连. */
export function proxyLabel(env: Env): string {
	const p = env.proxy;
	if (!p || !p.host) return "直连";
	return (p.type ?? "http") + "://" + p.host + (p.port ? ":" + p.port : "");
}

/** 表格里的短时间: 月-日 时:分, 全文进 title. */
export function whenShort(ms: number | undefined): JSX.Element {
	if (!ms) return <span>-</span>;
	const d = new Date(ms);
	const two = (n: number): string => String(n).padStart(2, "0");
	return <span title={d.toLocaleString()}>{two(d.getMonth() + 1) + "-" + two(d.getDate()) + " " + two(d.getHours()) + ":" + two(d.getMinutes())}</span>;
}

/** 位置: API 组带项目名, 再接文件夹 (空文件夹名写根目录). */
export function whereLabel(env: Env): string {
	return [env.lane === "agent" ? env.project : "", env.folder || "根目录"].filter(Boolean).join(" / ");
}

/** 毫秒时间戳转本地时间, 0 写横线. */
export function when(ms: number | undefined): string {
	return ms ? new Date(ms).toLocaleString() : "-";
}

/** 名称格: 单行截断, title 给全文. 宽度由列 (w-full max-w-0) 决定, 吃掉其余列剩下的空间. */
export function NameCell({ env }: { env: Env }): JSX.Element {
	return (
		<span className="block truncate font-mono t-body" title={env.name + "\n" + env.id}>
			{env.name}
		</span>
	);
}

/**
 * 状态格: 四种状态四种样子, 不再一片灰. API 占用 = 黄 badge + 机器人图标; 运行中 = 绿点 + 绿 badge;
 * 待重启 = 描边 badge + 刷新图标; 已停止 = 空心点 + 灰字 (不用 badge).
 */
export function StatusCell({ env }: { env: Env }): JSX.Element {
	if (env.in_use) {
		return (
			<span data-status-badge="busy" className="flex items-center gap-2 animate-in fade-in-0 duration-200" title={"api 或 mcp 自 " + when(env.in_use_since) + " 起占用"}>
				<SessionStatusDot status="pending-input" />
				<Badge variant="warning"><Bot className="size-3" />API 占用</Badge>
			</span>
		);
	}
	// 待重启只给图标 (title 给文字): 列宽省一个 badge, 表格在 1440 与 390 下都不溢出
	const restart = env.pending_restart ? <span title="待重启" aria-label="待重启"><RefreshCw className="size-4 text-warning" /></span> : null;
	if (isRunning(env)) {
		return (
			<span data-status-badge="running" className="flex items-center gap-2 animate-in fade-in-0 duration-200">
				<SessionStatusDot status="running" className="overflow-hidden rounded-full" />
				<Badge variant="success">运行中</Badge>
				{restart}
			</span>
		);
	}
	return (
		<span data-status-badge="idle" className="flex items-center gap-2 t-aux text-muted-foreground animate-in fade-in-0 duration-200">
			<SessionStatusDot status="idle" />
			<span title={env.last_open_at ? "上次打开 " + when(env.last_open_at) : "创建于 " + when(env.created_at) + ", 从未打开"}>{env.last_open_at ? "已停止" : "未启动"}</span>
			{restart}
		</span>
	);
}

export function TagsCell({ tags }: { tags: string[] }): JSX.Element {
	if (!tags.length) return <span className="text-muted-foreground">-</span>;
	return (
		<span className="flex gap-1" title={tags.join(", ")}>
			{tags.slice(0, MAX_TAGS).map((t) => <Badge key={t} variant="outline" className="max-w-12 truncate px-1">{t}</Badge>)}
			{tags.length > MAX_TAGS ? <Badge variant="secondary" className="px-1">+{tags.length - MAX_TAGS}</Badge> : null}
		</span>
	);
}

/** 一个 32x32 命中区的图标按钮, 文字进 aria-label 与 title. */
function IconAction(props: { label: string; onClick: () => void; children: JSX.Element; danger?: boolean; disabled?: boolean }): JSX.Element {
	return (
		<Button
			disabled={props.disabled}
			variant="ghost"
			size="icon-sm"
			className={HIT + (props.danger ? " text-destructive" : "")}
			aria-label={props.label}
			title={props.label}
			onClick={props.onClick}
		>
			{props.children}
		</Button>
	);
}

/** 操作格: 人类组给 启停 / 详情 两个图标按钮 + 更多菜单 (编辑, 改名, 移动, 标签, 内核, 删除), 列宽 <= 120px; API 组给详情 + 锁图标只读. */
export function ActionCell({ env, onAction }: { env: Env; onAction: (a: EnvAction, env: Env) => void }): JSX.Element {
	if (!selectable(env)) {
		return (
			<span className="inline-flex items-center justify-end gap-1 t-aux text-muted-foreground" title="API 组由 api 或 mcp 管理">
				<IconAction label="详情" onClick={() => onAction("detail", env)}>
					<PanelRightOpen className="size-4" />
				</IconAction>
				<Lock className="size-4" />只读
			</span>
		);
	}
	const on = isRunning(env);
	const p = usePending(env.id);
	const busy = p?.phase === "busy";
	// 启停按钮: 请求中转圈 (data-loading, 选择器 [data-action-state=busy] svg.animate-spin), 成功短暂对勾, 失败短暂叉
	const icon = busy ? <Spinner className="size-4" data-loading="" /> : p?.phase === "done" ? <Check className="size-4 text-success animate-in zoom-in-50 fade-in-0 duration-200" /> : p?.phase === "fail" ? <X className="size-4 text-destructive" /> : on ? <Square className="size-4" /> : <Play className="size-4" />;
	return (
		<span className="inline-flex items-center justify-end gap-1" data-action-state={p?.phase ?? "idle"}>
			<IconAction label={busy ? (p?.op === "stop" ? "停止中" : p?.op === "delete" ? "删除中" : "启动中") : on ? "停止" : "启动"} disabled={busy} onClick={() => onAction(on ? "stop" : "start", env)}>
				{icon}
			</IconAction>
			<IconAction label="详情" onClick={() => onAction("detail", env)}>
				<PanelRightOpen className="size-4" />
			</IconAction>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button variant="ghost" size="icon-sm" className={HIT} aria-label="更多操作" title="更多操作">
						<Ellipsis className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuItem onSelect={() => onAction("edit", env)}><SlidersHorizontal className="size-4" />编辑配置</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => onAction("rename", env)}><Pencil className="size-4" />改名</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => onAction("move", env)}><FolderInput className="size-4" />移动到文件夹</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => onAction("tags", env)}><Tags className="size-4" />编辑标签</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => onAction("kernel", env)}><Cpu className="size-4" />切换内核</DropdownMenuItem>
					<DropdownMenuSeparator />
					<DropdownMenuItem className="text-destructive focus:text-destructive" onSelect={() => onAction("delete", env)}><Trash2 className="size-4" />删除</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
		</span>
	);
}
