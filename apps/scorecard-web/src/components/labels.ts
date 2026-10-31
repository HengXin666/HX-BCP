import type { BadgeProps, StateTone, StatTone } from "@hx/ui";
import type { Trend } from "../lib/diff.ts";
import type { CellVerdict, KernelRecord } from "../lib/record.ts";

type BadgeVariant = NonNullable<BadgeProps["variant"]>;

/** 站结论的中文名与徽章样式 */
export const CELL: Record<CellVerdict, { text: string; variant: BadgeVariant }> = {
	pass: { text: "过", variant: "success" },
	fail: { text: "不过", variant: "destructive" },
	unmeasured: { text: "未测", variant: "secondary" },
	record: { text: "记录", variant: "outline" },
};

/** 升降方向的中文名, 徽章样式与数字卡色调 */
export const TREND: Record<Trend, { text: string; variant: BadgeVariant; tone: StatTone }> = {
	better: { text: "变好", variant: "success", tone: "good" },
	worse: { text: "变差", variant: "destructive", tone: "bad" },
	same: { text: "没变", variant: "secondary", tone: "muted" },
	first: { text: "首次", variant: "outline", tone: "muted" },
	new: { text: "新内核", variant: "outline", tone: "accent" },
	gone: { text: "这次没有", variant: "warning", tone: "bad" },
};

/** 内核整体状态点, 未测优先于结论 */
export function kernelDot(kernel: KernelRecord): { tone: StateTone; label: string } {
	if (kernel.reason) return { tone: "idle", label: "未测" };
	if (kernel.verdict === "pass") return { tone: "ok", label: "通过" };
	if (kernel.verdict === "block") return { tone: "error", label: "阻断" };
	return { tone: "warn", label: "未完成" };
}

/** 分数显示, null 显示未测 */
export function scoreText(score: number | null): string {
	return score === null ? "未测" : score.toFixed(1);
}

/** 分差显示, 正数带加号, null 显示无可比 */
export function deltaText(delta: number | null): string {
	if (delta === null) return "无可比";
	return delta > 0 ? `+${delta.toFixed(1)}` : delta.toFixed(1);
}
