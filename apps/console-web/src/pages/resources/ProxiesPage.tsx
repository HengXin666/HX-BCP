/**
 * 代理库 #/resources/proxies: 工具条 (搜索, 批量检测, 新建) + 表格 (勾选, 名称与地址, 类型, 状态, 出口, 最近检测, 操作).
 * 批量检测逐个调 POST /proxies/check (并发 4), 结果写回行内; 后端检测不落库, 刷新页面后显示库里上次的国家与延迟.
 */
import { useMemo, useState, type JSX } from "react";
import { LoaderCircle, Pencil, Plus, Radar, Trash2 } from "lucide-react";
import { Badge, Button, Checkbox, DataTable, EmptyState, type DataTableColumn } from "@hx/ui";
import { proxyApi, type ProxyBody, type ProxyItem } from "../../lib/api-res";
import { checkExit, runBatch, type ExitCheck } from "../../lib/api-write";
import { usePoll } from "../../lib/hooks";
import { proxyAddr } from "../../lib/proxy-parse";
import { ProxyDialog, type ProxyTarget } from "./ProxyDialog";
import { IconBtn, ResToolbar, confirmRemove, fmtTime, matchAny, submit } from "./ResParts";

type Checked = ExitCheck & { at: number };

function StatusCell(props: { p: ProxyItem; r?: Checked; pending: boolean }): JSX.Element {
	if (props.pending) return <Badge variant="secondary"><LoaderCircle className="size-3 animate-spin" />检测中</Badge>;
	const r = props.r;
	if (r) return r.ok ? <Badge variant="success">{"通 " + (r.latency_ms ?? "?") + "ms"}</Badge> : <Badge variant="destructive" title={r.error}>不通</Badge>;
	if (props.p.checked_at) return <Badge variant="outline">{"上次 " + (props.p.latency_ms ?? "?") + "ms"}</Badge>;
	return <span className="t-aux text-muted-foreground">未检测</span>;
}

function exitText(p: ProxyItem, r?: Checked): string {
	if (r) return r.ok ? [r.ip, r.country, r.city].filter(Boolean).join(" ") : r.error || "失败";
	return p.country ? p.country + " (库里上次)" : "-";
}

