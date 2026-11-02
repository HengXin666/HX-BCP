import type { JSX } from "react";
import { Badge, StatCard, StatGrid } from "@hx/ui";
import { fingerprintVerdict } from "../../lib/api";
import { usePoll } from "../../lib/hooks";
import { GlobalChain } from "./GlobalChain";
import { KernelCard } from "./KernelCard";
import { StatusCharts } from "./StatusCharts";
import { Block } from "../Block";

const TONE: Record<string, "success" | "warning" | "destructive" | "secondary"> = {
	green: "success",
	yellow: "warning",
	red: "destructive",
	unknown: "secondary",
};

const LABEL: Record<string, string> = { green: "通过", yellow: "待复核", red: "阻断", unknown: "未运行" };

/** 状态页: 全局链路 (浏览器 -> 缓存 -> 出口 -> 目标), 内核安装情况, 指纹自检结论. */
export function StatusTab(): JSX.Element {
	const verdict = usePoll(fingerprintVerdict);
	const fp = verdict.result?.ok ? verdict.result.data : { verdict: "unknown" as const, checked: 0, at: "", evidence: [] };

	return (
		<div className="flex flex-col gap-6">
			<Block title="链路" desc="浏览器服务 - 缓存代理 - 上游出口 - 目标站点, 颜色按下方图例">
				<GlobalChain />
			</Block>
			<Block title="趋势" desc="本页打开以来的前端采样">
				<StatusCharts />
			</Block>
			<Block title="内核与指纹">
				<KernelCard />
				<StatGrid columns={2}>
				<StatCard
					label="指纹自检"
					value={<Badge variant={TONE[fp.verdict] ?? "secondary"}>{LABEL[fp.verdict] ?? fp.verdict}</Badge>}
					hint={"检查项 " + fp.checked + ", 时间 " + (fp.at || "-")}
				/>
				<StatCard
					label="复算命令"
					value={<span className="font-mono t-aux">uv run audit.py --launch headless --json</span>}
					hint="在 packages/fingerprint-audit 下执行, 退出码 0 绿 1 黄 2 红"
				/>
				</StatGrid>
			</Block>
		</div>
	);
}
