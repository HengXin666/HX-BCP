import { useContext, type JSX } from "react";
import { Badge, RadioGroup, RadioGroupItem } from "@hx/ui";
import type { Env } from "../../lib/api";
import { FAMILIES, FAMILY, KernelContext, type Family, type Kernel } from "../../lib/kernels";

/**
 * 内核标识: 族色圆点 + 短名, 族色的描边底, 列宽 <= 80px. 继承设置的旧环境用虚线框 (悬停写明来源), 分得清 "指定" 与 "没指定".
 * 悬停 title 给全名, 族, 配置方式, 来源, 路径.
 */
export function KernelChip({ env }: { env: Env }): JSX.Element {
	const v = useContext(KernelContext)(env);
	const f = FAMILY[v.family];
	return (
		<span
			className={"inline-flex max-w-[4.5rem] items-center gap-1 rounded border px-1 t-micro normal-case tracking-normal " + f.chip + (v.how === "继承" ? " border-dashed" : "")}
			title={v.title}
			data-kernel={v.id || "unknown"}
			data-family={v.family}
		>
			<span className={"size-2 shrink-0 rounded-full " + f.dot} />
			<span className="truncate font-medium">{v.short}</span>
		</span>
	);
}

/** 一个内核选项: 单选圆点, 名称, 路径 (单行截断) 或 "未安装". 不可选时整行置灰并写原因. */
function KernelOption(props: { k: Kernel; blocked: string }): JSX.Element {
	const { k, blocked } = props;
	const off = !k.available || blocked !== "";
	const id = "kernel-" + k.id;
	return (
		<label
			htmlFor={id}
			className={"flex w-full min-w-0 items-center gap-2 rounded-md border border-border/60 px-3 py-2 " + (off ? "cursor-not-allowed opacity-50" : "cursor-pointer hover:bg-accent/40")}
			data-kernel-option={k.id}
			data-available={String(k.available)}
			data-disabled={String(off)}
		>
			<RadioGroupItem id={id} value={k.id} disabled={off} />
			<span className="flex min-w-0 flex-1 flex-col">
				<span className="t-body leading-5 text-foreground">{k.label}</span>
				<span className="truncate font-mono t-aux text-muted-foreground" title={k.path}>{k.path || "-"}</span>
			</span>
			{!k.available ? <Badge variant="secondary">未安装</Badge> : blocked ? <Badge variant="outline">{blocked}</Badge> : null}
		</label>
	);
}

/**
 * 内核选择: 按族分两组 (Chromium 系 / Firefox 系), 选项全部来自 GET /api/v1/kernels.
 * lockFamily: 编辑已有环境时只能在同族内换 (换族等于换配置格式, 老的 Preferences 或 user.js 用不上), 另一族置灰.
 */
export function KernelPicker(props: { kernels: Kernel[]; value: string; onChange: (id: string) => void; lockFamily?: Family }): JSX.Element {
	if (!props.kernels.length) return <span className="t-aux text-muted-foreground">内核列表未读到 (GET /api/v1/kernels)</span>;
	return (
		<RadioGroup value={props.value} onValueChange={props.onChange} className="grid-cols-[minmax(0,1fr)] gap-3" aria-label="浏览器内核" data-kernel-picker="">
			{FAMILIES.map((fam) => {
				const list = props.kernels.filter((k) => k.family === fam);
				if (!list.length) return null;
				const blocked = props.lockFamily && props.lockFamily !== fam ? "换族需新建" : "";
				return (
					<div key={fam} className="flex min-w-0 flex-col gap-2" role="group" aria-label={FAMILY[fam].name}>
						<span className="flex items-center gap-2 t-aux text-muted-foreground">
							<span className={"size-2 rounded-full " + FAMILY[fam].dot} />
							{FAMILY[fam].name}
							<span className="opacity-70">{FAMILY[fam].how}</span>
						</span>
						{list.map((k) => <KernelOption key={k.id} k={k} blocked={blocked} />)}
					</div>
				);
			})}
		</RadioGroup>
	);
}
