/** 页面内的区块: 区块标题 + 可选一句说明 + 右侧操作, 内容与标题间 12px. */
import type { JSX, ReactNode } from "react";

export function Block(props: { title: string; desc?: ReactNode; right?: ReactNode; children: ReactNode }): JSX.Element {
	return (
		<section className="flex min-w-0 flex-col gap-3">
			<header className="flex flex-wrap items-baseline gap-2">
				<h2 className="t-section">{props.title}</h2>
				{props.desc ? <p className="t-aux text-muted-foreground">{props.desc}</p> : null}
				{props.right ? <div className="ml-auto flex items-center gap-2">{props.right}</div> : null}
			</header>
			{props.children}
		</section>
	);
}
