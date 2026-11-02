/** 链路详情: 一个环境从浏览器到目标站点的四段, 每段的实况与数字, 以及换掉它会影响什么. 缺的数据写未上报. */
import { useCallback, useEffect, type JSX } from "react";
import { LoaderCircle, RotateCw } from "lucide-react";
import { Button, Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, Skeleton } from "@hx/ui";
import { cacheStats, envStatus, fingerprintVerdict, hxcpStatus, type Env } from "../../lib/api";
import { SEG_IMPACT, STATE_TEXT, chainOf, fmtBytes, fmtPct, hitRate, upstreamKey } from "../../lib/chain";
import { usePoll, type ExitChecks } from "../../lib/hooks";
import { KV, SegCard, Wire } from "./ChainParts";
import { when, whereLabel } from "./EnvCells";

const VERDICT: Record<string, string> = { green: "通过", yellow: "待复核", red: "阻断", unknown: "无报告" };

function rt(env: Env, k: string): string | number | undefined {
	const v = env.runtime?.[k];
	return typeof v === "string" || typeof v === "number" ? v : undefined;
}

function Body(props: { env: Env; checks: ExitChecks }): JSX.Element {
	const id = props.env.id;
	const live = usePoll(useCallback((s: AbortSignal) => envStatus(id, s), [id]));
	const hxcp = usePoll(useCallback((s: AbortSignal) => hxcpStatus(id, s), [id]));
	const cache = usePoll(cacheStats);
	const verdict = usePoll(fingerprintVerdict, 30000);
	const env = live.result?.ok ? live.result.data : props.env;
	const key = upstreamKey(env.proxy);
	const check = key ? props.checks.results[key] : undefined;
	const checking = props.checks.pending.has(key);
	const { run } = props.checks;
	const hasCheck = !!check;
	useEffect(() => {
		if (key && !hasCheck) void run([{ key, proxy: env.proxy ?? null }]);
	}, [key, hasCheck, run, env.proxy]);

	const [s0, s1, s2, s3] = chainOf(env, check);
	const fp = env.fingerprint ?? {};
	const c = cache.result?.ok ? cache.result.data : undefined;
	const h = hxcp.result?.ok ? hxcp.result.data : undefined;
	const v = verdict.result?.ok ? verdict.result.data : undefined;
	const relay = Number(rt(env, "relay_port") ?? 0);
	const pid = rt(env, "pid");
	return (
		<div className="flex flex-col px-4 pb-6">
			<SegCard k="env" title="浏览器实例 (身份)" state={s0.state} summary={s0.value} impact={SEG_IMPACT.env}>
				<KV
					rows={[
						["进程", pid ? "pid " + pid + ", 调试口 " + String(rt(env, "debug_port") ?? "") : null],
						["内核", [fp.kernel || fp.browser, rt(env, "kernel")].filter(Boolean).join(" / ")],
						["系统", fp.os],
						["UA", fp.user_agent || (fp.user_agent === "" ? "跟随内核" : null)],
						["时区 / 语言", [fp.timezone || "自动", fp.language || "自动"].join(" / ")],
						["分辨率", fp.resolution],
						["WebGL", [fp.webgl_vendor, fp.webgl_renderer].filter(Boolean).join(" / ") || null],
						["硬件并发", fp.hardware_concurrency ? fp.hardware_concurrency + " 核, 内存 " + (fp.device_memory || "-") + " GB" : null],
						["指纹自检", v ? VERDICT[v.verdict] + ", " + v.checked + " 项, " + (v.at || "无时间") + " (全局最近一份)" : null],
					]}
				/>
			</SegCard>
			<Wire vertical from={s0.state} to={s1.state} label="CDP Fetch 拦截静态资源" />
			<SegCard k="cache" title="缓存代理" state={s1.state} summary={s1.value} impact={SEG_IMPACT.cache}>
				<KV
					rows={[
						["挂载配置", env.hxcp_enabled ? "随启动自动挂载" : "关"],
						["引擎实况", h ? h.status + (h.pid ? ", pid " + h.pid : "") : null],
						["本环境命中", null],
						["本环境省流", null],
						["8898 引擎", c ? c.engine_state ?? null : cache.result && !cache.result.ok ? "不可达: " + cache.result.error : null],
						["8898 命中率", c ? fmtPct(hitRate(c)) + (c.engine ? " (" + (c.engine.hits ?? 0) + " 中 / " + (c.engine.misses ?? 0) + " 未中)" : "") : null],
						["8898 省流", c?.engine ? fmtBytes(c.engine.bytes_served) + " 由缓存返回, " + fmtBytes(c.engine.bytes_network) + " 走网络" : null],
						["8898 缓存库", c?.store ? (c.store.entries ?? 0) + " 条, " + fmtBytes(c.store.bytes) : null],
					]}
				/>
				<p className="mt-2 t-aux text-muted-foreground">本环境的计数写在它自己的缓存目录, 8325 尚未上报; 8898 行是全局缓存服务的数字.</p>
			</SegCard>
			<Wire vertical from={s1.state} to={s2.state} label={relay ? "本地 relay 127.0.0.1:" + relay : key ? "经上游代理" : "直连出网"} />
			<SegCard
				k="exit"
				title="出口 (上游代理)"
				state={checking ? "warn" : s2.state}
				summary={checking ? key + " 检测中" : s2.value}
				impact={SEG_IMPACT.exit}
				action={
					key ? (
						<Button size="sm" variant="outline" disabled={checking} onClick={() => void run([{ key, proxy: env.proxy ?? null }])}>
							{checking ? <LoaderCircle className="size-4 animate-spin" /> : <RotateCw className="size-4" />}
							{checking ? "检测中" : "重新检测"}
						</Button>
					) : undefined
				}
			>
				<KV
					rows={[
						["上游", key || (env.proxy?.type === "system" ? "系统代理" : "未挂载 (直连)")],
						["凭据", key ? (env.proxy?.has_password ? "有密码" : "无") : null],
						["出口 IP", check?.ok ? check.ip : check ? "不通: " + (check.error ?? "") : null],
						["位置", check?.ok ? [check.country, check.region, check.city].filter(Boolean).join(" / ") : null],
						["运营商", check?.ok ? [check.asn, check.isp].filter(Boolean).join(" ") : null],
						["延迟", check?.latency_ms !== undefined ? check.latency_ms + " ms (三源并发探测总耗时)" : null],
						["上次对齐", env.last_region?.ip ? env.last_region.ip + " " + (env.last_region.country ?? "") + " " + when(env.last_region.checked_at) : null],
						["位置模式", env.location_mode === "match_ip" ? "按出口 IP 对齐时区与语言" : env.location_mode],
					]}
				/>
			</SegCard>
			<Wire vertical from={s2.state} to={s3.state} label="TLS 不终结, 原样到站" />
			<SegCard k="target" title="目标站点" state={s3.state} summary={s3.value} impact={SEG_IMPACT.target}>
				<KV
					rows={[
						["启动页", (env.launch?.urls ?? []).join(", ") || null],
						["窗口标题", env.visible ? String(env.window_title ?? "") || null : null],
						["当前页面", null],
					]}
				/>
			</SegCard>
		</div>
	);
}

/** 右侧滑出的链路详情. 只在打开时挂载, 关掉就停止轮询. */
export function ChainSheet(props: { env: Env | null; onClose: () => void; checks: ExitChecks }): JSX.Element {
	const env = props.env;
	const segs = env ? chainOf(env, props.checks.results[upstreamKey(env.proxy)]) : [];
	const broken = segs.filter((s) => s.state === "bad").length;
	const mounted = segs.filter((s) => s.state !== "off").length;
	return (
		<Sheet open={!!env} onOpenChange={(o) => (o ? undefined : props.onClose())}>
			<SheetContent side="right" className="w-full gap-2 sm:max-w-xl" data-chain-sheet="">
				<SheetHeader>
					<SheetTitle className="truncate font-mono">{env?.name ?? ""}</SheetTitle>
					<SheetDescription>
						{env ? whereLabel(env) + " - 4 段中 " + mounted + " 段在链路上" + (broken ? ", " + broken + " 段" + STATE_TEXT.bad : "") : ""}
					</SheetDescription>
				</SheetHeader>
				{env ? <Body env={env} checks={props.checks} /> : <Skeleton className="mx-4 h-40" />}
			</SheetContent>
		</Sheet>
	);
}
