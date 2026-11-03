/**
 * 状态页图表: 运行中实例数 (面积图) 与内核分布 (环形饼图). 后端没有历史接口, 前端每 10 秒采一次 GET /status 的 running,
 * 放进 360 点的环形缓冲 (约 1 小时), 缓冲在模块里, 切走再回来不清空; 图上标明 "本页打开以来". 内核分布按全部环境的配置内核计.
 */
import { useEffect, useMemo, useState, type JSX } from "react";
import { Card, CardContent, HxAreaChart, HxPieChart } from "@hx/ui";
import { listEnvs, serviceStatus } from "../../lib/api";
import { usePoll } from "../../lib/hooks";

const CAP = 360;
const EVERY = 10000;
type Point = { name: string; running: number } & Record<string, unknown>;
const ring: Point[] = [];

function hhmmss(t: number): string {
	const d = new Date(t);
	return [d.getHours(), d.getMinutes(), d.getSeconds()].map((n) => String(n).padStart(2, "0")).join(":");
}

function useRunningSeries(): Point[] {
	const [pts, setPts] = useState<Point[]>(() => [...ring]);
	useEffect(() => {
		const ctl = new AbortController();
		const sample = (): void => {
			void serviceStatus(ctl.signal).then((r) => {
				if (!r.ok) return;
				ring.push({ name: hhmmss(Date.now()), running: r.data.running });
				if (ring.length > CAP) ring.splice(0, ring.length - CAP);
				setPts([...ring]);
			});
		};
		sample();
		const t = setInterval(sample, EVERY);
		return () => (ctl.abort(), clearInterval(t));
	}, []);
	return pts;
}

function ChartCard(props: { title: string; note: string; points: number; kind: string; children: JSX.Element }): JSX.Element {
	return (
		<Card variant="muted" data-chart={props.kind} data-chart-points={props.points}>
			<CardContent className="flex flex-col gap-2 p-4">
				<div className="flex items-baseline gap-2">
					<span className="t-card">{props.title}</span>
					<span className="ml-auto t-aux text-muted-foreground">{props.note}</span>
				</div>
				{props.children}
			</CardContent>
		</Card>
	);
}

export function StatusCharts(): JSX.Element {
	const pts = useRunningSeries();
	const envs = usePoll(listEnvs, 30000);
	const pie = useMemo(() => {
		const n = new Map<string, number>();
		for (const e of envs.result?.ok ? envs.result.data : []) {
			const k = e.fingerprint?.kernel || e.fingerprint?.browser || "未指定";
			n.set(k, (n.get(k) ?? 0) + 1);
		}
		return [...n.entries()].sort((a, b) => b[1] - a[1]).map(([name, value]) => ({ name, value }));
	}, [envs.result]);
	const last = pts.length ? pts[pts.length - 1].running : null;

	return (
		<div className="grid gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
			<ChartCard kind="running" points={pts.length} title={"运行中实例" + (last === null ? " 未上报" : " " + last)} note={"本页打开以来, 每 10 秒一点, " + pts.length + " / " + CAP}>
				<HxAreaChart data={pts} series={[{ dataKey: "running", name: "运行中" }]} height={180} />
			</ChartCard>
			<ChartCard kind="kernels" points={pie.length} title="内核分布" note={pie.length ? pie.reduce((s, p) => s + p.value, 0) + " 个环境" : "未上报"}>
				<HxPieChart data={pie} height={180} innerRadius={48} />
			</ChartCard>
		</div>
	);
}
