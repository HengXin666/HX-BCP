import { createContext, useContext, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { confirmDialog, toast } from "@hx/ui";
import type { Env, Result } from "../../lib/api";
import { batchOp, envApi, folderApi, type BatchOp, type BatchOutcome } from "../../lib/api-write";
import { kernelPatch, type Kernel } from "../../lib/kernels";
import type { BatchAction } from "./BatchBar";
import { parseTags, type DialogState, type DialogValues } from "./EnvDialogs";
import type { EnvAction } from "./EnvCells";
import type { FpTarget } from "./FpDialog";

/** 每行的异步反馈: busy = 请求中 (按钮转圈), done = 刚成功 (短暂对勾), fail = 刚失败. */
export type RowPhase = { op: string; phase: "busy" | "done" | "fail" };
export const PendingContext = createContext<Record<string, RowPhase>>({});
export const usePending = (id: string): RowPhase | undefined => useContext(PendingContext)[id];

const FLASH_MS = 1200;

function report(label: string, r: BatchOutcome): void {
	if (r.failed.length === 0) toast.success(label + " " + r.done + " 个完成");
	else toast.error(label + ": 成功 " + r.done + ", 失败 " + r.failed.length + ", 例: " + r.failed[0].error);
}

/** 删除确认的正文: 列出名字 (最多 5 个), 说明去向. 危险按钮由 confirmDialog 的 danger 档给出. */
function askDelete(names: string[]): Promise<boolean> {
	const head = names.slice(0, 5).join(", ") + (names.length > 5 ? " 等 " + names.length + " 个" : "");
	return confirmDialog({
		title: names.length > 1 ? "删除选中的 " + names.length + " 个浏览器?" : "删除 " + names[0] + "?",
		message: head,
		hint: "移入最近删除, 可以从最近删除恢复.",
		confirmLabel: "移入最近删除",
		cancelLabel: "保留",
		variant: "danger",
	});
}

/** 浏览器页的全部写操作. 逐行状态进 pending (启停删按钮读它显示转圈与成功态), 结束刷新列表. */
export function useEnvActions(ctx: {
	byId: Map<string, Env>;
	picked: Set<string>;
	setPicked: Dispatch<SetStateAction<Set<string>>>;
	kernels: Kernel[];
	reload: () => void;
	pickNode: (id: string) => void;
}) {
	const [busy, setBusy] = useState(false);
	const [dialog, setDialog] = useState<DialogState>(null);
	const [fp, setFp] = useState<FpTarget>(null);
	const [pending, setPending] = useState<Record<string, RowPhase>>({});
	const timers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});

	const mark = (id: string, op: string, phase: RowPhase["phase"]): void => {
		clearTimeout(timers.current[id]);
		setPending((p) => ({ ...p, [id]: { op, phase } }));
		if (phase !== "busy") timers.current[id] = setTimeout(() => setPending(({ [id]: _, ...rest }) => rest), FLASH_MS);
	};

	const run = async (label: string, call: () => Promise<Result<unknown>>, row?: { id: string; op: string }): Promise<boolean> => {
		setBusy(true);
		if (row) mark(row.id, row.op, "busy");
		const r = await call();
		setBusy(false);
		if (row) mark(row.id, row.op, r.ok ? "done" : "fail");
		if (r.ok) toast.success(label + "完成");
		else toast.error(label + "失败: " + r.error);
		ctx.reload();
		return r.ok;
	};

	/** 批量: 一次 POST /environments/batch, 每行先转圈, 回包后按 id 标成败; 汇总进 toast (成功数, 失败数, 第一条原因). */
	const runMany = async (label: string, ids: string[], op: BatchOp, extra?: { folder?: string; tags?: string[] }): Promise<boolean> => {
		setBusy(true);
		for (const id of ids) mark(id, op, "busy");
		const r = await batchOp(op, ids, extra);
		setBusy(false);
		const out: BatchOutcome = r.ok ? r.data : { done: 0, failed: ids.map((id) => ({ id, error: r.error })) };
		const bad = new Set(out.failed.map((f) => f.id));
		for (const id of ids) mark(id, op, bad.has(id) ? "fail" : "done");
		report(label, out);
		ctx.setPicked(new Set());
		ctx.reload();
		return out.failed.length === 0;
	};

	const onAction = async (action: EnvAction, env: Env): Promise<void> => {
		const row = { id: env.id, op: action };
		if (action === "start") await run("启动", () => envApi.start(env.id), row);
		if (action === "stop") await run("停止", () => envApi.stop(env.id), row);
		if (action === "edit") setFp({ mode: "edit", env });
		if (action === "rename") setDialog({ kind: "rename-env", env });
		if (action === "move") setDialog({ kind: "move-env", env });
		if (action === "tags") setDialog({ kind: "set-tags", env });
		if (action === "kernel") setDialog({ kind: "set-kernel", env });
		if (action === "delete" && (await askDelete([env.name]))) await run("删除", () => envApi.remove(env.id), row);
	};

	const onBatch = async (action: BatchAction): Promise<void> => {
		try {
			await batchOf(action);
		} catch (e) {
			// 任何异常都落成提示, 不让 void 调用吞掉
			setBusy(false);
			toast.error("批量操作出错: " + String((e as Error)?.message ?? e));
		}
	};

	const batchOf = async (action: BatchAction): Promise<void> => {
		const ids = [...ctx.picked].filter((id) => ctx.byId.has(id));
		if (!ids.length) return void toast.error("没有选中可写的浏览器, 重新勾选后再试");
		if (action === "start") await runMany("批量启动", ids, "start");
		if (action === "stop") await runMany("批量停止", ids, "stop");
		if (action === "move") setDialog({ kind: "move-many", ids });
		if (action === "tags") setDialog({ kind: "add-tags", ids });
		if (action === "delete" && (await askDelete(ids.map((id) => ctx.byId.get(id)?.name ?? id)))) await runMany("批量删除", ids, "delete");
	};

	const onSubmit = async (state: NonNullable<DialogState>, v: DialogValues): Promise<void> => {
		const tagsAdded = parseTags(v.tags);
		let okAll = false;
		if (state.kind === "move-many") okAll = await runMany("批量移动", state.ids, "move", { folder: v.folder });
		else if (state.kind === "add-tags") okAll = await runMany("批量加标签", state.ids, "tags", { tags: tagsAdded });
		else {
			const calls: Record<string, () => Promise<Result<unknown>>> = {
				"set-kernel": () => envApi.setKernel(state.kind === "set-kernel" ? state.env.id : "", kernelPatch(ctx.kernels.find((k) => k.id === v.kernel)) ?? { kernel: "", browser: "sun" }),
				"new-folder": () => folderApi.create(v.name),
				"rename-folder": () => folderApi.rename(state.kind === "rename-folder" ? state.folder : "", v.name),
				"rename-env": () => envApi.rename(state.kind === "rename-env" ? state.env.id : "", v.name),
				"move-env": () => envApi.move(state.kind === "move-env" ? state.env.id : "", v.folder),
				"set-tags": () => envApi.setTags(state.kind === "set-tags" ? state.env.id : "", tagsAdded),
			};
			okAll = await run("保存", calls[state.kind]);
			if (okAll && state.kind === "rename-folder") ctx.pickNode("h/" + v.name);
		}
		if (okAll) setDialog(null);
	};

	const onFpSubmit = async (target: NonNullable<FpTarget>, body: Record<string, unknown>): Promise<void> => {
		const ok = target.mode === "create"
			? await run("新建", () => envApi.create(body))
			: await run("保存", () => envApi.update(target.env.id, body), { id: target.env.id, op: "edit" });
		if (ok) setFp(null);
	};

	const removeFolder = async (name: string): Promise<void> => {
		const yes = await confirmDialog({ title: "删除文件夹 " + name + "?", message: "只能删除空文件夹, 里面的浏览器要先移走.", confirmLabel: "删除文件夹", variant: "danger" });
		if (yes && (await run("删除文件夹", () => folderApi.remove(name)))) ctx.pickNode("all");
	};

	return { busy, dialog, setDialog, fp, setFp, pending, onAction, onBatch, onSubmit, onFpSubmit, removeFolder };
}
