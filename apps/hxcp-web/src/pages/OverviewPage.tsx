/** 总览: 本轮与累计的省流效果, 最省流量的站点, 判定分布, 接入的客户端. */
import type { JSX } from "react";
import { Badge, Card, CardContent, CardHeader, CardTitle, DataTable, EmptyState, HxBarChart, HxPieChart, PageHeader, StatCard, StatGrid, StateDot } from "@hx/ui";
import { api, type Client, type Stats } from "../lib/api";
import { ago, bytes, pct } from "../lib/format";
import { usePoll } from "../lib/hooks";
import { Block } from "./Block";

export function OverviewPage(props: { stats: Stats | null; go: (page: string, q?: Record<string, string>) => void }): JSX.Element {
	const hosts = usePoll(api.hosts, 10000);
	const clients = usePoll(api.clients, 5000);
	const e = props.stats?.engine ?? {};
	const st = props.stats?.store;
	const req = (e.hits ?? 0) + (e.misses ?? 0);
	const top = (hosts.data?.hosts ?? [])
		.filter((h) => h.saved > 0)
		.slice(0, 8)
		.map((h) => ({ host: h.host, saved: Math.round(h.saved / 1024) }));
	const reasons = Object.entries(e.reasons ?? {})
		.sort((a, b) => b[1] - a[1])
		.slice(0, 6)
		.map(([name, value]) => ({ name, value }));

	return (
		<div className="flex min-w-0 flex-col gap-6">
			<PageHeader title="总览" desc="所有入口 (代理档与 CDP 档) 写同一个缓存库, 这里是正在运行的实例的合计." />
			<StatGrid columns={4}>
				<StatCard label="本轮已省流量" value={bytes(e.bytes_served)} hint={"命中本地应答, 出网 " + bytes(e.bytes_network)} tone="good" />
				<StatCard label="字节节省率" value={pct(e.saved_ratio)} hint="已省 / (已省 + 出网)" tone="accent" />
				<StatCard label="命中率" value={req ? pct((e.hits ?? 0) / req) : "-"} hint={(e.hits ?? 0) + " / " + req + " 个可缓存请求"} />
				<StatCard label="累计已省" value={bytes(st?.bytes_served)} hint={"缓存库历史命中 " + (st?.hits ?? 0) + " 次"} tone="good" />
				<StatCard label="缓存库" value={(st?.entries ?? 0) + " 条"} hint={bytes(st?.bytes) + ", 所有浏览器共用"} />
				<StatCard label="隧道 / 解密" value={(e.tunnels ?? 0) + " / " + (e.decrypted ?? 0)} hint="HTTPS 连接, 解密的 host 才能缓存" tone={e.decrypted ? "accent" : "default"} />
				<StatCard label="回源校验" value={String(e.revalidated ?? 0)} hint="过期条目带 ETag 回源, 304 本地应答" />
				<StatCard label="错误" value={String(e.errors ?? 0)} hint="上游或抓取错误" tone={e.errors ? "bad" : "muted"} />
			</StatGrid>
			<div className="grid gap-3 lg:grid-cols-2">
				<Card>
					<CardHeader>
						<CardTitle>最省流量的站点 (KB)</CardTitle>
					</CardHeader>
					<CardContent>
						{top.length ? (
							<HxBarChart data={top} xKey="host" series={[{ dataKey: "saved", name: "已省 KB" }]} height={220} />
						) : (
							<EmptyState compact text="还没有命中" hint="同一资源第二次被任何浏览器访问时开始省流量" />
						)}
					</CardContent>
				</Card>
				<Card>
					<CardHeader>
						<CardTitle>判定分布</CardTitle>
					</CardHeader>
					<CardContent>
						{reasons.length ? <HxPieChart data={reasons} height={220} innerRadius={56} /> : <EmptyState compact text="引擎未运行或还没有请求" />}
					</CardContent>
				</Card>
			</div>
			<Block title="接入的客户端" desc="每个入口端口或 CDP 挂载算一个客户端, 点一行看它的抓包">
				<DataTable<Client>
					rows={clients.data?.clients ?? []}
					rowKey={(c) => c.client}
					loading={clients.loading}
					onRowClick={(c) => props.go("flows", { client: c.client })}
					empty={<EmptyState compact text="还没有客户端接入" hint="去 接入 页拿代理地址" />}
					columns={[
						{ key: "client", header: "客户端", render: (c) => <span className="font-mono">{c.client}</span> },
						{ key: "kinds", header: "入口", render: (c) => c.kinds.map((k) => <Badge key={k} variant="secondary" className="mr-1">{k}</Badge>) },
						{
							key: "state",
							header: "状态",
							render: (c) => (
								<span className="inline-flex items-center gap-2">
									<StateDot tone={c.state === "running" ? "ok" : "idle"} label={c.state} />
									{c.state}
								</span>
							),
						},
						{ key: "updated_at", header: "最近", render: (c) => ago(c.updated_at) },
					]}
				/>
			</Block>
		</div>
	);
}
