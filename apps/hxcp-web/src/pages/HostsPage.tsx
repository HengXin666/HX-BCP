/**
 * 主机与 TLS: 每个 host 的连接, 客户端, 缓存规模, 已省字节; 一键切换 隧道 / 解密 / 永不解密.
 * 解密会改变上游看到的握手, 页首写明代价; 全部解密要二次确认.
 */
import { useMemo, useState, type JSX } from "react";
import { Alert, AlertDescription, AlertTitle, Badge, Button, ButtonGroup, Card, DataTable, EmptyState, Input, PageHeader, Switch, confirmDialog, toast } from "@hx/ui";
import { api, type HostRow, type TlsMode } from "../lib/api";
import { bytes } from "../lib/format";
import { usePoll } from "../lib/hooks";

const MODE_LABEL: Record<TlsMode, string> = { decrypt: "解密", tunnel: "隧道", never: "永不解密" };

function current(h: HostRow): TlsMode {
	if (h.mode === "decrypt") return "decrypt";
	return h.reason.startsWith("never:") ? "never" : "tunnel";
}

export function HostsPage(): JSX.Element {
	const hosts = usePoll(api.hosts, 5000);
	const rules = usePoll(api.rules, 5000);
	const [q, setQ] = useState("");
	const [add, setAdd] = useState("");

	const rows = useMemo(() => {
		const list = [...(hosts.data?.hosts ?? [])];
		const r = rules.data;
		for (const h of [...(r?.decrypt ?? []), ...(r?.never_decrypt ?? [])]) {
			if (list.some((x) => x.host === h)) continue;
			const dec = r?.decrypt.includes(h);
			list.push({ host: h, entries: 0, bytes: 0, hits: 0, saved: 0, conns: 0, clients: [], last: 0, mode: dec ? "decrypt" : "tunnel", reason: (dec ? "rule:" : "never:") + h });
		}
		const k = q.trim().toLowerCase();
		return k ? list.filter((h) => h.host.includes(k)) : list;
	}, [hosts.data, rules.data, q]);

	const set = async (host: string, mode: TlsMode): Promise<void> => {
		if (!host.trim()) return;
		try {
			await api.setHost(host.trim(), mode);
			toast.success(host + " 已切到 " + MODE_LABEL[mode] + ", 约 1 秒后对新连接生效");
			hosts.reload();
			rules.reload();
		} catch (e) {
			toast.error(String(e));
		}
	};
	const decryptAll = async (on: boolean): Promise<void> => {
		if (on) {
			const ok = await confirmDialog({
				title: "全部解密?",
				message: "所有站点都会看到代理的 TLS 握手, 只建议调试时打开. 永不解密列表仍然生效.",
				variant: "danger",
				confirmLabel: "打开",
			});
			if (!ok) return;
		}
		const r = rules.data ?? { decrypt: [], never_decrypt: [], decrypt_all: false };
		await api.putRules({ ...r, decrypt_all: on });
		rules.reload();
	};

	return (
		<div className="flex min-w-0 flex-col gap-4">
			<PageHeader
				title="主机与 TLS"
				desc="按 host 后缀决定 HTTPS 是隧道还是解密. 规则文件热加载, 控制台, CLI, MCP 改的是同一份."
				right={
					<label className="inline-flex items-center gap-2 t-aux text-muted-foreground">
						<Switch size="sm" checked={!!rules.data?.decrypt_all} onCheckedChange={(v) => void decryptAll(v)} aria-label="全部解密" />
						全部解密 (仅调试)
					</label>
				}
			/>
			<Alert variant="warning">
				<AlertTitle>默认隧道, 指纹不变</AlertTitle>
				<AlertDescription>
					隧道: 浏览器自己与站点握手, JA3/JA4 与直连相同, 但代理看不到内容, 也缓存不了. 解密: 纯静态 CDN 才建议切过去, 上游会看到代理的握手,
					浏览器要信任本地 CA (见 接入). 登录, API, 风控相关域名放进永不解密.
				</AlertDescription>
			</Alert>
			<Card className="flex flex-wrap items-center gap-2 p-3">
				<Input className="h-8 w-56" placeholder="过滤 host" value={q} onChange={(e) => setQ(e.target.value)} />
				<Input className="h-8 w-56" placeholder="手动添加 host 后缀" value={add} onChange={(e) => setAdd(e.target.value)} />
				<Button size="sm" variant="outline" onClick={() => void set(add, "decrypt").then(() => setAdd(""))}>
					加入解密
				</Button>
				<Button size="sm" variant="outline" onClick={() => void set(add, "never").then(() => setAdd(""))}>
					加入永不解密
				</Button>
			</Card>
			<DataTable<HostRow>
				rows={rows}
				rowKey={(h) => h.host}
				loading={hosts.loading}
				empty={<EmptyState compact text="还没看到任何主机" hint="让浏览器走代理访问几个页面" />}
				columns={[
					{
						key: "host",
						header: "host",
						render: (h) => (
							<div className="flex flex-col">
								<span className="font-mono">{h.host}</span>
								<span className="t-aux text-muted-foreground">{h.reason}</span>
							</div>
						),
					},
					{
						key: "mode",
						header: "当前",
						width: "96px",
						render: (h) => {
							const m = current(h);
							return <Badge variant={m === "decrypt" ? "warning" : m === "never" ? "destructive" : "outline"}>{MODE_LABEL[m]}</Badge>;
						},
					},
					{ key: "conns", header: "连接", width: "64px", numeric: true },
					{ key: "clients", header: "客户端", render: (h) => <span className="t-aux">{h.clients.join(", ") || "-"}</span> },
					{ key: "entries", header: "缓存条目", width: "80px", numeric: true },
					{ key: "bytes", header: "占用", width: "90px", numeric: true, render: (h) => bytes(h.bytes) },
					{ key: "saved", header: "已省", width: "90px", numeric: true, render: (h) => bytes(h.saved) },
					{
						key: "switch",
						header: "切换",
						width: "220px",
						render: (h) => {
							const m = current(h);
							return (
								<ButtonGroup>
									{(["tunnel", "decrypt", "never"] as const).map((k) => (
										<Button key={k} size="xs" variant={m === k ? "secondary" : "outline"} disabled={m === k} onClick={() => void set(h.host, k)}>
											{MODE_LABEL[k]}
										</Button>
									))}
								</ButtonGroup>
							);
						},
					},
				]}
			/>
		</div>
	);
}
