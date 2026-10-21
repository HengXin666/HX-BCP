import type { JSX } from "react";
import { Badge, Card, CardContent, EmptyState } from "@hx/ui";
import { fingerprintVerdict } from "../lib/api";
import { usePoll } from "../lib/hooks";

const TONE: Record<string, "success" | "warning" | "destructive" | "secondary"> = {
	green: "success",
	yellow: "warning",
	red: "destructive",
	unknown: "secondary",
};

const LABEL: Record<string, string> = { green: "通过", yellow: "待复核", red: "阻断", unknown: "未运行" };

/** 指纹评分: 只展示自检结论, 复算命令写在页面上. */
export function FingerprintPage(): JSX.Element {
	const { result, loading } = usePoll(fingerprintVerdict);
	if (loading && !result) return <EmptyState text="读取中" hint="正在读取自检结论" />;

	const data = result?.ok ? result.data : { verdict: "unknown" as const, checked: 0, at: "", evidence: [] };
	return (
		<Card variant="muted">
			<CardContent className="flex flex-col gap-3 pt-3">
				<div className="flex items-center gap-3">
					<Badge variant={TONE[data.verdict] ?? "secondary"}>{LABEL[data.verdict] ?? data.verdict}</Badge>
					<span className="text-[12px] text-muted-foreground">检查项 {data.checked}</span>
					<span className="text-[12px] text-muted-foreground">时间 {data.at || "-"}</span>
				</div>
				<pre className="overflow-x-auto rounded-md bg-background/60 p-2 font-mono text-[12px]">cd packages/fingerprint-audit && uv run audit.py --launch headless --json</pre>
				<div className="text-[12px] text-muted-foreground">退出码 0 绿 / 1 黄 / 2 红, 流水线直接用它当闸门.</div>
			</CardContent>
		</Card>
	);
}
