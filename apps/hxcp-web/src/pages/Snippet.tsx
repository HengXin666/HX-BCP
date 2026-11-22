/**
 * 等宽代码片段 + 复制按钮. 不用 @hx/ui 的 CodeBlock: 它带 shiki 全部语法, 打包多 10 MB,
 * 而这里的片段 (命令, 头, JSON) 不需要语法高亮.
 */
import type { JSX } from "react";
import { Copy } from "lucide-react";
import { Button, toast } from "@hx/ui";
import { copyText } from "../lib/format";

export function Snippet(props: { code: string; title?: string; maxHeight?: string }): JSX.Element {
	const copy = (): void => void copyText(props.code).then((ok) => (ok ? toast.success("已复制") : toast.error("复制失败")));
	return (
		<div className="min-w-0 overflow-hidden rounded-md border border-border-muted bg-muted/40">
			<div className="flex h-8 items-center gap-2 border-b border-border-muted px-3">
				<span className="t-aux text-muted-foreground">{props.title ?? ""}</span>
				<Button size="icon-xs" variant="ghost" className="ml-auto" onClick={copy} aria-label="复制">
					<Copy className="size-3.5" />
				</Button>
			</div>
			<pre className="m-0 overflow-auto whitespace-pre-wrap break-all p-3 font-mono t-aux" style={{ maxHeight: props.maxHeight ?? "360px" }}>
				{props.code}
			</pre>
		</div>
	);
}
