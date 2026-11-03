/** 详情抽屉各页共用的小件: 只读判定, 时间格式, 写按钮 (只读时 disabled 带提示), 小节卡片. */
import type { JSX, ReactNode } from "react";
import { Button } from "@hx/ui";
import type { Env } from "../../lib/api";

export const RO_HINT = "由 api 或 mcp 管理";

/** API 组或后端标了 readonly 的环境, 控制台只读. */
export function readOnly(env: Env): boolean {
	return env.lane === "agent" || env.readonly;
}

/** 毫秒时间戳转本地时间; 0 或缺失返回 null, 由 KV 显示 "未上报". */
export function ts(ms: number | undefined | null): string | null {
	return ms ? new Date(ms).toLocaleString() : null;
}

/** 写按钮: 只读环境一律 disabled, 外层 span 带提示 (disabled 按钮自己收不到悬停). */
export function WriteButton(props: { ro: boolean; onClick: () => void; busy?: boolean; children: ReactNode; variant?: "outline" | "destructive" | "default" }): JSX.Element {
	return (
		<span title={props.ro ? RO_HINT : undefined} data-write-btn="" data-ro={props.ro ? "1" : "0"}>
			<Button size="sm" variant={props.variant ?? "outline"} disabled={props.ro || props.busy} onClick={props.onClick}>
				{props.children}
			</Button>
		</span>
	);
}

/** 页内小节标题. */
export function Section(props: { title: string; right?: ReactNode; children: ReactNode }): JSX.Element {
	return (
		<section className="flex flex-col gap-2 rounded-xl border border-border bg-card p-4">
			<header className="flex items-center gap-2">
				<h3 className="t-card">{props.title}</h3>
				<div className="ml-auto flex items-center gap-2">{props.right}</div>
			</header>
			{props.children}
		</section>
	);
}
