/** 全局链路: 浏览器服务 8325 -> 缓存代理 8898 -> 上游出口 -> 目标站点, 每段的实况与数字. 缺的数据写未上报. */
import { useEffect, useMemo, type JSX, type ReactNode } from "react";
import { LoaderCircle, RotateCw } from "lucide-react";
import { Button, Skeleton, EmptyState } from "@hx/ui";
import { cacheStats, listEnvs, listProxies, serviceStatus, type CacheStats } from "../../lib/api";
import { STATE_TEXT, distribution, fmtBytes, fmtPct, hitRate, upstreamKey, type SegKey, type SegState } from "../../lib/chain";
import { usePoll, useExitChecks } from "../../lib/hooks";
import { KV, SegNode, StateBadge } from "../browsers/ChainParts";

const LEGEND: SegState[] = ["ok", "warn", "bad", "off", "idle", "na"];
const MAX_AUTO_CHECK = 8;

function cacheState(c: CacheStats | undefined, failed: boolean): SegState {
	if (failed) return "bad";
	if (!c) return "na";
	return c.engine_state === "running" ? "ok" : c.engine_state === "absent" ? "off" : "warn";
}

/** 一段: 节点 + 标题 + 状态, 下面是数字. data-global-seg 供验收脚本读. */
function Stage(props: { k: SegKey; title: string; port?: string; state: SegState; action?: ReactNode; children: ReactNode }): JSX.Element {
	return (
		<section className="flex min-w-0 flex-col gap-3 rounded-xl border border-border bg-card p-4 transition-colors duration-150 hover:border-foreground/20" data-global-seg={props.k} data-state={props.state}>
			<header className="flex items-center gap-3">
				<SegNode k={props.k} state={props.state} size="md" />
				<div className="min-w-0 flex-1">
					<div className="truncate t-card">{props.title}</div>
					{props.port ? <div className="font-mono t-aux text-muted-foreground">{props.port}</div> : null}
				</div>
				<StateBadge state={props.state} />
			</header>
			{props.children}
			{props.action}
		</section>
	);
}

/** 段间连线: 宽屏横向, 窄屏纵向; 两端都正常是实线绿, 有一端故障是红, 其余虚线灰. */
function Link(props: { a: SegState; b: SegState }): JSX.Element {
	const color = props.a === "bad" || props.b === "bad" ? "border-destructive/70" : props.a === "ok" && props.b === "ok" ? "border-success/70" : "border-border border-dashed";
	return (
		<div className="flex items-center justify-center" aria-hidden>
			<span className={"hidden h-0 w-full border-t-2 xl:block " + color} />
			<span className={"h-5 border-l-2 xl:hidden " + color} />
		</div>
	);
}

