import type { JSX } from "react";
import { Badge, EmptyState } from "@hx/ui";
import { listEnvs } from "../lib/api";
import { usePoll } from "../lib/hooks";

/** 浏览器环境列表: 显示名字, 指纹种子, 当前代理与运行态. */
export function EnvironmentsPage(): JSX.Element {
	const { result, loading } = usePoll(listEnvs);

	if (loading && !result) return <EmptyState title="读取中" description="正在连接浏览器服务 (8325)" />;
	if (!result?.ok) return <EmptyState title="未连接" description={"浏览器服务不可达: " + (result && "error" in result ? result.error : "")} />;

	const envs = result.data;
	if (envs.length === 0) return <EmptyState title="还没有环境" description="用 hxb new --name demo --seed demo-1 创建一个" />;

	return (
		<div className="flex flex-col gap-2">
			{envs.map((env) => (
				<div key={env.name} className="flex items-center gap-3 rounded-lg border border-border/60 px-3 py-2">
					<span className="font-mono text-[13px]">{env.name}</span>
					<Badge variant={env.running ? "success" : "secondary"}>{env.running ? "运行中" : "未启动"}</Badge>
					<span className="text-[12px] text-muted-foreground">seed {env.seed ?? "-"}</span>
					<span className="ml-auto font-mono text-[12px] text-muted-foreground">{env.proxy ?? "直连"}</span>
				</div>
			))}
		</div>
	);
}
