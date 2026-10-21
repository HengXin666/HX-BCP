import type { JSX } from "react";
import { Badge, EmptyState } from "@hx/ui";
import { listEnvs } from "../lib/api";
import type { EnvProxy } from "../lib/api";
import { usePoll } from "../lib/hooks";

/** 代理显示为 type://host:port, 没有 host 时为直连. */
function proxyText(p: EnvProxy | undefined): string {
	if (!p || !p.host) return "直连";
	return (p.type ?? "http") + "://" + p.host + (p.port ? ":" + String(p.port) : "");
}

/** 浏览器环境列表: 显示名字, 分组, 平台, 指纹内核, 代理, 运行态与可见性. */
export function EnvironmentsPage(): JSX.Element {
	const { result, loading } = usePoll(listEnvs);

	if (loading && !result) return <EmptyState text="读取中" hint="正在连接浏览器服务 (8325)" />;
	if (!result?.ok) return <EmptyState text="未连接" hint={"浏览器服务不可达: " + (result && "error" in result ? result.error : "")} />;

	const envs = result.data;
	if (envs.length === 0) return <EmptyState text="还没有环境" hint="用 hxb new --name demo 创建一个" />;

	return (
		<div className="flex flex-col gap-2">
			{envs.map((env) => {
				const running = env.runtime != null;
				return (
					<div key={env.id} className="flex flex-wrap items-center gap-3 rounded-lg border border-border/60 px-3 py-2">
						<span className="font-mono text-[13px]">{env.name}</span>
						<Badge variant={running ? "success" : "secondary"}>{running ? "运行中" : "未启动"}</Badge>
						<Badge variant={env.visible ? "default" : "secondary"}>{env.visible ? "可见" : "不可见"}</Badge>
						<span className="text-[12px] text-muted-foreground">{env.visible_reason ?? "-"}</span>
						<span className="text-[12px] text-muted-foreground">分组 {env.group || "-"}</span>
						<span className="text-[12px] text-muted-foreground">平台 {env.platform || "-"}</span>
						<span className="text-[12px] text-muted-foreground">指纹 {env.fingerprint?.browser ?? "-"}</span>
						<span className="text-[12px] text-muted-foreground">打开 {env.open_count ?? 0} 次</span>
						<span className="ml-auto font-mono text-[12px] text-muted-foreground">{proxyText(env.proxy)}</span>
					</div>
				);
			})}
		</div>
	);
}
