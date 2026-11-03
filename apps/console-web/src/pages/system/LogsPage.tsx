/**
 * 日志 #/system/logs: GET /logs?limit=N 的操作日志 (后端最多留 500 条, 新的在前). 这里倒成时间正序给 LogViewer, 最新在底.
 * 级别由动作名推出 (后端只记动作, 不记级别): 含 fail / error = error, 删除 / 清理 / 杀进程 = warn, 其余 info.
 * 关键字过滤动作名与明细; 自动刷新开时 5 秒拉一次, 关掉即冻结当前内容 (读历史时不被新行顶走); 手动刷新随时可点.
 */
import { useMemo, useState, type JSX } from "react";
import { RotateCw, Search } from "lucide-react";
import { Button, EmptyState, InputGroup, InputGroupAddon, InputGroupInput, LogViewer, SelectField, Switch, type LogLevel, type LogLine } from "@hx/ui";
import { systemApi, type LogEntry } from "../../lib/api-res";
import { usePoll } from "../../lib/hooks";

const LIMITS = [100, 200, 500];

function levelOf(a: string): LogLevel {
	if (/fail|error|crash/i.test(a)) return "error";
	if (/delete|purge|cleanup|kill|orphan/i.test(a)) return "warn";
	if (/create|import|start/i.test(a)) return "success";
	return "info";
}

function toLine(e: LogEntry, i: number): LogLine {
	const detail = e.detail && Object.keys(e.detail).length ? JSON.stringify(e.detail) : "";
	return { id: e.at + ":" + i, level: levelOf(e.action), time: e.at, message: e.action + (detail ? "  " + detail.slice(0, 160) : ""), detail: detail.length > 160 ? JSON.stringify(e.detail, null, 2) : undefined };
}

export function LogsPage(): JSX.Element {
	const [limit, setLimit] = useState(200);
	const [live, setLive] = useState(true);
	const [q, setQ] = useState("");
	const load = useMemo(() => (s: AbortSignal) => systemApi.logs(limit, s), [limit]);
	const res = usePoll(load, live ? 5000 : 3600000);
	const [frozen, setFrozen] = useState<LogEntry[] | null>(null);
	const latest = res.result?.ok ? res.result.data : [];
	const data = live ? latest : (frozen ?? latest);
	const lines = useMemo(() => {
		const w = q.trim().toLowerCase();
		return [...data].reverse().map(toLine).filter((l) => !w || l.message.toLowerCase().includes(w));
	}, [data, q]);

	if (res.result && !res.result.ok && !latest.length) return <EmptyState text="未连接" hint={"GET /api/v1/logs 失败: " + res.result.error} />;
	return (
		<div className="flex flex-col gap-3" data-sys-page="logs" data-log-count={lines.length}>
			<div className="flex flex-wrap items-center gap-2">
				<div className="min-w-48 flex-1">
					<InputGroup>
						<InputGroupAddon><Search className="size-4" /></InputGroupAddon>
						<InputGroupInput value={q} onChange={(e) => setQ(e.target.value)} placeholder="关键字: 动作名或明细, 如 env.update, cleanup" aria-label="搜索日志" />
					</InputGroup>
				</div>
				<div className="w-28"><SelectField value={String(limit)} onChange={(v) => setLimit(Number(v))} options={LIMITS.map((n) => ({ value: String(n), label: "最近 " + n }))} /></div>
				<label className="flex items-center gap-2 t-aux">
					<Switch checked={live} onCheckedChange={(v) => (setFrozen(v ? null : latest), setLive(v))} aria-label="自动滚动" />
					{live ? "自动刷新并滚到最新" : "已冻结"}
				</label>
				<Button size="sm" variant="outline" onClick={() => (setFrozen(null), res.reload())}><RotateCw className="size-4" />刷新</Button>
				<span className="ml-auto t-aux text-muted-foreground">{lines.length + " / " + data.length + " 条"}</span>
			</div>
			{lines.length ? (
				<LogViewer lines={lines} height="calc(100dvh - 15rem)" showTime />
			) : q ? (
				<EmptyState text="没有匹配的日志" hint="换个关键字试试" action={<Button size="sm" variant="outline" onClick={() => setQ("")}>清除搜索</Button>} />
			) : (
				<EmptyState text="后端还没有记录任何操作" action={<Button size="sm" variant="outline" onClick={() => (setFrozen(null), res.reload())}><RotateCw className="size-4" />刷新</Button>} />
			)}
		</div>
	);
}
