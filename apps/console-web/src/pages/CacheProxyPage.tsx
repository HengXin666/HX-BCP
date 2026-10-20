import type { JSX } from "react";
import { Card, CardContent, EmptyState, ProgressMeter } from "@hx/ui";
import { cacheStats } from "../lib/api";
import { usePoll } from "../lib/hooks";

const PERCENT = 100;

/** 缓存代理统计: 命中率与省流字节数. */
export function CacheProxyPage(): JSX.Element {
	const { result, loading } = usePoll(cacheStats);
	if (loading && !result) return <EmptyState text="读取中" hint="正在连接缓存代理 (8898)" />;
	if (!result?.ok) return <EmptyState text="未连接" hint={"缓存代理不可达: " + (result && "error" in result ? result.error : "")} />;

	const s = result.data;
	const hits = s.hits ?? 0;
	const total = hits + (s.misses ?? 0);
	const rate = total === 0 ? 0 : Math.round((hits / total) * PERCENT);
	const savedMB = ((s.bytesSaved ?? 0) / (1024 * 1024)).toFixed(1);

	return (
		<Card variant="muted">
			<CardContent className="flex flex-col gap-3 pt-3">
				<ProgressMeter label="缓存命中率" value={rate} max={PERCENT} />
				<div className="flex gap-6 text-[13px]">
					<span>命中 <b className="tabular-nums">{hits}</b></span>
					<span>未命中 <b className="tabular-nums">{s.misses ?? 0}</b></span>
					<span>省流 <b className="tabular-nums">{savedMB}</b> MB</span>
					<span>条目 <b className="tabular-nums">{s.entries ?? 0}</b></span>
				</div>
			</CardContent>
		</Card>
	);
}
