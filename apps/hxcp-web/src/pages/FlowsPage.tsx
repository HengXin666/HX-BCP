/**
 * 抓包: 过滤条件写进地址栏 (可分享), 实时尾随 (按 since 增量拉), 时序密度条, 左表右详情.
 * 隧道连接只有一行 CONNECT 记录: 代理看不到路径与内容, 这是不解密的代价.
 */
import { useEffect, useMemo, useRef, useState, type JSX } from "react";
import { Download, RefreshCw } from "lucide-react";
import { Badge, Button, Card, DataTable, EmptyState, Input, NativeSelect, NativeSelectOption, PageHeader, Switch } from "@hx/ui";
import { api, type Flow } from "../lib/api";
import { clock, splitUrl } from "../lib/format";
import { usePoll } from "../lib/hooks";
import { FlowDetail } from "./FlowDetail";
import { sizeOf, tlsLabel, verdictTone } from "./flowView";
import { Spark } from "./Spark";

const KEYS = ["host", "status", "q", "client", "kind", "via", "days"] as const;
type Filters = Record<(typeof KEYS)[number], string>;
const PAGE = 300;

function fromQuery(q: URLSearchParams): Filters {
	const f = { days: "1" } as Filters;
	for (const k of KEYS) f[k] = q.get(k) ?? f[k] ?? "";
	return f;
}

function toQs(f: Filters): string {
	const sp = new URLSearchParams();
	for (const k of KEYS) if (f[k] && !(k === "days" && f[k] === "0")) sp.set(k, f[k]);
	return sp.toString();
}

