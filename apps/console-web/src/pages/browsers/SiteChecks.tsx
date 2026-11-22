/**
 * 指纹页下半部: 检测站评分 与 一致性自检, 与 fingerprint-score 流水线同一套工具, 只是对准这个环境.
 * 后端在后台跑 (几分钟), 这里 4 秒轮询一次状态; 结论色判据在后端与 gate, 这里只展示.
 */
import { useCallback, useState, type JSX } from "react";
import { Play } from "lucide-react";
import { Badge, Button, EmptyState, toast } from "@hx/ui";
import type { Env } from "../../lib/api";
import { usePoll } from "../../lib/hooks";
import { siteChecksApi, tone, VERDICT_TEXT, type CheckKind, type CheckState } from "../../lib/siteChecks";
import { Section } from "./DetailParts";

const META: Record<CheckKind, { title: string; hint: string }> = {
	sites: { title: "检测站评分", hint: "打开 CreepJS, Sannysoft, Pixelscan 等公开检测站, 按它们页面上的结论打分. 约 3 分钟, 结果受出口 IP 影响." },
	audit: { title: "一致性自检", hint: "fingerprint-audit 的规则: 时区, 语言, WebRTC, 指纹字段是否互相矛盾. 约 5 分钟, 报告也会更新全局指纹结论." },
};

const when = (ms?: number): string => (ms ? new Date(ms).toLocaleString() : "");

function Head(props: { s: CheckState }): JSX.Element | null {
	const s = props.s;
	if (s.state === "running") return <Badge variant="secondary">检测中</Badge>;
	if (s.state === "failed") return <Badge variant="destructive">失败</Badge>;
	if (s.state !== "done") return null;
	const v = s.verdict ?? "unknown";
	const score = typeof s.score === "number" ? " " + s.score.toFixed(1) : "";
	return <Badge variant={tone(v)} data-check-verdict={v}>{(VERDICT_TEXT[v] ?? v) + score}</Badge>;
}

function Body(props: { kind: CheckKind; s: CheckState }): JSX.Element {
	const s = props.s;
	if (s.state === "idle") return <EmptyState text="还没跑过" hint={META[props.kind].hint} />;
	if (s.state === "running") return <p className="t-aux text-muted-foreground">{"开始于 " + when(s.started_at) + ", 跑完自动刷新"}</p>;
	if (s.state === "failed") return <p className="t-aux text-destructive break-all">{s.error || "未知错误"}</p>;
	const rows = s.rows ?? [];
	return (
		<div className="flex flex-col gap-1">
			{rows.length === 0 ? <p className="t-aux text-muted-foreground">没有发现问题</p> : null}
			<ul className="flex flex-col divide-y divide-border/50 t-aux" data-check-rows={rows.length}>
				{rows.map((r, i) => (
					<li key={(r.siteId ?? r.name) + i} className="flex flex-col gap-0.5 py-2">
						<div className="flex items-center gap-2">
							<span className="font-medium">{r.name}</span>
							{typeof r.score === "number" ? <span className="tabular-nums text-muted-foreground">{r.score.toFixed(0)}</span> : null}
							<Badge variant={tone(r.verdict)} className="ml-auto">{VERDICT_TEXT[r.verdict] ?? r.verdict}</Badge>
						</div>
						{r.headline ? <span className="text-muted-foreground">{r.headline}</span> : null}
						{r.fix ? <span className="text-foreground">{"修复: " + r.fix}</span> : null}
					</li>
				))}
			</ul>
			<span className="t-micro text-muted-foreground">{"完成于 " + when(s.finished_at)}</span>
		</div>
	);
}

/** 两种检测各一块, 环境需在跑且是 Chromium 系, 不满足时后端的原因直接弹出来. */
export function SiteChecks(props: { env: Env }): JSX.Element {
	const id = props.env.id;
	const res = usePoll(useCallback((s: AbortSignal) => siteChecksApi.get(id, s), [id]), 4000);
	const [busy, setBusy] = useState<CheckKind | "">("");
	const run = async (kind: CheckKind): Promise<void> => {
		setBusy(kind);
		const r = await siteChecksApi.run(id, kind);
		setBusy("");
		if (r.ok) {
			toast.success(META[kind].title + " 已开始");
			res.reload();
		} else toast.error(r.error);
	};
	const data = res.result?.ok ? res.result.data : null;
	return (
		<>
			{(["sites", "audit"] as CheckKind[]).map((kind) => {
				const s: CheckState = data?.[kind] ?? { kind, state: "idle" };
				const right = (
					<>
						<Head s={s} />
						<Button size="sm" variant="outline" disabled={busy !== "" || s.state === "running"} onClick={() => void run(kind)} data-check-run={kind}>
							<Play className="size-4" />{s.state === "running" ? "检测中" : s.state === "idle" ? "开始检测" : "重新检测"}
						</Button>
					</>
				);
				return (
					<Section key={kind} title={META[kind].title} right={right}>
						{res.result && !res.result.ok ? <EmptyState text="未上报" hint={res.result.error} /> : <Body kind={kind} s={s} />}
					</Section>
				);
			})}
		</>
	);
}
