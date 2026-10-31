import { Badge, Card, CardContent, CardHeader, CardTitle, Progress, StatCard, StatGrid } from "@hx/ui";
import { casePage } from "../lib/data.ts";
import { hasScore, unmeasuredCount } from "../lib/record.ts";
import { useCase } from "../shell/CaseContext.tsx";
import { bootPage } from "../shell/mount.tsx";
import { PageNav, pageHref } from "../shell/PageNav.tsx";

/** 总览: 一次测评的结论只占一屏, 想看细节点进各自的页面 */
function Overview() {
	const load = useCase();
	const { current, diff } = load;
	const scored = current.kernels.filter(hasScore);
	const blocked = scored.filter((item) => item.verdict === "block");
	const best = [...scored].sort((left, right) => (right.score ?? 0) - (left.score ?? 0))[0];
	const worst = [...scored].sort((left, right) => (left.score ?? 0) - (right.score ?? 0))[0];
	return (
		<main className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-baseline justify-between gap-2">
					<h1 className="text-2xl font-semibold">PR #{current.pr} 第 {current.no} 次测评</h1>
					<span className="text-muted-foreground text-sm">{current.recordedAt.replace("T", " ").slice(0, 16)}</span>
				</div>
				<p className="text-muted-foreground text-sm">
					{current.kernels.length} 个内核, {scored.length} 个有分. 未测的不是 0 分, 原因写在各内核自己的页面上.
				</p>
			</header>
			<PageNav current="overview" load={load} />
			<StatGrid columns={4}>
				<StatCard label="内核数" value={current.kernels.length} />
				<StatCard label="有分" value={scored.length} tone={scored.length ? "good" : "muted"} />
				<StatCard label="未测" value={current.kernels.length - scored.length} tone="muted" />
				<StatCard label="阻断" value={blocked.length} tone={blocked.length ? "bad" : "muted"} />
			</StatGrid>
			<div className="grid gap-4 sm:grid-cols-2">
				<Card>
					<CardHeader>
						<CardTitle className="text-sm">分数分布</CardTitle>
					</CardHeader>
					<CardContent className="flex flex-col gap-2 text-sm">
						{best ? (
							<div className="flex items-center justify-between gap-3">
								<span className="text-muted-foreground">最好</span>
								<span className="flex items-center gap-2 font-medium">
									{best.label}
									<span className="font-mono">{(best.score ?? 0).toFixed(1)}</span>
								</span>
							</div>
						) : null}
						{worst && worst.kernel !== best?.kernel ? (
							<div className="flex items-center justify-between gap-3">
								<span className="text-muted-foreground">最差</span>
								<span className="flex items-center gap-2 font-medium">
									{worst.label}
									<span className="font-mono">{(worst.score ?? 0).toFixed(1)}</span>
								</span>
							</div>
						) : null}
						<div className="flex items-center justify-between gap-3">
							<span className="text-muted-foreground">与上次</span>
							<span className="flex gap-2">
								<Badge variant={diff.better ? "success" : "secondary"}>{diff.better} 变好</Badge>
								<Badge variant={diff.worse ? "destructive" : "secondary"}>{diff.worse} 变差</Badge>
							</span>
						</div>
					</CardContent>
				</Card>
				<Card>
					<CardHeader>
						<CardTitle className="text-sm">各内核分数</CardTitle>
					</CardHeader>
					<CardContent className="flex flex-col gap-2">
						{current.kernels.map((item) => (
							<a key={item.kernel} href={`${pageHref("kernel", load.selected)}?kernel=${item.kernel}`} className="group flex items-center gap-3">
								<span className="w-20 shrink-0 truncate text-xs group-hover:underline">{item.label}</span>
								<Progress value={item.score ?? 0} tone={item.reason ? "primary" : "success"} className="flex-1" />
								<span className="w-12 shrink-0 text-right font-mono text-xs">{item.reason ? "未测" : (item.score ?? 0).toFixed(1)}</span>
							</a>
						))}
					</CardContent>
				</Card>
			</div>
			<footer className="text-muted-foreground flex flex-wrap gap-4 text-xs">
				<a className="hover:underline" href={pageHref("now", load.selected)}>看本次各内核</a>
				<a className="hover:underline" href={pageHref("compare", load.selected)}>看与上次对比</a>
				<a className="hover:underline" href={pageHref("artifacts", load.selected)}>打开图与产物</a>
				<a className="hover:underline" href={casePage(1)}>回到入口</a>
				<span>未测站合计 {current.kernels.reduce((sum, item) => sum + unmeasuredCount(item), 0)}</span>
			</footer>
		</main>
	);
}

bootPage(Overview);
