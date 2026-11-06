import { useEffect, useState, type JSX } from "react";
import {
	Button,
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
	InputField,
	SelectField,
} from "@hx/ui";
import type { Env } from "../../lib/api";
import { envFamily, type Kernel } from "../../lib/kernels";
import { KernelPicker } from "./KernelBits";

/**
 * 小对话框种类 (新建与编辑浏览器的四节对话框在 FpDialog.tsx): 新建或改名文件夹, 浏览器改名, 移动到文件夹 (单个或批量), 标签 (单个替换或批量追加).
 * 切换内核 (只能同族). 都只作用于人类组.
 */
export type DialogState =
	| { kind: "new-folder" }
	| { kind: "rename-folder"; folder: string }
	| { kind: "rename-env"; env: Env }
	| { kind: "move-env"; env: Env }
	| { kind: "move-many"; ids: string[] }
	| { kind: "set-tags"; env: Env }
	| { kind: "add-tags"; ids: string[] }
	| { kind: "set-kernel"; env: Env }
	| null;

export type DialogValues = { name: string; folder: string; tags: string; kernel: string };

/** 逗号或空格分隔的标签文本转数组, 去重去空. */
export function parseTags(text: string): string[] {
	return [...new Set(text.split(/[,\s]+/).map((t) => t.trim()).filter(Boolean))];
}

const ROOT = "__root__";

const TITLE: Record<string, string> = {
	"new-folder": "新建文件夹",
	"rename-folder": "文件夹改名",
	"rename-env": "浏览器改名",
	"move-env": "移动到文件夹",
	"move-many": "批量移动到文件夹",
	"set-tags": "编辑标签",
	"add-tags": "批量追加标签",
	"set-kernel": "切换内核",
};

const BLANK: DialogValues = { name: "", folder: "", tags: "", kernel: "" };

/** 打开对话框时的初始值. */
function initial(state: DialogState): DialogValues {
	if (!state) return BLANK;
	if (state.kind === "set-kernel") return { ...BLANK, kernel: state.env.fingerprint?.kernel ?? "" };
	if (state.kind === "rename-folder") return { ...BLANK, name: state.folder };
	if (state.kind === "rename-env") return { ...BLANK, name: state.env.name };
	if (state.kind === "move-env") return { ...BLANK, folder: state.env.folder };
	if (state.kind === "set-tags") return { ...BLANK, tags: (state.env.tags ?? []).join(", ") };
	return BLANK;
}

/** 一行带标签的表单项. */
function Row({ label, children }: { label: string; children: JSX.Element }): JSX.Element {
	return (
		<label className="flex flex-col gap-1 t-aux text-muted-foreground">
			{label}
			{children}
		</label>
	);
}

/** 人类组的表单对话框, 提交时把填写值交给调用方, 调用方负责请求与报错. */
export function EnvDialog(props: {
	state: DialogState;
	folders: string[];
	kernels: Kernel[];
	busy: boolean;
	onClose: () => void;
	onSubmit: (state: NonNullable<DialogState>, values: DialogValues) => void;
}): JSX.Element {
	const { state } = props;
	const [values, setValues] = useState<DialogValues>(initial(state));
	// 只在打开或换对话框时重置; 内核列表 5 秒轮询一次, 不能因此冲掉已选项
	// eslint-disable-next-line react-hooks/exhaustive-deps
	useEffect(() => setValues(initial(state)), [state]);

	const set = (patch: Partial<DialogValues>): void => setValues((v) => ({ ...v, ...patch }));
	const folderOptions = [{ value: ROOT, label: "根目录" }, ...props.folders.map((f) => ({ value: f, label: f }))];
	const kind = state?.kind ?? "";
	const needsName = ["new-folder", "rename-folder", "rename-env"].includes(kind);
	const needsFolder = ["move-env", "move-many"].includes(kind);
	const needsTags = kind === "set-tags" || kind === "add-tags";
	const needsKernel = kind === "set-kernel";
	const lockFamily = state?.kind === "set-kernel" ? envFamily(state.env, props.kernels) : undefined;
	const canSubmit =
		!props.busy &&
		(!needsName || values.name.trim() !== "") &&
		(kind !== "add-tags" || parseTags(values.tags).length > 0) &&
		(!needsKernel || props.kernels.some((k) => k.id === values.kernel && k.available));

	return (
		<Dialog open={state !== null} onOpenChange={(open) => (open ? undefined : props.onClose())}>
			<DialogContent showCloseButton={false}>
				<DialogHeader>
					<DialogTitle>{state ? TITLE[state.kind] : ""}</DialogTitle>
					<DialogDescription>只作用于人类组. API 组由 api 或 mcp 管理, 这里只读.</DialogDescription>
				</DialogHeader>
				<div className="flex max-h-[65dvh] flex-col gap-3 overflow-y-auto pr-1">
					{needsName ? (
						<Row label="名称">
							<InputField value={values.name} onChange={(name) => set({ name })} autoFocus aria-label="名称" />
						</Row>
					) : null}
					{needsFolder ? (
						<Row label="文件夹">
							<SelectField
								value={values.folder || ROOT}
								onChange={(f) => set({ folder: f === ROOT ? "" : f })}
								options={folderOptions}
							/>
						</Row>
					) : null}
					{needsTags ? (
						<Row label={kind === "add-tags" ? "追加的标签 (逗号或空格分隔, 已有标签保留)" : "标签 (逗号或空格分隔, 留空即清空)"}>
							<InputField value={values.tags} onChange={(tags) => set({ tags })} autoFocus aria-label="标签" />
						</Row>
					) : null}
					{needsKernel ? (
						<Row label={lockFamily ? "内核 (同族内切换, 下次启动生效)" : "内核 (族决定配置方式, 内核决定用哪个可执行文件)"}>
							<KernelPicker kernels={props.kernels} value={values.kernel} onChange={(kernel) => set({ kernel })} lockFamily={lockFamily} />
						</Row>
					) : null}
				</div>
				<DialogFooter>
					<Button variant="ghost" onClick={props.onClose}>取消</Button>
					<Button
						variant="primary"
						disabled={!canSubmit}
						onClick={() => (state ? props.onSubmit(state, { ...values, name: values.name.trim() }) : undefined)}
					>
						确定
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
