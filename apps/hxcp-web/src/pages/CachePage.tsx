/** 缓存: 条目搜索与排序, 侧边抽屉看正文预览, 单条删除, 清理过期. */
import { useEffect, useState, type JSX } from "react";
import { Trash2 } from "lucide-react";
import {
	Badge,
	Button,
	Card,
	DataTable,
	EmptyState,
	Input,
	NativeSelect,
	NativeSelectOption,
	PageHeader,
	Sheet,
	SheetContent,
	SheetDescription,
	SheetHeader,
	SheetTitle,
	confirmDialog,
	toast,
} from "@hx/ui";
import { api, type Entry } from "../lib/api";
import { bytes, clock, copyText } from "../lib/format";
import { usePoll } from "../lib/hooks";
import { Snippet } from "./Snippet";

export function CachePage(): JSX.Element {
	const [q, setQ] = useState("");
	const [order, setOrder] = useState("hits");
	const [applied, setApplied] = useState({ q: "", order: "hits" });
	const list = usePoll(() => api.entries(applied.q, applied.order), 0, [applied.q, applied.order]);
	const [sel, setSel] = useState<Entry | null>(null);

	const purge = async (): Promise<void> => {
		const r = await api.purge();
		toast.success("清理过期 " + r.purged + " 条");
		list.reload();
	};
	const remove = async (e: Entry): Promise<void> => {
		const ok = await confirmDialog({ title: "删除这条缓存?", message: e.url, variant: "danger", confirmLabel: "删除" });
		if (!ok) return;
		await api.deleteEntry(e.url);
		toast.success("已删除");
		setSel(null);
		list.reload();
	};
	const now = Date.now() / 1000;

	return (
		<div className="flex min-w-0 flex-col gap-4">
			<PageHeader
				title="缓存"
				desc="所有浏览器共用的内容寻址缓存库. 命中次数是历史累计."
				right={
					<Button size="sm" variant="outline" onClick={() => void purge()}>
						清理过期
					</Button>
				}
			/>
			<Card className="flex flex-wrap items-center gap-2 p-3">
				<Input
					className="h-8 w-72"
					placeholder="URL 片段"
					value={q}
					onChange={(e) => setQ(e.target.value)}
					onKeyDown={(e) => e.key === "Enter" && setApplied({ q, order })}
				/>
				<NativeSelect size="sm" value={order} onChange={(e) => (setOrder(e.target.value), setApplied({ q, order: e.target.value }))}>
					<NativeSelectOption value="hits">按命中</NativeSelectOption>
					<NativeSelectOption value="recent">按时间</NativeSelectOption>
					<NativeSelectOption value="size">按体积</NativeSelectOption>
				</NativeSelect>
				<Button size="sm" variant="primary" onClick={() => setApplied({ q, order })}>
					查询
				</Button>
				<span className="ml-auto t-aux text-muted-foreground">{list.data ? list.data.count + " 条" : ""}</span>
			</Card>
			<DataTable<Entry>
				rows={list.data?.entries ?? []}
				rowKey={(e) => e.key}
				loading={list.loading}
				onRowClick={setSel}
				empty={<EmptyState compact text="缓存库是空的或没有匹配项" />}
				columns={[
					{
						key: "url",
						header: "URL",
						render: (e) => (
							<div className="min-w-0 max-w-[620px] truncate" title={e.url}>
								<span>{e.host}</span>
								<span className="ml-2 font-mono t-aux text-muted-foreground">{e.url.replace(/^https?:\/\/[^/]+/, "")}</span>
							</div>
						),
					},
					{ key: "type", header: "类型", width: "180px", render: (e) => <span className="t-aux">{e.type}</span> },
					{ key: "size", header: "大小", width: "90px", numeric: true, render: (e) => bytes(e.size) },
					{ key: "hit_count", header: "命中", width: "64px", numeric: true },
					{
						key: "fresh",
						header: "新鲜度",
						width: "80px",
						render: (e) => (e.fresh_until > now ? <Badge variant="success">新鲜</Badge> : <Badge variant="warning">过期</Badge>),
					},
					{ key: "stored_at", header: "入库", width: "110px", render: (e) => <span className="font-mono tabular-nums">{clock(e.stored_at)}</span> },
				]}
			/>
			<EntrySheet entry={sel} onClose={() => setSel(null)} onDelete={(e) => void remove(e)} />
		</div>
	);
}

function EntrySheet(props: { entry: Entry | null; onClose: () => void; onDelete: (e: Entry) => void }): JSX.Element {
	const e = props.entry;
	const [preview, setPreview] = useState<string>("");
	useEffect(() => {
		setPreview("");
		if (e) api.body(e.url).then((b) => setPreview(b.preview)).catch((x: unknown) => setPreview("读不到正文: " + String(x)));
	}, [e]);
	return (
		<Sheet open={!!e} onOpenChange={(o) => !o && props.onClose()}>
			<SheetContent side="right" className="w-full max-w-xl overflow-y-auto sm:max-w-xl">
				{e ? (
					<>
						<SheetHeader>
							<SheetTitle className="break-all font-mono t-body">{e.url}</SheetTitle>
							<SheetDescription>
								{e.type}, {bytes(e.size)}, 命中 {e.hit_count} 次
							</SheetDescription>
						</SheetHeader>
						<div className="flex flex-col gap-3 px-4 pb-4">
							<dl className="grid grid-cols-[96px_1fr] gap-x-3 gap-y-1 t-aux">
								<dt className="text-muted-foreground">状态</dt>
								<dd className="m-0 font-mono">{e.status}</dd>
								<dt className="text-muted-foreground">来源</dt>
								<dd className="m-0 font-mono">{e.source}</dd>
								<dt className="text-muted-foreground">新鲜到</dt>
								<dd className="m-0 font-mono">{new Date(e.fresh_until * 1000).toLocaleString()}</dd>
								<dt className="text-muted-foreground">ETag</dt>
								<dd className="m-0 break-all font-mono">{e.etag || "-"}</dd>
							</dl>
							<div className="flex gap-2">
								<Button size="sm" variant="outline" onClick={() => void copyText(e.url).then(() => toast.success("已复制 URL"))}>
									复制 URL
								</Button>
								<Button size="sm" variant="destructive" onClick={() => props.onDelete(e)}>
									<Trash2 className="size-4" />
									删除
								</Button>
							</div>
							<p className="t-aux text-muted-foreground">正文前 512 字节</p>
							{preview ? <Snippet code={preview} /> : <EmptyState compact kind="loading" />}
						</div>
					</>
				) : null}
			</SheetContent>
		</Sheet>
	);
}
