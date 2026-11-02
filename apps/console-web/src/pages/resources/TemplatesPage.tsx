/**
 * 模板 #/resources/templates: 工具条 (搜索, 新建) + 表格 (名称, 说明, 指纹摘要, 启动页, 操作) + 编辑对话框 + 应用对话框.
 * 应用分两种: 选中的已有人类组环境逐个 PUT 指纹与启动段 (按字段合并, 并发 4);
 * 另可按模板新建 N 个环境 (后端 POST /templates/{id}/apply?count=N, 上限 50).
 */
import { useEffect, useMemo, useState, type JSX } from "react";
import { Pencil, Plus, Stamp, Trash2 } from "lucide-react";
import { Button, DataTable, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, EmptyState, Input, Textarea, toast, type DataTableColumn } from "@hx/ui";
import { templateApi, type TemplateBody, type TemplateItem } from "../../lib/api-res";
import { envApi, runBatch } from "../../lib/api-write";
import { usePoll } from "../../lib/hooks";
import { EnvPicker, useHumanEnvs } from "./EnvPicker";
import { IconBtn, ResToolbar, Row, confirmRemove, fmtTime, matchAny, submit } from "./ResParts";

type Edit = { mode: "create" } | { mode: "edit"; item: TemplateItem } | null;

function fpSummary(t: TemplateItem): string {
	const f = t.fingerprint ?? {};
	return [f.kernel || f.browser, f.os, f.resolution, f.language, f.timezone].filter(Boolean).map(String).join(" / ") || "全部默认";
}
const urlsOf = (t: TemplateItem): string[] => (Array.isArray(t.launch?.urls) ? (t.launch.urls as unknown[]).map(String) : []);

