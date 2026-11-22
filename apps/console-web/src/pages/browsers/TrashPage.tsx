/**
 * 最近删除: 列出移入回收站的浏览器, 支持勾选后 恢复 / 永久删除, 以及清空. 永久删除不可撤销, 走 confirmDialog 的 danger 档.
 * 列表与写操作都走 opsApi (trash 读, restore / purge 写), 写后由 api-ops 失效缓存, 这里 reload.
 */
import { useState, type JSX } from "react";
import { Button, Checkbox, DataTable, EmptyState, confirmDialog, toast, type DataTableColumn } from "@hx/ui";
import type { Env, Result } from "../../lib/api";
import { opsApi, ago } from "../../lib/api-ops";
import { runBatch } from "../../lib/api-write";
import { usePoll } from "../../lib/hooks";
import { PageLoading } from "../PageLoading";
import { PageTitle } from "../PageTitle";

/** 后端时间戳可能是秒也可能是毫秒, 统一成毫秒再交给 ago. */
const toMs = (t: number | undefined): number => (!t ? 0 : t < 1e12 ? t * 1000 : t);

type Row = Env & { deleted_at?: number };

export function TrashPage(): JSX.Element {
	const trash = usePoll(opsApi.trash, 15000);
	const [picked, setPicked] = useState<Set<string>>(new Set());
	const [busy, setBusy] = useState(false);

	const rows: Row[] = trash.result?.ok ? (trash.result.data as Row[]) : [];
	const ids = picked.size ? [...picked].filter((id) => rows.some((r) => r.id === id)) : [];

	const toggle = (id: string): void =>
		setPicked((p) => {
			const n = new Set(p);
			n.has(id) ? n.delete(id) : n.add(id);
			return n;
		});

	const exec = async (label: string, target: string[], call: (id: string) => Promise<Result<unknown>>): Promise<void> => {
		setBusy(true);
		const r = await runBatch(target, call);
		setBusy(false);
		setPicked(new Set());
		r.failed.length ? toast.error(label + ": 成功 " + r.done + ", 失败 " + r.failed.length + ", 例: " + r.failed[0].error) : toast.success(label + " " + r.done + " 个完成");
		trash.reload();
	};

	const purge = async (target: string[]): Promise<void> => {
		const ok = await confirmDialog({
			title: "永久删除 " + target.length + " 个浏览器?",
			message: rows.filter((r) => target.includes(r.id)).slice(0, 5).map((r) => r.name).join(", "),
			hint: "配置, Cookie 与用户数据一并删除, 无法恢复.",
			confirmLabel: "永久删除",
			cancelLabel: "取消",
			variant: "danger",
		});
		if (ok) await exec("永久删除", target, opsApi.purge);
	};

	const columns: DataTableColumn<Row>[] = [
		{
			key: "pick",
			header: <Checkbox checked={rows.length > 0 && ids.length === rows.length} onCheckedChange={(c) => setPicked(c ? new Set(rows.map((r) => r.id)) : new Set())} />,
			render: (r) => <Checkbox checked={picked.has(r.id)} onCheckedChange={() => toggle(r.id)} onClick={(ev) => ev.stopPropagation()} />,
			width: "2.25rem",
		},
		{ key: "name", header: "名称", render: (r) => <span className="truncate">{r.name}</span> },
		{ key: "folder", header: "原文件夹", render: (r) => <span className="text-muted-foreground">{r.folder || "未分组"}</span> },
		{ key: "lane", header: "车道", render: (r) => (r.lane === "agent" ? "Agent" : "人工") },
		{ key: "at", header: "删除于", render: (r) => <span className="text-muted-foreground">{r.deleted_at ? ago(toMs(r.deleted_at)) : "-"}</span> },
		{
			key: "ops",
			header: "",
			render: (r) => (
				<div className="flex justify-end gap-1">
					<Button size="sm" variant="ghost" disabled={busy} onClick={() => void exec("恢复", [r.id], opsApi.restore)}>
						恢复
					</Button>
					<Button size="sm" variant="ghost" disabled={busy} onClick={() => void purge([r.id])}>
						永久删除
					</Button>
				</div>
			),
		},
	];

	if (trash.loading) return <PageLoading />;
	if (!trash.result?.ok) return <EmptyState text="未连接" hint={"读取最近删除失败: " + (trash.result ? trash.result.error : "")} />;

	return (
		<div className="flex min-w-0 flex-col gap-4" data-page="trash">
			<PageTitle
				title="最近删除"
				desc={rows.length + " 个浏览器, 恢复后回到原文件夹"}
				right={
					<div className="flex gap-2">
						<Button size="sm" variant="outline" disabled={busy || !ids.length} onClick={() => void exec("恢复", ids, opsApi.restore)}>
							恢复选中{ids.length ? " " + ids.length : ""}
						</Button>
						<Button size="sm" variant="outline" disabled={busy || !ids.length} onClick={() => void purge(ids)}>
							永久删除选中
						</Button>
						<Button size="sm" variant="destructive" disabled={busy || !rows.length} onClick={() => void purge(rows.map((r) => r.id))}>
							清空
						</Button>
					</div>
				}
			/>
			<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} empty={<EmptyState text="最近删除是空的" hint="在浏览器页删除的浏览器会先到这里" />} bare />
		</div>
	);
}
