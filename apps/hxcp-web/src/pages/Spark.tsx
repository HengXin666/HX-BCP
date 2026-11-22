/** 请求时序密度条: 按时间分 140 格, 有 4xx/5xx 或错误的格标红. */
import type { JSX } from "react";
import type { Flow } from "../lib/api";
import { clock } from "../lib/format";

const N = 140;

export function Spark({ rows }: { rows: readonly Flow[] }): JSX.Element | null {
	if (!rows.length) return null;
	const ts = rows.map((r) => r.at);
	const lo = Math.min(...ts);
	const hi = Math.max(...ts);
	const span = Math.max(hi - lo, 1);
	const count = new Array<number>(N).fill(0);
	const err = new Array<number>(N).fill(0);
	for (const r of rows) {
		const i = Math.min(N - 1, Math.floor(((r.at - lo) / span) * N));
		count[i] += 1;
		if ((r.status ?? 0) >= 400 || String(r.verdict ?? "").startsWith("error")) err[i] += 1;
	}
	const mx = Math.max(...count, 1);
	return (
		<div className="flex flex-col gap-1">
			<div className="spark" aria-label="请求时序">
				{count.map((c, i) => (
					<i key={i} data-err={err[i] > 0} style={{ height: Math.max(1, Math.round((c / mx) * 38)) + "px" }} title={c + " 个请求"} />
				))}
			</div>
			<div className="flex justify-between t-aux text-muted-foreground">
				<span>{clock(lo)}</span>
				<span>请求时序, 红色 = 4xx/5xx</span>
				<span>{clock(hi) + ", 跨度 " + Math.round(span) + " 秒"}</span>
			</div>
		</div>
	);
}