function TplDialog(props: { target: Edit; busy: boolean; onClose: () => void; onSubmit: (b: TemplateBody) => void }): JSX.Element {
	const t = props.target;
	const [name, setName] = useState("");
	const [desc, setDesc] = useState("");
	const [fp, setFp] = useState("{}");
	const [urls, setUrls] = useState("");
	useEffect(() => {
		const i = t?.mode === "edit" ? t.item : null;
		setName(i?.name ?? "");
		setDesc(i?.description ?? "");
		setFp(JSON.stringify(i?.fingerprint ?? {}, null, 2));
		setUrls(i ? urlsOf(i).join("\n") : "");
	}, [t]);
	const parsed = useMemo((): Record<string, unknown> | string => {
		try {
			const v = JSON.parse(fp || "{}") as unknown;
			return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : "指纹必须是 JSON 对象";
		} catch (e) {
			return "JSON 错误: " + String((e as Error).message);
		}
	}, [fp]);
	const bad = typeof parsed === "string";
	const body = (): TemplateBody => {
		const i = t?.mode === "edit" ? t.item : null;
		const list = urls.split(/\n+/).map((u) => u.trim()).filter(Boolean);
		return { name: name.trim(), description: desc.trim(), fingerprint: bad ? {} : parsed, launch: { ...(i?.launch ?? {}), urls: list } };
	};
	return (
		<Dialog open={!!t} onOpenChange={(o) => (o ? undefined : props.onClose())}>
			<DialogContent className="sm:max-w-xl">
				<DialogHeader>
					<DialogTitle>{t?.mode === "edit" ? "编辑模板" : "新建模板"}</DialogTitle>
					<DialogDescription>指纹段字段名同浏览器指纹 (kernel, os, language, timezone, resolution 等), 只写要覆盖的项.</DialogDescription>
				</DialogHeader>
				<div className="flex flex-col gap-3">
					<Row label="名称"><Input value={name} onChange={(e) => setName(e.target.value)} /></Row>
					<Row label="说明"><Input value={desc} onChange={(e) => setDesc(e.target.value)} /></Row>
					<Row label="指纹 (JSON)" hint={bad ? (parsed as string) : undefined}>
						<Textarea className="h-36 font-mono t-aux" value={fp} onChange={(e) => setFp(e.target.value)} aria-invalid={bad} />
					</Row>
					<Row label="启动页" hint="一行一个"><Textarea className="h-20 font-mono t-aux" value={urls} onChange={(e) => setUrls(e.target.value)} /></Row>
				</div>
				<DialogFooter>
					<Button variant="outline" onClick={props.onClose}>取消</Button>
					<Button disabled={!name.trim() || bad || props.busy} onClick={() => props.onSubmit(body())}>{props.busy ? "保存中" : "保存"}</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

function ApplyDialog(props: { item: TemplateItem | null; onClose: () => void; onDone: () => void }): JSX.Element {
	const { envs, error } = useHumanEnvs();
	const [ids, setIds] = useState<string[]>([]);
	const [count, setCount] = useState(0);
	const [busy, setBusy] = useState(false);
	const item = props.item;
	useEffect(() => (setIds([]), setCount(0)), [item]);
	const apply = async (): Promise<void> => {
		if (!item) return;
		setBusy(true);
		if (ids.length) {
			const r = await runBatch(ids, (id) => envApi.update(id, { fingerprint: item.fingerprint, launch: item.launch }));
			if (r.failed.length) toast.error("应用到环境: 成功 " + r.done + ", 失败 " + r.failed.length + ", 例: " + r.failed[0].error);
			else toast.success("已应用到 " + r.done + " 个环境, 在跑的需重启生效");
		}
		if (count > 0) await submit("按模板新建 " + count + " 个", () => templateApi.spawn(item.id, count));
		setBusy(false);
		props.onDone();
	};
	return (
		<Dialog open={!!item} onOpenChange={(o) => (o ? undefined : props.onClose())}>
			<DialogContent className="sm:max-w-lg">
				<DialogHeader>
					<DialogTitle>{"应用模板 " + (item?.name ?? "")}</DialogTitle>
					<DialogDescription>指纹与启动段按字段合并到选中环境. API 组环境由 api 或 mcp 管理, 不在候选里.</DialogDescription>
				</DialogHeader>
				{error ? <p className="t-aux text-destructive">{"环境列表不可达: " + error}</p> : null}
				<Row label="应用到已有环境"><EnvPicker envs={envs} value={ids} onChange={setIds} /></Row>
				<Row label="另按模板新建 (个)" hint="0 表示不新建, 上限 50">
					<Input inputMode="numeric" className="w-24" value={String(count)} onChange={(e) => setCount(Math.min(50, Number(e.target.value.replace(/\D/g, "")) || 0))} />
				</Row>
				<DialogFooter>
					<Button variant="outline" onClick={props.onClose}>取消</Button>
					<Button disabled={busy || (ids.length === 0 && count === 0)} onClick={() => void apply()}>{busy ? "应用中" : "应用"}</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

export function TemplatesPage(): JSX.Element {
	const res = usePoll(templateApi.list, 15000);
	const [q, setQ] = useState("");
	const [edit, setEdit] = useState<Edit>(null);
	const [applyTo, setApplyTo] = useState<TemplateItem | null>(null);
	const [busy, setBusy] = useState(false);
	const all = res.result?.ok ? res.result.data : [];
	const rows = useMemo(() => all.filter((t) => matchAny(q, t.name, t.description, fpSummary(t))), [all, q]);

	const save = async (b: TemplateBody): Promise<void> => {
		setBusy(true);
		const t = edit;
		const ok = t?.mode === "edit" ? await submit("保存模板", () => templateApi.update(t.item.id, b)) : await submit("新建模板", () => templateApi.create(b));
		setBusy(false);
		if (ok) (setEdit(null), res.reload());
	};

	const cols: DataTableColumn<TemplateItem>[] = [
		{ key: "name", header: "名称", cellClassName: "max-w-[12rem] truncate t-body", render: (t) => t.name || "未命名" },
		{ key: "desc", header: "说明", cellClassName: "max-w-[14rem] truncate t-aux text-muted-foreground", render: (t) => <span title={t.description}>{t.description || "-"}</span> },
		{ key: "fp", header: "指纹", cellClassName: "max-w-0 w-full min-w-[12rem] truncate font-mono t-aux", render: (t) => <span title={JSON.stringify(t.fingerprint)}>{fpSummary(t)}</span> },
		{ key: "urls", header: "启动页", numeric: true, render: (t) => urlsOf(t).length },
		{ key: "at", header: "创建于", cellClassName: "whitespace-nowrap t-aux text-muted-foreground", render: (t) => fmtTime(t.created_at) },
		{
			key: "act",
			header: "",
			align: "right",
			width: "7.5rem",
			render: (t) => (
				<div className="flex justify-end gap-1">
					<IconBtn label="应用到环境" onClick={() => setApplyTo(t)}><Stamp className="size-4" /></IconBtn>
					<IconBtn label="编辑" onClick={() => setEdit({ mode: "edit", item: t })}><Pencil className="size-4" /></IconBtn>
					<IconBtn label="删除" onClick={() => void confirmRemove("模板", t.name, () => templateApi.remove(t.id)).then((ok) => ok && res.reload())}><Trash2 className="size-4" /></IconBtn>
				</div>
			),
		},
	];

	if (res.result && !res.result.ok && !all.length) return <EmptyState text="未连接" hint={"GET /api/v1/templates 失败: " + res.result.error} />;
	return (
		<div className="flex flex-col gap-3" data-res-page="templates">
			<ResToolbar query={q} onQuery={setQ} placeholder="搜索名称, 说明, 指纹" count={rows.length}>
				<Button size="sm" onClick={() => setEdit({ mode: "create" })}><Plus className="size-4" />新建模板</Button>
			</ResToolbar>
			<DataTable columns={cols} rows={rows} rowKey={(t) => t.id} loading={res.loading && !res.result} emptyText={q ? "没有匹配的模板" : "还没有模板, 点新建模板添加"} bare />
			<TplDialog target={edit} busy={busy} onClose={() => setEdit(null)} onSubmit={(b) => void save(b)} />
			<ApplyDialog item={applyTo} onClose={() => setApplyTo(null)} onDone={() => setApplyTo(null)} />
		</div>
	);
}
