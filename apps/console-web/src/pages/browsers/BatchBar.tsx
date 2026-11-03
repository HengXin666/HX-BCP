import type { JSX } from "react";
import { FolderInput, Play, Square, Tags, Trash2, X } from "lucide-react";
import { Button, ButtonGroup, Card, CardContent } from "@hx/ui";

export type BatchAction = "start" | "stop" | "move" | "tags" | "delete";

/**
 * 批量操作条: 选中人类组浏览器后出现在表格上方. API 组的行不可勾选, 所以这里只会作用于人类组.
 * @hx/ui 没有现成的批量条布局, 用 Card + ButtonGroup 组合, 见 .review/hx-ui-issue.md.
 */
export function BatchBar(props: {
	count: number;
	busy: boolean;
	onAction: (a: BatchAction) => void;
	onClear: () => void;
}): JSX.Element | null {
	if (props.count === 0) return null;
	return (
		<Card variant="muted" data-batch-bar={props.count}>
			<CardContent className="flex flex-wrap items-center gap-2 px-4 py-2">
				<span className="t-body">已选 {props.count} 个</span>
				<ButtonGroup>
					<Button size="sm" variant="outline" disabled={props.busy} data-batch="start" onClick={() => props.onAction("start")}><Play className="size-4" />启动</Button>
					<Button size="sm" variant="outline" disabled={props.busy} data-batch="stop" onClick={() => props.onAction("stop")}><Square className="size-4" />停止</Button>
					<Button size="sm" variant="outline" disabled={props.busy} data-batch="move" onClick={() => props.onAction("move")}><FolderInput className="size-4" />移动</Button>
					<Button size="sm" variant="outline" disabled={props.busy} data-batch="tags" onClick={() => props.onAction("tags")}><Tags className="size-4" />加标签</Button>
					<Button size="sm" variant="destructive" disabled={props.busy} data-batch="delete" onClick={() => props.onAction("delete")}><Trash2 className="size-4" />删除</Button>
				</ButtonGroup>
				<Button size="sm" variant="ghost" className="ml-auto" onClick={props.onClear}><X className="size-4" />取消选择</Button>
			</CardContent>
		</Card>
	);
}
