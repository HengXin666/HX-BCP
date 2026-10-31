import { Card, CardContent, CardHeader, CardTitle, EmptyState } from "@hx/ui";
import { useMemo, useState } from "react";
import { prDir, svgUrls } from "../lib/data.ts";
import { useCase } from "../shell/CaseContext.tsx";
import { bootPage } from "../shell/mount.tsx";
import { PageNav } from "../shell/PageNav.tsx";

/** 复制按钮要给出反馈, 否则用户不知道点没点上 */
function CopyButton({ text }: { text: string }) {
	const [done, setDone] = useState(false);
	return (
		<button
			type="button"
			onClick={async () => {
				try {
					await navigator.clipboard.writeText(text);
					setDone(true);
					window.setTimeout(() => setDone(false), 1500);
				} catch {
					setDone(false);
				}
			}}
			className="hover:bg-muted shrink-0 cursor-pointer rounded-md border px-2 py-1 text-xs transition-colors duration-150"
		>
			{done ? "已复制" : "复制"}
		</button>
	);
}

/** 图与产物: 总览图单独占满宽度, 单内核图按网格排列, 每个都给可复制的 raw 地址 */
function ArtifactsPage() {
	const load = useCase();
	const { current } = load;
	const overview = useMemo(() => (current.svg.overview ? svgUrls(current, current.svg.overview) : null), [current]);
	const kernels = useMemo(
		() => Object.entries(current.svg.kernels).map(([kernel, path]) => ({ kernel, ...svgUrls(current, path) })),
		[current],
	);
	const dataUrl = new URL(`${current.no}.json`, prDir(current.pr)).href;
	return (
		<main className="mx-auto flex max-w-6xl flex-col gap-5 p-6">
			<h1 className="text-2xl font-semibold">图与产物</h1>
			<PageNav current="artifacts" load={load} />
			{!overview && kernels.length === 0 ? (
				<EmptyState text="这次没有图" hint="采集没跑到出图那一步, 或者产物没发布成功" />
			) : null}
			{overview ? (
				<Card>
					<CardHeader>
						<CardTitle className="flex items-center justify-between gap-3 text-sm">
							<span>全部内核总览</span>
							<CopyButton text={overview.raw} />
						</CardTitle>
					</CardHeader>
					<CardContent className="flex flex-col gap-3">
						<img src={overview.view} alt="全部内核总览" className="w-full rounded-lg border" />
						<a className="text-muted-foreground text-xs hover:underline" href={overview.raw}>
							{overview.raw}
						</a>
					</CardContent>
				</Card>
			) : null}
			{kernels.length ? (
				<div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
					{kernels.map((item) => (
						<Card key={item.kernel}>
							<CardHeader>
								<CardTitle className="flex items-center justify-between gap-2 text-sm">
									<span className="truncate">内核 {item.kernel}</span>
									<CopyButton text={item.raw} />
								</CardTitle>
							</CardHeader>
							<CardContent className="flex flex-col gap-2">
								<img src={item.view} alt={`内核 ${item.kernel}`} className="w-full rounded-lg border" />
								<a className="text-muted-foreground truncate text-xs hover:underline" href={item.raw}>
									{item.raw}
								</a>
							</CardContent>
						</Card>
					))}
				</div>
			) : null}
			<Card>
				<CardHeader>
					<CardTitle className="text-sm">结构化数据</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-2 text-xs">
					<a className="hover:underline" href={dataUrl}>
						{dataUrl}
					</a>
					<span className="text-muted-foreground">这次测评的原始记录, 页面显示的所有数字都来自它</span>
				</CardContent>
			</Card>
		</main>
	);
}

bootPage(ArtifactsPage);
