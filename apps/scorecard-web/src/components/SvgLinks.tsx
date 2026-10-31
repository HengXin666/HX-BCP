import { Button, Card, CardContent, CardHeader, CardTitle, Input } from "@hx/ui";
import { useState } from "react";
import { svgUrls } from "../lib/data.ts";
import type { CaseRecord } from "../lib/record.ts";

/** 一张 svg: 点图在新标签打开, 下方给可复制的 raw 链接 */
function SvgItem({ record, title, path }: { record: CaseRecord; title: string; path: string }) {
	const [copied, setCopied] = useState(false);
	const urls = svgUrls(record, path);
	const copy = () => {
		void navigator.clipboard.writeText(urls.raw).then(() => setCopied(true));
	};
	return (
		<Card>
			<CardHeader>
				<CardTitle>{title}</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-2">
				<a href={urls.view} target="_blank" rel="noreferrer">
					<img src={urls.view} alt={title} className="w-full" loading="lazy" />
				</a>
				<div className="flex gap-2">
					<Input readOnly value={urls.raw} onFocus={(event) => event.currentTarget.select()} />
					<Button type="button" variant="outline" onClick={copy}>
						{copied ? "已复制" : "复制"}
					</Button>
				</div>
			</CardContent>
		</Card>
	);
}

/** 总览图与每个内核的图, 没有发布的图写明缺失 */
export function SvgLinks({ record }: { record: CaseRecord }) {
	const kernels = record.kernels.filter((kernel) => record.svg.kernels[kernel.kernel]);
	const missing = record.kernels.filter((kernel) => !record.svg.kernels[kernel.kernel]).map((kernel) => kernel.label);
	return (
		<div className="flex flex-col gap-4">
			{record.svg.overview ? <SvgItem record={record} title="全部内核总览" path={record.svg.overview} /> : <div>这次没有发布总览图</div>}
			{kernels.map((kernel) => (
				<SvgItem key={kernel.kernel} record={record} title={`内核 ${kernel.label}`} path={record.svg.kernels[kernel.kernel]} />
			))}
			{missing.length ? <div className="text-muted-foreground text-sm">没有图的内核 {missing.join(", ")}</div> : null}
		</div>
	);
}
