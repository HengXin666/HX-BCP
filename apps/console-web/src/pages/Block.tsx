/** 页面内的区块: 15px/600 区块标题 + 可选一句 12px 说明, 内容与标题间 12px; 区块之间由父容器 gap-6 (24px) 隔开. */
import type { JSX, ReactNode } from "react";

export function Block(props: { title: string; desc?: string; right?: ReactNode; children: ReactNode }): JSX.Element {
	return (
		<section className="flex min-w-0 flex-col gap-3" data-block={props.title}>
			<header className="flex flex-wrap items-baseline gap-2">
				<h2 className="t-section">{props.title}</h2>
				{props.desc ? <p className="t-aux text-muted-foreground">{props.desc}</p> : null}
				{props.right ? <div className="ml-auto flex items-center gap-2">{props.right}</div> : null}
			</header>
			{props.children}
		</section>
	);
}
