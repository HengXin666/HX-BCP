/**
 * 孤儿进程 #/system/orphans: GET /orphans 列出在跑但无人认领的浏览器主进程 (服务重启后失联的遗留), POST /orphans/cleanup 杀掉全部.
 * 清理只杀进程, 不删 profile 目录; 后端不报进程占用与目录大小, 确认框里如实写 "未上报".
 */
import type { JSX } from "react";
import { Eraser, RotateCw } from "lucide-react";
import { Button, DataTable, EmptyState, confirmDialog, toast, type DataTableColumn } from "@hx/ui";
import { systemApi, type Orphan } from "../../lib/api-res";
import { usePoll } from "../../lib/hooks";

const cols: DataTableColumn<Orphan>[] = [
	{ key: "pid", header: "PID", numeric: true, cellClassName: "font-mono t-aux", render: (o) => o.pid },
	{ key: "env", header: "环境 id", cellClassName: "font-mono t-aux whitespace-nowrap", render: (o) => o.env_id || "未上报" },
	{ key: "cmd", header: "命令行 (前 240 字)", cellClassName: "max-w-0 w-full truncate font-mono t-aux text-muted-foreground", render: (o) => <span title={o.cmdline}>{o.cmdline}</span> },
];

export function OrphansPage(): JSX.Element {
	const res = usePoll(systemApi.orphans, 15000);
	const list = res.result?.ok ? res.result.data.orphans : [];
	const envs = new Set(list.map((o) => o.env_id).filter(Boolean));

	const cleanup = async (): Promise<void> => {
		const yes = await confirmDialog({
			title: "清理 " + list.length + " 个孤儿进程?",
			message: "涉及 " + envs.size + " 个环境目录 (" + [...envs].slice(0, 5).join(", ") + (envs.size > 5 ? " 等" : "") + "), 总大小 未上报 (后端不统计).",
			hint: "只杀进程 (按进程组, 子进程一起走), 不删 profile 目录, 环境与登录态都在.",
			confirmLabel: "杀掉进程",
			cancelLabel: "保留",
			variant: "danger",
		});
		if (!yes) return;
		const r = await systemApi.cleanup();
		if (r.ok) toast.success("已清理 " + r.data.count + " 个进程");
		else toast.error("清理失败: " + r.error);
		res.reload();
	};

	if (res.result && !res.result.ok) return <EmptyState text="未连接" hint={"GET /api/v1/orphans 失败: " + res.result.error} />;
	return (
		<div className="flex flex-col gap-3" data-sys-page="orphans" data-orphan-count={list.length}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="t-body">{"孤儿进程 " + list.length + " 个, 涉及环境 " + envs.size + " 个"}</span>
				<Button size="sm" variant="outline" className="ml-auto" onClick={res.reload}><RotateCw className="size-4" />重新扫描</Button>
				<Button size="sm" variant="destructive" disabled={!list.length} onClick={() => void cleanup()}><Eraser className="size-4" />全部清理</Button>
			</div>
			<DataTable columns={cols} rows={list} rowKey={(o) => String(o.pid)} loading={res.loading && !res.result} empty={<EmptyState text="没有孤儿进程" hint="每个在跑的浏览器都有人认领" action={<Button size="sm" variant="outline" onClick={res.reload}><RotateCw className="size-4" />重新扫描</Button>} />} bare />
		</div>
	);
}