export function GlobalChain(): JSX.Element {
	const svc = usePoll(serviceStatus);
	const cache = usePoll(cacheStats);
	const envs = usePoll(listEnvs, 10000);
	const saved = usePoll(listProxies, 30000);
	const checks = useExitChecks();
	const list = envs.result?.ok ? envs.result.data : [];
	const dist = useMemo(() => distribution(list), [list]);
	const ups = useMemo(() => {
		const m = new Map(dist.ups);
		for (const p of saved.result?.ok ? saved.result.data : []) {
			const k = upstreamKey(p);
			if (k && !m.has(k)) m.set(k, { proxy: p, n: 0 });
		}
		return [...m.entries()].sort((a, b) => b[1].n - a[1].n);
	}, [dist, saved.result]);
	const { run, results } = checks;
	const keys = ups.map(([k]) => k).join(",");
	useEffect(() => {
		const todo = ups.filter(([k]) => !results[k]).slice(0, MAX_AUTO_CHECK);
		if (todo.length) void run(todo.map(([key, v]) => ({ key, proxy: v.proxy })));
		// 只在上游集合变化时自动测一轮, 结果回来不再触发
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [keys, run]);

	const s = svc.result?.ok ? svc.result.data : undefined;
	const c = cache.result?.ok ? cache.result.data : undefined;
	const sv: SegState = svc.result ? (s ? "ok" : "bad") : "na";
	const cs = cacheState(c, !!cache.result && !cache.result.ok);
	const tested = ups.map(([k]) => results[k]).filter(Boolean);
	const es: SegState = !ups.length ? "off" : checks.pending.size ? "warn" : tested.some((r) => !r.ok) ? "bad" : tested.length ? "ok" : "na";
	if (svc.loading && cache.loading) return <Skeleton className="h-64 w-full rounded-xl" />;

	return (
		<div className="flex flex-col gap-3" data-global-chain="">
			<div className="flex flex-wrap items-center gap-x-4 gap-y-1 t-aux text-muted-foreground">
				{LEGEND.map((st) => (
					<span key={st} className="inline-flex items-center gap-2">
						<SegNode k="env" state={st} />
						{STATE_TEXT[st]}
					</span>
				))}
			</div>
			<div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_1.5rem_minmax(0,1fr)_1.5rem_minmax(0,1.35fr)_1.5rem_minmax(0,0.8fr)]">
				<Stage k="env" title="浏览器服务" port={s ? "hx-browser " + s.version + " :" + s.api_port : "8325"} state={sv}>
					<KV
						rows={[
							["运行中实例", s ? s.running : svc.result && !svc.result.ok ? "不可达: " + svc.result.error : null],
							["环境总数", envs.result?.ok ? dist.total : null],
							["挂缓存", envs.result?.ok ? dist.cache + " 个" : null],
							["挂上游", envs.result?.ok ? dist.upstream + " 个" : null],
							["直连", envs.result?.ok ? dist.direct + " 个" : null],
						]}
					/>
				</Stage>
				<Link a={sv} b={cs} />
				<Stage k="cache" title="缓存代理" port="hxcp :8898" state={cs}>
					<KV
						rows={[
							["引擎", c ? c.engine_state : cache.result && !cache.result.ok ? "不可达: " + cache.result.error : null],
							["命中率", c ? fmtPct(hitRate(c)) : null],
							["命中 / 未中", c?.engine ? (c.engine.hits ?? 0) + " / " + (c.engine.misses ?? 0) : null],
							["缓存返回", c?.engine ? fmtBytes(c.engine.bytes_served) : null],
							["走网络", c?.engine ? fmtBytes(c.engine.bytes_network) : null],
							["省流比", c?.engine ? fmtPct(c.engine.saved_ratio ?? null) : null],
							["缓存库", c?.store ? (c.store.entries ?? 0) + " 条, " + fmtBytes(c.store.bytes) : null],
						]}
					/>
				</Stage>
				<Link a={cs} b={es} />
				<Stage
					k="exit"
					title="上游出口"
					port={ups.length + " 个上游"}
					state={es}
					action={
						ups.length ? (
							<Button size="sm" variant="outline" className="self-start" disabled={checks.pending.size > 0} onClick={() => void run(ups.map(([key, v]) => ({ key, proxy: v.proxy })))}>
								{checks.pending.size ? <LoaderCircle className="size-4 animate-spin" /> : <RotateCw className="size-4" />}
								{checks.pending.size ? "检测中 " + checks.pending.size : "重新检测全部"}
							</Button>
						) : null
					}
				>
					{ups.length ? (
						<ul className="flex flex-col gap-2 t-aux">
							{ups.map(([k, v]) => {
								const r = results[k];
								const st: SegState = checks.pending.has(k) ? "warn" : !r ? "na" : r.ok ? "ok" : "bad";
								return (
									<li key={k} className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1 transition-colors duration-150 hover:bg-accent/40" data-upstream={k} data-state={st}>
										<StateBadge state={st} text={checks.pending.has(k) ? "检测中" : undefined} />
										<span className="min-w-0 flex-1 truncate font-mono" title={k}>{k.replace(/^.*:\/\//, "")}</span>
										<span className="shrink-0 tabular-nums text-muted-foreground">{v.n} 环境</span>
										<span className="w-[9.5rem] shrink-0 truncate text-right font-mono tabular-nums" title={r?.ok ? r.isp : r?.error}>
											{r?.ok ? r.ip + " " + (r.country ?? "") + " " + r.latency_ms + "ms" : r ? "不通" : "未检测"}
										</span>
									</li>
								);
							})}
						</ul>
					) : (
						<EmptyState compact text="没有环境挂上游代理" hint="全部直连出网" />
					)}
				</Stage>
				<Link a={es} b="na" />
				<Stage k="target" title="目标站点" state="na">
					<KV rows={[["抓包记录", c?.flows?.records ?? null], ["当前页面", null]]} />
					<p className="t-aux text-muted-foreground">目标由各环境的页面决定, 8325 不汇总当前页面.</p>
				</Stage>
			</div>
		</div>
	);
}