export function FlowsPage(props: { query: URLSearchParams; onQuery: (q: Record<string, string>) => void }): JSX.Element {
	const [draft, setDraft] = useState<Filters>(() => fromQuery(props.query));
	const [applied, setApplied] = useState<Filters>(draft);
	const [rows, setRows] = useState<Flow[]>([]);
	const [more, setMore] = useState(false);
	const [busy, setBusy] = useState(false);
	const [live, setLive] = useState(false);
	const [sel, setSel] = useState<Flow | null>(null);
	const clients = usePoll(api.clients, 15000);
	const qs = toQs(applied);
	const rowsRef = useRef(rows);
	rowsRef.current = rows;

	const load = (offset: number): void => {
		setBusy(true);
		api
			.flows("limit=" + PAGE + "&offset=" + offset + (qs ? "&" + qs : ""))
			.then((d) => {
				setRows((old) => (offset ? old.concat(d.flows) : d.flows));
				setMore(d.flows.length >= PAGE);
				if (!offset) setSel(null);
			})
			.catch(() => setRows((old) => (offset ? old : [])))
			.finally(() => setBusy(false));
	};

	useEffect(() => {
		props.onQuery(Object.fromEntries(KEYS.map((k) => [k, applied[k]])));
		load(0);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [qs]);

	useEffect(() => {
		if (!live) return;
		const t = setInterval(() => {
			const head = rowsRef.current[0];
			if (!head) return load(0);
			api.flows("limit=500&since=" + head.at + (qs ? "&" + qs : "")).then((d) => {
				if (d.flows.length) setRows((old) => d.flows.concat(old).slice(0, 3000));
			});
		}, 2000);
		return () => clearInterval(t);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [live, qs]);

	const set = (k: keyof Filters, v: string, now = false): void => {
		const next = { ...draft, [k]: v };
		setDraft(next);
		if (now) setApplied(next);
	};
	const apply = (): void => setApplied({ ...draft });
	const onEnter = (e: React.KeyboardEvent): void => {
		if (e.key === "Enter") apply();
	};
	const clientNames = useMemo(() => {
		const names = new Set((clients.data?.clients ?? []).map((c) => c.client));
		if (applied.client) names.add(applied.client);
		return [...names].sort();
	}, [clients.data, applied.client]);

	return (
		<div className="flex min-w-0 flex-col gap-4">
			<PageHeader
				title="抓包"
				desc="每个请求一行. 隧道连接只记 CONNECT, 想看明文去 主机与 TLS 把 host 切到解密."
				right={
					<Button asChild variant="outline" size="sm">
						<a href={"flows.har" + (qs ? "?" + qs : "")} download>
							<Download className="size-4" />
							导出 HAR
						</a>
					</Button>
				}
			/>
			<Card className="flex flex-wrap items-center gap-2 p-3">
				<Input className="h-8 w-44" placeholder="host 后缀" value={draft.host} onChange={(e) => set("host", e.target.value)} onKeyDown={onEnter} />
				<Input className="h-8 w-20" placeholder="状态" value={draft.status} onChange={(e) => set("status", e.target.value)} onKeyDown={onEnter} />
				<Input className="h-8 w-56" placeholder="关键字 (URL / 头 / 参数)" value={draft.q} onChange={(e) => set("q", e.target.value)} onKeyDown={onEnter} />
				<NativeSelect size="sm" value={draft.client} onChange={(e) => set("client", e.target.value, true)}>
					<NativeSelectOption value="">全部客户端</NativeSelectOption>
					{clientNames.map((c) => (
						<NativeSelectOption key={c} value={c}>{c}</NativeSelectOption>
					))}
				</NativeSelect>
				<NativeSelect size="sm" value={draft.kind} onChange={(e) => set("kind", e.target.value, true)}>
					<NativeSelectOption value="">全部类型</NativeSelectOption>
					<NativeSelectOption value="flow">请求</NativeSelectOption>
					<NativeSelectOption value="hit">命中</NativeSelectOption>
					<NativeSelectOption value="tunnel">隧道</NativeSelectOption>
				</NativeSelect>
				<NativeSelect size="sm" value={draft.via} onChange={(e) => set("via", e.target.value, true)}>
					<NativeSelectOption value="">全部入口</NativeSelectOption>
					<NativeSelectOption value="proxy">代理档</NativeSelectOption>
					<NativeSelectOption value="cdp">CDP 档</NativeSelectOption>
				</NativeSelect>
				<NativeSelect size="sm" value={draft.days} onChange={(e) => set("days", e.target.value, true)}>
					<NativeSelectOption value="1">最近 1 天</NativeSelectOption>
					<NativeSelectOption value="3">3 天</NativeSelectOption>
					<NativeSelectOption value="7">7 天</NativeSelectOption>
					<NativeSelectOption value="0">全部</NativeSelectOption>
				</NativeSelect>
				<Button size="sm" variant="primary" onClick={apply} disabled={busy}>
					<RefreshCw className={"size-4" + (busy ? " animate-spin" : "")} />
					查询
				</Button>
				<label className="ml-auto inline-flex items-center gap-2 t-aux text-muted-foreground">
					<Switch size="sm" checked={live} onCheckedChange={setLive} aria-label="实时" />
					实时尾随
				</label>
			</Card>
			<Spark rows={rows} />
			<div className="grid min-w-0 gap-3 xl:grid-cols-[minmax(0,1.5fr)_minmax(380px,1fr)]" data-density="compact">
				<div className="flex min-w-0 flex-col gap-2">
					<DataTable<Flow>
						rows={rows}
						rowKey={(r, i) => r.at + ":" + i}
						loading={busy && !rows.length}
						maxHeight="calc(100dvh - 330px)"
						onRowClick={setSel}
						empty={<EmptyState compact text="没有匹配的请求" hint={qs ? "当前过滤: " + qs : "让浏览器走代理访问几个页面"} />}
						tableClassName="flow-table"
						columns={[
							{ key: "at", header: "时间", width: "88px", render: (r) => <span className="font-mono tabular-nums" data-selected={sel === r}>{clock(r.at)}</span> },
							{ key: "client", header: "客户端", width: "72px", render: (r) => <span className="font-mono t-aux">{r.client ?? "-"}</span> },
							{ key: "method", header: "方法", width: "64px", render: (r) => <span className="font-mono t-aux">{r.method ?? "GET"}</span> },
							{
								key: "url",
								header: "host / path",
								render: (r) => {
									const [h, p] = splitUrl(r.url);
									return (
										<div className="min-w-0 max-w-[280px] truncate" title={r.url}>
											<span>{h}</span>
											<span className="ml-2 font-mono t-aux text-muted-foreground">{p}</span>
										</div>
									);
								},
							},
							{ key: "status", header: "状态", width: "56px", numeric: true, render: (r) => <StatusText s={r.status} /> },
							{ key: "size", header: "大小", width: "80px", numeric: true, render: sizeOf },
							{ key: "ms", header: "耗时", width: "64px", numeric: true, render: (r) => (r.ms != null ? r.ms + "ms" : "-") },
							{
								key: "tls",
								header: "TLS",
								width: "64px",
								render: (r) => {
									const [t, tone] = tlsLabel(r);
									return <Badge variant={tone}>{t}</Badge>;
								},
							},
							{
								key: "verdict",
								header: "判定",
								width: "120px",
								render: (r) => (
									<Badge variant={verdictTone(r)} className="max-w-[112px] truncate" title={r.verdict ?? r.kind}>
										{r.verdict ?? r.kind}
									</Badge>
								),
							},
						]}
					/>
					<div className="flex items-center gap-3 t-aux text-muted-foreground">
						<span>已载 {rows.length} 条{live ? ", 实时中" : ""}</span>
						{more ? (
							<Button size="xs" variant="ghost" onClick={() => load(rows.length)} disabled={busy}>
								加载更多
							</Button>
						) : null}
					</div>
				</div>
				<FlowDetail
					flow={sel}
					onHost={(h) => setApplied({ ...draft, host: h })}
					onClient={(c) => setApplied({ ...draft, client: c })}
				/>
			</div>
		</div>
	);
}

function StatusText({ s }: { s?: number }): JSX.Element {
	const v = Number(s ?? 0);
	const cls = !v ? "text-muted-foreground" : v < 300 ? "text-success" : v < 400 ? "text-link" : v < 500 ? "text-warning" : "text-destructive";
	return <span className={"font-mono " + cls}>{v || "-"}</span>;
}
