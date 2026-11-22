/**
 * 总览页: 一屏回答 "服务活着吗, 跑着几个, 有什么要我处理". 上排四张数字卡可点进对应页,
 * 中间是待处理清单 (待重启 / 指纹非绿 / 缓存引擎未跑), 下面是最近打开的浏览器. 全部只读, 走带缓存的读接口.
 */
import { useMemo, type JSX } from "react";
import { Badge, Button, Card, CardContent, EmptyState, StatCard, StatGrid } from "@hx/ui";
import { cacheStats, fingerprintVerdict, listEnvs, serviceStatus, type Env } from "../../lib/api";
import { ago } from "../../lib/api-ops";
import { usePoll } from "../../lib/hooks";
import { PageLoading } from "../PageLoading";
import { PageTitle } from "../PageTitle";

/** 后端时间戳可能是秒也可能是毫秒, 统一成毫秒再交给 ago. */
const toMs = (t: number | undefined): number => (!t ? 0 : t < 1e12 ? t * 1000 : t);

type Go = (path: string[], q?: Record<string, string>) => void;
type Todo = { key: string; tone: "warning" | "destructive"; text: string; action: string; to: [string[], Record<string, string>?] };

const VERDICT: Record<string, { text: string; tone: "success" | "warning" | "destructive" | "secondary" }> = {
	green: { text: "通过", tone: "success" },
	yellow: { text: "待复核", tone: "warning" },
	red: { text: "不通过", tone: "destructive" },
	unknown: { text: "未检", tone: "secondary" },
};

function todos(envs: Env[], verdict: string | undefined, engine: string | undefined): Todo[] {
	const out: Todo[] = [];
	const restart = envs.filter((e) => e.pending_restart);
	if (restart.length) out.push({ key: "restart", tone: "warning", text: restart.length + " 个浏览器改了配置, 重启后生效", action: "去处理", to: [["browsers"], { status: "restart" }] });
	if (verdict === "red" || verdict === "yellow")
		out.push({ key: "fp", tone: verdict === "red" ? "destructive" : "warning", text: "指纹实测: " + VERDICT[verdict].text, action: "看详情", to: [["system", "status"]] });
	if (engine && engine !== "running") out.push({ key: "cache", tone: "warning", text: "缓存引擎状态: " + engine, action: "看状态", to: [["system", "status"]] });
	return out;
}

export function OverviewPage(props: { go: Go }): JSX.Element {
	const status = usePoll(serviceStatus, 5000);
	const envs = usePoll(listEnvs, 10000);
	const verdict = usePoll(fingerprintVerdict, 60000);
	const cache = usePoll(cacheStats, 15000);

	const list = envs.result?.ok ? envs.result.data : [];
	const running = list.filter((e) => e.in_use);
	const recent = useMemo(
		() => [...list].filter((e) => e.last_open_at).sort((a, b) => (b.last_open_at ?? 0) - (a.last_open_at ?? 0)).slice(0, 8),
		[list],
	);
	const v = verdict.result?.ok ? verdict.result.data.verdict : undefined;
	const eng = cache.result?.ok ? cache.result.data.engine_state : undefined;
	const ratio = cache.result?.ok ? cache.result.data.engine?.saved_ratio : undefined;
	const items = todos(list, v, eng);

	if (status.loading && envs.loading) return <PageLoading rows={6} />;
	if (status.result && !status.result.ok) return <EmptyState text="未连接" hint={"浏览器服务不可达: " + status.result.error} />;
	const svc = status.result?.ok ? status.result.data : null;

	return (
		<div className="flex min-w-0 flex-col gap-4" data-page="overview">
			<PageTitle title="总览" desc={svc ? "服务 " + svc.status + " · v" + svc.version + " · API " + svc.api_port : ""} />
			<StatGrid columns={4}>
				<button type="button" className="rounded-xl text-left transition-shadow hover:shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => props.go(["live"])}>
					<StatCard label="运行中" value={String(svc?.running ?? running.length)} />
				</button>
				<button type="button" className="rounded-xl text-left transition-shadow hover:shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => props.go(["browsers"])}>
					<StatCard label="浏览器总数" value={String(list.length)} />
				</button>
				<button type="button" className="rounded-xl text-left transition-shadow hover:shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => props.go(["system", "status"])}>
					<StatCard label="指纹实测" value={v ? VERDICT[v]?.text ?? v : "-"} />
				</button>
				<button type="button" className="rounded-xl text-left transition-shadow hover:shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => props.go(["system", "status"])}>
					<StatCard label="缓存节省" value={ratio == null ? "-" : Math.round(ratio * 100) + "%"} />
				</button>
			</StatGrid>

			<Card variant="muted">
				<CardContent className="flex flex-col gap-2 p-4">
					<span className="t-card">待处理</span>
					{items.length === 0 ? (
						<span className="t-aux text-muted-foreground">没有需要处理的事项</span>
					) : (
						items.map((t) => (
							<div key={t.key} className="flex items-center gap-2" data-todo={t.key}>
								<Badge variant={t.tone}>{t.tone === "destructive" ? "紧急" : "注意"}</Badge>
								<span className="min-w-0 flex-1 truncate">{t.text}</span>
								<Button size="sm" variant="ghost" onClick={() => props.go(...t.to)}>
									{t.action}
								</Button>
							</div>
						))
					)}
				</CardContent>
			</Card>

			<Card variant="muted">
				<CardContent className="flex flex-col gap-1 p-4">
					<div className="flex items-baseline">
						<span className="t-card">最近打开</span>
						<Button size="sm" variant="ghost" className="ml-auto" onClick={() => props.go(["browsers"])}>
							全部浏览器
						</Button>
					</div>
					{recent.length === 0 ? (
						<span className="t-aux text-muted-foreground">还没有打开记录</span>
					) : (
						recent.map((e) => (
							<button key={e.id} type="button" className="flex items-center gap-2 rounded px-1 py-1 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" onClick={() => props.go(["browsers"], { q: e.name })}>
								<span className={"size-2 shrink-0 rounded-full " + (e.in_use ? "bg-emerald-500" : "bg-muted-foreground/40")} />
								<span className="min-w-0 flex-1 truncate">{e.name}</span>
								<span className="t-aux text-muted-foreground">{e.folder || "未分组"}</span>
								<span className="t-aux w-20 text-right text-muted-foreground">{ago(toMs(e.last_open_at))}</span>
							</button>
						))
					)}
				</CardContent>
			</Card>
		</div>
	);
}
