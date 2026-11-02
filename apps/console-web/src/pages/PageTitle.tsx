/**
 * 全站统一的页头: 标题 20px 600, 说明 12px muted, 右侧放主操作. 包 @hx/ui PageHeader (它的 compact 标题是 15px bold, 层级不够),
 * 标题用 span 自带字号与字重压过 h1 的默认值. 刻度: 页标题 20 / 卡片标题 14 / 正文 13 / 辅助 12 / 侧栏分区 11.
 */
import type { JSX, ReactNode } from "react";
import { PageHeader } from "@hx/ui";

export function PageTitle(props: { title: string; desc?: string; right?: ReactNode; extra?: ReactNode }): JSX.Element {
	return (
		<PageHeader
			compact
			className="mb-0"
			title={<span className="t-page block" data-page-title="">{props.title}</span>}
			desc={props.desc}
			right={props.right}
			extra={props.extra}
		/>
	);
}
