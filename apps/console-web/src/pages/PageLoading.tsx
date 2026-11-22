/**
 * 页面首屏骨架 (浏览器页之外的各页共用): 页标题, 说明, 再按版式给表格行或卡片格. 形状贴近真实布局, 数据到了原位替换, 不跳版.
 * 浏览器页有自己的分组栏版式, 用 browsers/PageSkeleton.
 */
import type { JSX } from "react";
import { Skeleton } from "@hx/ui";

export function PageLoading(props: { layout?: "table" | "cards"; rows?: number }): JSX.Element {
	const n = props.rows ?? (props.layout === "cards" ? 6 : 8);
	return (
		<div className="flex min-w-0 flex-col gap-4" data-skeleton="" aria-busy="true" aria-label="读取中">
			<div className="flex flex-col gap-2">
				<Skeleton className="h-7 w-40" />
				<Skeleton className="h-4 w-72" />
			</div>
			{props.layout === "cards" ? (
				<div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
					{Array.from({ length: n }, (_, i) => <Skeleton key={i} className="h-40 w-full rounded-xl" />)}
				</div>
			) : (
				<div className="flex flex-col rounded-xl border border-border">
					{Array.from({ length: n }, (_, i) => (
						<div key={i} className="flex h-10 items-center gap-3 border-b border-border px-3 last:border-b-0">
							<Skeleton className="h-4 flex-1" />
							<Skeleton className="h-4 w-20" />
							<Skeleton className="h-4 w-24" />
						</div>
					))}
				</div>
			)}
		</div>
	);
}
