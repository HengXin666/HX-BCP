/**
 * 扩展 #/resources/extensions: 工具条 (搜索, 新建) + 表格 (名称, 路径, 分类, 已绑定, 操作) + 编辑对话框 + 绑定对话框.
 * 绑定是覆盖式: 后端按提交的 env_ids 同步每个环境的 launch.extensions, 不在列表里的环境会被解绑. 只列人类组环境.
 */
import { useEffect, useMemo, useState, type JSX } from "react";
import { Link2, Pencil, Plus, Trash2 } from "lucide-react";
import { Badge, Button, DataTable, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, EmptyState, Input, type DataTableColumn } from "@hx/ui";
import { extensionApi, type ExtensionBody, type ExtensionItem } from "../../lib/api-res";
import { usePoll } from "../../lib/hooks";
import { EnvPicker, useHumanEnvs } from "./EnvPicker";
import { IconBtn, ResToolbar, Row, confirmRemove, fmtTime, matchAny, splitList, submit } from "./ResParts";

type Edit = { mode: "create" } | { mode: "edit"; item: ExtensionItem } | null;

function ExtDialog(props: { target: Edit; busy: boolean; onClose: () => void; onSubmit: (b: ExtensionBody) => void }): JSX.Element {
	const t = props.target;
	const [name, setName] = useState("");
	const [path, setPath] = useState("");
	const [cats, setCats] = useState("");
	const [plats, setPlats] = useState("");
	useEffect(() => {
		const i = t?.mode === "edit" ? t.item : null;
		setName(i?.name ?? "");
		setPath(i?.path ?? "");
		setCats((i?.categories ?? []).join(", "));
		setPlats((i?.platforms ?? []).join(", "));
	}, [t]);
	return (
		<Dialog open={!!t} onOpenChange={(o) => (o ? undefined : props.onClose())}>
			<DialogContent className="sm:max-w-lg">
				<DialogHeader>
					<DialogTitle>{t?.mode === "edit" ? "编辑扩展" : "新建扩展"}</DialogTitle>
					<DialogDescription>路径是 8325 所在机器上解压后的扩展目录, 启动时作为 --load-extension 传给浏览器.</DialogDescription>
				</DialogHeader>
				<div className="flex flex-col gap-3">
					<Row label="名称"><Input value={name} onChange={(e) => setName(e.target.value)} /></Row>
					<Row label="目录路径"><Input className="font-mono" value={path} placeholder="/home/hx/.bcp/extensions/ublock" onChange={(e) => setPath(e.target.value)} /></Row>
					<Row label="分类" hint="逗号或空格分隔"><Input value={cats} onChange={(e) => setCats(e.target.value)} /></Row>
					<Row label="平台" hint="逗号或空格分隔, 如 google youtube"><Input value={plats} onChange={(e) => setPlats(e.target.value)} /></Row>
				</div>
				<DialogFooter>
					<Button variant="outline" onClick={props.onClose}>取消</Button>
					<Button disabled={!name.trim() || !path.trim() || props.busy} onClick={() => props.onSubmit({ name: name.trim(), path: path.trim(), categories: splitList(cats), platforms: splitList(plats) })}>
						{props.busy ? "保存中" : "保存"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

function BindDialog(props: { item: ExtensionItem | null; onClose: () => void; onDone: () => void }): JSX.Element {
	const { envs, error } = useHumanEnvs();
	const [ids, setIds] = useState<string[]>([]);
	const [busy, setBusy] = useState(false);
	const item = props.item;
	useEffect(() => setIds(item?.bind_env_ids ?? []), [item]);
	const save = async (): Promise<void> => {
		if (!item) return;
		setBusy(true);
		const ok = await submit("绑定 " + item.name, () => extensionApi.bind(item.id, ids));
		setBusy(false);
		if (ok) props.onDone();
	};
	return (
		<Dialog open={!!item} onOpenChange={(o) => (o ? undefined : props.onClose())}>
			<DialogContent className="sm:max-w-lg">
				<DialogHeader>
					<DialogTitle>{"绑定 " + (item?.name ?? "") + " 到环境"}</DialogTitle>
					<DialogDescription>覆盖式: 保存后只有选中的环境带这个扩展, 下次启动生效. API 组环境由 api 或 mcp 管理, 不在候选里.</DialogDescription>
				</DialogHeader>
				{error ? <p className="t-aux text-destructive">{"环境列表不可达: " + error}</p> : null}
				<EnvPicker envs={envs} value={ids} onChange={setIds} />
				<DialogFooter>
					<Button variant="outline" onClick={props.onClose}>取消</Button>
					<Button disabled={busy} onClick={() => void save()}>{busy ? "保存中" : "保存绑定 (" + ids.length + ")"}</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

export function ExtensionsPage(): JSX.Element {
	const res = usePoll(extensionApi.list, 15000);
	const [q, setQ] = useState("");
	const [edit, setEdit] = useState<Edit>(null);
	const [bind, setBind] = useState<ExtensionItem | null>(null);
	const [busy, setBusy] = useState(false);
	const all = res.result?.ok ? res.result.data : [];
	const rows = useMemo(() => all.filter((x) => matchAny(q, x.name, x.path, ...x.categories, ...x.platforms)), [all, q]);

	const save = async (b: ExtensionBody): Promise<void> => {
		setBusy(true);
		const t = edit;
		const ok = t?.mode === "edit" ? await submit("保存扩展", () => extensionApi.update(t.item.id, b)) : await submit("新建扩展", () => extensionApi.create(b));
		setBusy(false);
		if (ok) (setEdit(null), res.reload());
	};

	const cols: DataTableColumn<ExtensionItem>[] = [
		{ key: "name", header: "名称", cellClassName: "max-w-[12rem] truncate t-body", render: (x) => x.name || "未命名" },
		{ key: "path", header: "路径", cellClassName: "max-w-0 w-full min-w-[12rem] truncate font-mono t-aux text-muted-foreground", render: (x) => <span title={x.path}>{x.path}</span> },
		{ key: "cats", header: "分类", render: (x) => <div className="flex gap-1">{x.categories.slice(0, 2).map((c) => <Badge key={c} variant="outline">{c}</Badge>)}{x.categories.length > 2 ? <Badge variant="secondary">{"+" + (x.categories.length - 2)}</Badge> : null}</div> },
		{ key: "bound", header: "已绑定", numeric: true, render: (x) => x.bind_env_ids.length },
		{ key: "at", header: "添加于", cellClassName: "whitespace-nowrap t-aux text-muted-foreground", render: (x) => fmtTime(x.created_at) },
		{
			key: "act",
			header: "",
			align: "right",
			width: "7.5rem",
			render: (x) => (
				<div className="flex justify-end gap-1">
					<IconBtn label="绑定到环境" onClick={() => setBind(x)}><Link2 className="size-4" /></IconBtn>
					<IconBtn label="编辑" onClick={() => setEdit({ mode: "edit", item: x })}><Pencil className="size-4" /></IconBtn>
					<IconBtn label="删除" onClick={() => void confirmRemove("扩展", x.name, () => extensionApi.remove(x.id)).then((ok) => ok && res.reload())}><Trash2 className="size-4" /></IconBtn>
				</div>
			),
		},
	];

	if (res.result && !res.result.ok && !all.length) return <EmptyState text="未连接" hint={"GET /api/v1/extensions 失败: " + res.result.error} />;
	return (
		<div className="flex flex-col gap-3" data-res-page="extensions">
			<ResToolbar query={q} onQuery={setQ} placeholder="搜索名称, 路径, 分类" count={rows.length}>
				<Button size="sm" onClick={() => setEdit({ mode: "create" })}><Plus className="size-4" />新建扩展</Button>
			</ResToolbar>
			<DataTable columns={cols} rows={rows} rowKey={(x) => x.id} loading={res.loading && !res.result} emptyText={q ? "没有匹配的扩展" : "还没有扩展, 点新建扩展添加"} bare />
			<ExtDialog target={edit} busy={busy} onClose={() => setEdit(null)} onSubmit={(b) => void save(b)} />
			<BindDialog item={bind} onClose={() => setBind(null)} onDone={() => (setBind(null), res.reload())} />
		</div>
	);
}