export function ProxiesPage(): JSX.Element {
	const res = usePoll(proxyApi.list, 15000);
	const [q, setQ] = useState("");
	const [picked, setPicked] = useState<Set<string>>(new Set());
	const [checks, setChecks] = useState<Record<string, Checked>>({});
	const [pending, setPending] = useState<Set<string>>(new Set());
	const [target, setTarget] = useState<ProxyTarget>(null);
	const [busy, setBusy] = useState(false);

	const all = res.result?.ok ? res.result.data : [];
	const rows = useMemo(() => all.filter((p) => matchAny(q, p.label, p.host, String(p.port), p.type, p.country)), [all, q]);
	const ids = rows.map((p) => p.id);
	const on = ids.filter((id) => picked.has(id)).length;
	const head = on === 0 ? false : on === ids.length ? true : "indeterminate";
	const pick = (list: string[], v: boolean): void =>
		setPicked((cur) => {
			const n = new Set(cur);
			for (const id of list) (v ? n.add(id) : n.delete(id));
			return n;
		});

	const checkPicked = async (): Promise<void> => {
		const list = all.filter((p) => picked.has(p.id));
		setPending(new Set(list.map((p) => p.id)));
		await runBatch(list.map((p) => p.id), async (id) => {
			const p = list.find((x) => x.id === id) as ProxyItem;
			const r = await checkExit(p);
			const data: Checked = r.ok ? { ...r.data, at: Date.now() } : { ok: false, error: r.error, at: Date.now() };
			setChecks((m) => ({ ...m, [id]: data }));
			setPending((s) => {
				const n = new Set(s);
				n.delete(id);
				return n;
			});
			return r;
		});
	};
	const save = async (body: ProxyBody): Promise<void> => {
		setBusy(true);
		const t = target;
		const ok =
			t?.mode === "edit"
				? await submit("保存代理", () => proxyApi.update(t.item.id, body.password ? body : { ...body, password: undefined }))
				: await submit("新建代理", () => proxyApi.create(body));
		setBusy(false);
		if (ok) (setTarget(null), res.reload());
	};
	const remove = async (p: ProxyItem): Promise<void> => {
		if (await confirmRemove("代理", p.label || proxyAddr(p), () => proxyApi.remove(p.id))) (pick([p.id], false), res.reload());
	};

	const cols: DataTableColumn<ProxyItem>[] = [
		{
			key: "pick",
			width: "2.25rem",
			header: <Checkbox checked={head} disabled={ids.length === 0} onCheckedChange={(v) => pick(ids, v === true)} aria-label="选择全部代理" />,
			render: (p) => <Checkbox checked={picked.has(p.id)} onCheckedChange={(v) => pick([p.id], v === true)} aria-label={"选择 " + (p.label || p.host)} />,
		},
		{
			key: "addr",
			header: "名称 / 地址",
			cellClassName: "max-w-0 w-full min-w-[14rem]",
			render: (p) => (
				<div className="flex min-w-0 flex-col">
					<span className="truncate t-body">{p.label || "未命名"}</span>
					<span className="truncate font-mono t-aux text-muted-foreground" title={proxyAddr(p) + (p.username ? " (有凭据)" : "")}>{proxyAddr(p) + (p.username ? " (有凭据)" : "")}</span>
				</div>
			),
		},
		{ key: "type", header: "类型", cellClassName: "font-mono t-aux", render: (p) => p.type },
		{ key: "status", header: "状态", cellClassName: "whitespace-nowrap", render: (p) => <StatusCell p={p} r={checks[p.id]} pending={pending.has(p.id)} /> },
		{ key: "exit", header: "出口 / 失败原因", cellClassName: "max-w-[14rem] truncate t-aux", render: (p) => <span title={exitText(p, checks[p.id])}>{exitText(p, checks[p.id])}</span> },
		{ key: "at", header: "最近检测", cellClassName: "whitespace-nowrap t-aux text-muted-foreground", render: (p) => fmtTime(checks[p.id]?.at ?? p.checked_at) },
		{
			key: "act",
			header: "",
			align: "right",
			width: "5.5rem",
			render: (p) => (
				<div className="flex justify-end gap-1">
					<IconBtn label="编辑" onClick={() => setTarget({ mode: "edit", item: p })}><Pencil className="size-4" /></IconBtn>
					<IconBtn label="删除" onClick={() => void remove(p)}><Trash2 className="size-4" /></IconBtn>
				</div>
			),
		},
	];

	if (res.result && !res.result.ok && !all.length) return <EmptyState text="未连接" hint={"GET /api/v1/proxies 失败: " + res.result.error} />;
	return (
		<div className="flex flex-col gap-3" data-res-page="proxies">
			<ResToolbar query={q} onQuery={setQ} placeholder="搜索名称, 主机, 端口, 国家" count={rows.length}>
				<Button size="sm" variant="outline" disabled={picked.size === 0 || pending.size > 0} onClick={() => void checkPicked()}>
					{pending.size ? <LoaderCircle className="size-4 animate-spin" /> : <Radar className="size-4" />}
					{"检测选中" + (picked.size ? " (" + picked.size + ")" : "")}
				</Button>
				<Button size="sm" onClick={() => setTarget({ mode: "create" })}><Plus className="size-4" />新建代理</Button>
			</ResToolbar>
			<DataTable columns={cols} rows={rows} rowKey={(p) => p.id} loading={res.loading && !res.result} empty={q ? <EmptyState text="没有匹配的代理" hint="换个关键字试试" action={<Button size="sm" variant="outline" onClick={() => setQ("")}>清除搜索</Button>} /> : <EmptyState text="代理库是空的" hint="新建一条代理, 再挂到环境上" action={<Button size="sm" onClick={() => setTarget({ mode: "create" })}><Plus className="size-4" />新建代理</Button>} />} bare />
			<ProxyDialog target={target} busy={busy} onClose={() => setTarget(null)} onSubmit={(b) => void save(b)} />
		</div>
	);
}
