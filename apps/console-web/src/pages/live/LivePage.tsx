/**
 * 运行中页: 只列 in_use 的浏览器, 卡片网格, 每张显示运行时长, 出口地区, 车道, 窗口标题, 待重启标记,
 * 操作是 聚焦窗口 / 重启 / 停止. 写操作后 invalidate 由 api-write / api-ops 内部完成, 这里只 reload.
 */
import { useState, type JSX } from "react";
import { Badge, Button, Card, CardContent, EmptyState, Input, Spinner, toast } from "@hx/ui";
import { listEnvs, type Env, type Result } from "../../lib/api";
import { opsApi, ago } from "../../lib/api-ops";
import { envApi } from "../../lib/api-write";
import { usePoll } from "../../lib/hooks";
import { PageLoading } from "../PageLoading";
import { PageTitle } from "../PageTitle";

/** 后端时间戳可能是秒也可能是毫秒, 统一成毫秒再交给 ago. */
const toMs = (t: number | undefined): number => (!t ? 0 : t < 1e12 ? t * 1000 : t);

function uptime(since: number): string {
	if (!since) return "-";
	const s = Math.max(0, Math.floor((Date.now() - toMs(since)) / 1000));
	const h = Math.floor(s / 3600);
	const m = Math.floor((s % 3600) / 60);
	return h ? h + " 小时 " + m + " 分" : m + " 分";
}

function LiveCard(props: { env: Env; busy: string; act: (op: string, call: () => Promise<Result<unknown>>) => void }): JSX.Element {
	const e = props.env;
	const region = e.last_region?.country ? e.last_region.country + (e.last_region.ip ? " · " + e.last_region.ip : "") : "出口未知";
	return (
		<Card variant="muted" data-live-env={e.id}>
			<CardContent className="flex flex-col gap-2 p-4">
				<div className="flex items-center gap-2">
					<span className="size-2 shrink-0 rounded-full bg-emerald-500" />
					<span className="t-card min-w-0 flex-1 truncate" title={e.name}>
						{e.name}
					</span>
					<Badge variant="outline">{e.lane === "agent" ? "Agent" : "人工"}</Badge>
					{e.pending_restart ? <Badge variant="warning">待重启</Badge> : null}
				</div>
				<div className="t-aux truncate text-muted-foreground" title={e.window_title}>
					{e.window_title || "无窗口标题"}
				</div>
				<div className="t-aux flex gap-3 text-muted-foreground">
					<span>已运行 {uptime(e.in_use_since)}</span>
					<span className="truncate">{region}</span>
				</div>
				<div className="flex gap-1">
					<Button size="sm" variant="outline" disabled={!!props.busy} onClick={() => props.act("focus", () => opsApi.focus(e.id))}>
						{props.busy === "focus" ? <Spinner /> : null}聚焦
					</Button>
					<Button size="sm" variant="outline" disabled={!!props.busy} onClick={() => props.act("restart", () => opsApi.restart(e.id))}>
						{props.busy === "restart" ? <Spinner /> : null}重启
					</Button>
					<Button size="sm" variant="ghost" className="ml-auto" disabled={!!props.busy} onClick={() => props.act("stop", () => envApi.stop(e.id))}>
						{props.busy === "stop" ? <Spinner /> : null}停止
					</Button>
				</div>
			</CardContent>
		</Card>
	);
}

export function LivePage(): JSX.Element {
	const envs = usePoll(listEnvs, 3000);
	const [kw, setKw] = useState("");
	const [busy, setBusy] = useState<Record<string, string>>({});
	const [stopping, setStopping] = useState(false);

	const all = envs.result?.ok ? envs.result.data.filter((e) => e.in_use) : [];
	const rows = kw ? all.filter((e) => (e.name + " " + (e.window_title ?? "") + " " + e.folder).toLowerCase().includes(kw.toLowerCase())) : all;
	rows.sort((a, b) => (a.in_use_since || 0) - (b.in_use_since || 0));

	const act = (id: string, op: string, call: () => Promise<Result<unknown>>): void => {
		setBusy((b) => ({ ...b, [id]: op }));
		void call().then((r) => {
			setBusy(({ [id]: _, ...rest }) => rest);
			if (!r.ok) toast.error(r.error);
			envs.reload();
		});
	};

	const stopAll = async (): Promise<void> => {
		setStopping(true);
		const rs = await Promise.all(rows.map((e) => envApi.stop(e.id)));
		const bad = rs.filter((r) => !r.ok).length;
		setStopping(false);
		bad ? toast.error("停止失败 " + bad + " 个") : toast.success("已停止 " + rs.length + " 个");
		envs.reload();
	};

	if (envs.loading) return <PageLoading layout="cards" />;
	if (!envs.result?.ok) return <EmptyState text="未连接" hint={"浏览器服务不可达: " + (envs.result ? envs.result.error : "")} />;

	return (
		<div className="flex min-w-0 flex-col gap-4" data-page="live">
			<PageTitle
				title="实时预览"
				desc={all.length + " 个浏览器在运行, 每 3 秒刷新" + (envs.error ? " · 最近一次刷新失败: " + envs.error : "")}
				right={
					<div className="flex items-center gap-2">
						<Input className="h-8 w-48" placeholder="筛选名称 / 窗口标题" value={kw} onChange={(ev) => setKw(ev.target.value)} />
						<Button size="sm" variant="outline" disabled={!rows.length || stopping} onClick={() => void stopAll()}>
							{stopping ? <Spinner /> : null}全部停止{kw ? "(筛选结果)" : ""}
						</Button>
					</div>
				}
			/>
			{rows.length === 0 ? (
				<EmptyState text={all.length ? "没有匹配的运行中浏览器" : "没有在运行的浏览器"} hint={all.length ? "换个关键字" : "在浏览器页启动一个, 这里会自动出现"} />
			) : (
				<div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
					{rows.map((e) => (
						<LiveCard key={e.id} env={e} busy={busy[e.id] ?? ""} act={(op, call) => act(e.id, op, call)} />
					))}
				</div>
			)}
			<span className="t-aux tabular-nums text-muted-foreground">最早启动于 {rows.length ? ago(toMs(rows[0].in_use_since)) : "-"}</span>
		</div>
	);
}
