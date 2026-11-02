import { useEffect, useMemo, useState, type JSX } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Spinner, Tabs, TabsContent, TabsList, TabsTrigger } from "@hx/ui";
import type { Env } from "../../lib/api";
import { BLANK_FORM, bodyOf, formOf, problems, type FpForm } from "../../lib/fp-form";
import { defaultKernel, type Kernel } from "../../lib/kernels";
import { AdvancedSection, BasicSection, DeviceSection, FingerprintSection } from "./FpSections";

/** 新建 (env 为空, 带初始文件夹) 或编辑一个人类组浏览器. */
export type FpTarget = { mode: "create"; folder: string } | { mode: "edit"; env: Env } | null;

const STEPS = [
	{ id: "basic", label: "基础" },
	{ id: "device", label: "内核与设备" },
	{ id: "fp", label: "指纹" },
	{ id: "adv", label: "高级" },
] as const;

/**
 * 新建与编辑共用的四节对话框 (Tabs): 基础, 内核与设备, 指纹, 高级. 每节都能直接点确定, 不填的字段交给后端默认.
 * 四节都挂在 DOM 里 (forceMount, 非当前节隐藏), 切节不丢输入. 底部: 取消, 上一步, 下一步, 确定; 没有右上角 Close.
 * 提交交给调用方 (onSubmit 收到请求体), 调用方负责请求, 报错与关闭.
 */
export function FpDialog(props: {
	target: FpTarget;
	folders: string[];
	kernels: Kernel[];
	busy: boolean;
	onClose: () => void;
	onSubmit: (target: NonNullable<FpTarget>, body: Record<string, unknown>) => void;
}): JSX.Element {
	const { target } = props;
	const before = useMemo(() => (target?.mode === "edit" ? formOf(target.env) : null), [target]);
	const fresh = (): FpForm => before ?? { ...BLANK_FORM, folder: target?.mode === "create" ? target.folder : "", kernel: defaultKernel(props.kernels) };
	const [form, setForm] = useState<FpForm>(fresh);
	const [step, setStep] = useState(0);
	// 只在打开或换目标时重置; 内核列表会轮询, 不能因此冲掉输入
	// eslint-disable-next-line react-hooks/exhaustive-deps
	useEffect(() => (setForm(fresh()), setStep(0)), [target]);

	const set = (patch: Partial<FpForm>): void => setForm((f) => ({ ...f, ...patch }));
	const issues = problems(form);
	const kernelOk = !form.kernel || props.kernels.some((k) => k.id === form.kernel && k.available);
	const body = bodyOf(form, before, props.kernels);
	const empty = target?.mode === "edit" && Object.keys(body).length === 0;
	const canSubmit = !props.busy && issues.length === 0 && kernelOk && !empty;
	const lockFamily = target?.mode === "edit" ? (target.env.fingerprint?.browser === "flower" ? "gecko" : "blink") : undefined;
	const title = target?.mode === "edit" ? "编辑 " + target.env.name : "新建浏览器";

	return (
		<Dialog open={target !== null} onOpenChange={(open) => (open ? undefined : props.onClose())}>
			<DialogContent showCloseButton={false} className="sm:max-w-2xl" data-fp-dialog="">
				<DialogHeader>
					<DialogTitle>{title}</DialogTitle>
					<DialogDescription>标了 留空=自动 的字段可以不填, 后端按内核与出口 IP 补齐. 只作用于人类组.</DialogDescription>
				</DialogHeader>
				<Tabs value={STEPS[step].id} onValueChange={(v) => setStep(STEPS.findIndex((s) => s.id === v))}>
					<TabsList>
						{STEPS.map((s, i) => (
							<TabsTrigger key={s.id} value={s.id}>{i + 1 + " " + s.label}</TabsTrigger>
						))}
					</TabsList>
					<div className="mt-3 max-h-[58dvh] overflow-y-auto pr-1">
						<TabsContent forceMount value="basic" className="data-[state=inactive]:hidden data-[state=active]:animate-in data-[state=active]:fade-in-0 duration-150">
							<BasicSection form={form} set={set} folders={props.folders} />
						</TabsContent>
						<TabsContent forceMount value="device" className="data-[state=inactive]:hidden data-[state=active]:animate-in data-[state=active]:fade-in-0 duration-150">
							<DeviceSection form={form} set={set} kernels={props.kernels} lockFamily={lockFamily} />
						</TabsContent>
						<TabsContent forceMount value="fp" className="data-[state=inactive]:hidden data-[state=active]:animate-in data-[state=active]:fade-in-0 duration-150">
							<FingerprintSection form={form} set={set} />
						</TabsContent>
						<TabsContent forceMount value="adv" className="data-[state=inactive]:hidden data-[state=active]:animate-in data-[state=active]:fade-in-0 duration-150">
							<AdvancedSection form={form} set={set} />
						</TabsContent>
					</div>
				</Tabs>
				{issues.length ? <p className="t-aux text-destructive" role="alert">{issues.join("; ")}</p> : null}
				<DialogFooter className="flex-row items-center sm:justify-between">
					<Button variant="ghost" onClick={props.onClose}>取消</Button>
					<span className="flex gap-2">
						<Button variant="outline" disabled={step === 0} onClick={() => setStep((s) => s - 1)}><ChevronLeft className="size-4" />上一步</Button>
						<Button variant="outline" disabled={step === STEPS.length - 1} onClick={() => setStep((s) => s + 1)}>下一步<ChevronRight className="size-4" /></Button>
						<Button variant="primary" disabled={!canSubmit} onClick={() => (target ? props.onSubmit(target, body) : undefined)} data-fp-submit="">
							{props.busy ? <Spinner className="size-4" /> : null}
							{target?.mode === "edit" ? "保存" : "确定"}
						</Button>
					</span>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
