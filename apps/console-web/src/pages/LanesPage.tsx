import type { JSX } from "react";
import { Badge, Card, CardContent } from "@hx/ui";

const ROWS = [
	{ key: "发起者", human: "人 (控制台 / CLI)", agent: "程序 (HTTP API)" },
	{ key: "准入", human: "本机人类令牌", agent: "项目名必填" },
	{ key: "分组", human: "按时间", agent: "按项目名" },
	{ key: "Agent 能否接管", human: "否", agent: "是 (仅自己的项目)" },
	{ key: "人能否查看", human: "是", agent: "只读" },
] as const;

/** 通道说明: 两条链路互不相通的判据表. */
export function LanesPage(): JSX.Element {
	return (
		<Card variant="muted">
			<CardContent className="flex flex-col gap-2 pt-3">
				<div className="grid grid-cols-[auto_1fr_1fr] items-center gap-x-4 gap-y-2 text-[13px]">
					<span />
					<Badge variant="secondary">人类通道</Badge>
					<Badge variant="default">Agent 通道</Badge>
					{ROWS.map((row) => (
						<div key={row.key} className="contents">
							<span className="text-[12px] text-muted-foreground">{row.key}</span>
							<span>{row.human}</span>
							<span>{row.agent}</span>
						</div>
					))}
				</div>
				<div className="mt-2 text-[12px] text-muted-foreground">
					判据唯一实现在 packages/lane/lane.py, 接口层与控制台都引用它.
				</div>
			</CardContent>
		</Card>
	);
}
