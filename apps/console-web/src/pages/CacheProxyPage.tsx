import type { JSX } from "react";
import { Badge, Card, CardContent, EmptyState, ProgressMeter } from "@hx/ui";
import { cacheStats } from "../lib/api";
import { usePoll } from "../lib/hooks";

const PERCENT = 100;
const MB = 1024 * 1024;

/** 字节数转 MB, 保留一位小数. */
function toMB(bytes?: number): string {
	return ((bytes ?? 0) / MB).toFixed(1);
}

/** 缓存代理统计: 命中率, 存储规模与抓包记录数. */
export function CacheProxyPage(): JSX.Element {
	const { result, loading } = usePoll(cacheStats);
	if (loading && !result) return <EmptyState text="读取中" hint="正在连接缓存代理 (8898)" />;
	if (!result?.ok) return <EmptyState text="未连接" hint={"缓存代理不可达: " + (result ? result.error : "")} />;

	const s = result.data;
	const hits = s.engine?.hits ?? 0;
	const misses = s.engine?.misses ?? 0;
	const total = hits + misses;
	const rate = total === 0 ? 0 : Math.round((hits / total) * PERCENT);

	return (
		<Card variant="muted">
			<CardContent className="flex flex-col gap-3 pt-3">
				<div className="flex items-center gap-3">
					<Badge variant={s.engine_state === "running" ? "success" : "secondary"}>{s.engine_state ?? "unknown"}</Badge>
					<span className="text-[12px] text-muted-foreground">引擎状态</span>
				</div>
				<ProgressMeter label="缓存命中率 (engine.hits / (hits + misses))" value={rate} max={PERCENT} />
				<div className="flex flex-wrap gap-6 text-[13px]">
					<span>命中 <b className="tabular-nums">{hits}</b></span>
					<span>未命中 <b className="tabular-nums">{misses}</b></span>
					<span>累计服务字节 <b className="tabular-nums">{toMB(s.store?.bytes_served)}</b> MB</span>
					<span>缓存体积 <b className="tabular-nums">{toMB(s.store?.bytes)}</b> MB</span>
					<span>条目 <b className="tabular-nums">{s.store?.entries ?? 0}</b></span>
					<span>抓包记录 <b className="tabular-nums">{s.flows?.records ?? 0}</b></span>
				</div>
				<div className="text-[12px] text-muted-foreground">
					累计服务字节是缓存累计返回给浏览器的字节数, 不等于节省字节.
				</div>
			</CardContent>
		</Card>
	);
}
