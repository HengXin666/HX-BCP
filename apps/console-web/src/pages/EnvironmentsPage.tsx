import type { JSX } from "react";
import { Badge, EmptyState } from "@hx/ui";
import { listEnvs, type Env } from "../lib/api";
import { usePoll } from "../lib/hooks";

/** 环境的运行态: runtime 为 null 表示未启动. */
function running(env: Env): boolean {
	return env.runtime !== null && env.runtime !== undefined;
}

/** 环境的代理显示: 没有代理写直连. */
function proxyLabel(env: Env): string {
	const p = env.proxy;
	if (!p || !p.host) return "直连";
	return (p.type ?? "http") + "://" + p.host + (p.port ? ":" + p.port : "");
}

/** 浏览器环境列表: 运行态, 可见性, 分组, 平台, 指纹与代理. */
export function EnvironmentsPage(): JSX.Element {
	const { result, loading } = usePoll(listEnvs);

	if (loading && !result) return <EmptyState text="读取中" hint="正在连接浏览器服务 (8325)" />;
	if (!result?.ok) return <EmptyState text="未连接" hint={"浏览器服务不可达: " + (result ? result.error : "")} />;

	const envs = result.data;
	if (envs.length === 0) return <EmptyState text="还没有环境" hint="用 hxb new --name demo 创建一个" />;

	return (
		<div className="flex flex-col gap-2">
			{envs.map((env) => (
				<div key={env.id} className="flex flex-col gap-1 rounded-lg border border-border/60 px-3 py-2">
					<div className="flex items-center gap-3">
						<span className="font-mono text-[13px]">{env.name}</span>
						<Badge variant={running(env) ? "success" : "secondary"}>{running(env) ? "运行中" : "未启动"}</Badge>
						<Badge variant={env.visible ? "success" : "secondary"}>{env.visible ? "窗口可见" : "不可见"}</Badge>
						<span className="ml-auto font-mono text-[12px] text-muted-foreground">{proxyLabel(env)}</span>
					</div>
					<div className="flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-muted-foreground">
						<span>分组 {env.group ?? "-"}</span>
						<span>平台 {env.platform ?? "-"}</span>
						<span>指纹 {env.fingerprint?.browser ?? "-"}</span>
						<span>打开 {env.open_count ?? 0} 次</span>
						{env.visible_reason ? <span>{env.visible_reason}</span> : null}
					</div>
				</div>
			))}
		</div>
	);
}
