import type { JSX } from "react";
import { Skeleton } from "@hx/ui";

/** 首次加载的骨架: 左侧分组栏 (宽屏), 页头, 工具条, 12 行表格. 形状与真实布局一致, 数据到了原位替换, 不跳版. */
export function PageSkeleton(): JSX.Element {
	return (
		<div className="flex min-w-0 items-start gap-3" data-skeleton="" aria-busy="true" aria-label="读取中">
			<div className="hidden w-[240px] shrink-0 flex-col gap-2 rounded-xl border border-border p-3 lg:flex">
				<Skeleton className="h-8 w-full" />
				{Array.from({ length: 9 }, (_, i) => <Skeleton key={i} className={"h-6 " + (i % 3 === 0 ? "w-2/3" : "w-full")} />)}
			</div>
			<div className="flex min-w-0 flex-1 flex-col gap-3">
				<Skeleton className="h-7 w-48" />
				<Skeleton className="h-4 w-72" />
				<div className="flex gap-2">
					<Skeleton className="h-9 flex-1" />
					{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-9 w-36" />)}
				</div>
				<div className="flex flex-col gap-0 rounded-xl border border-border">
					{Array.from({ length: 12 }, (_, i) => (
						<div key={i} className="flex h-11 items-center gap-3 border-b border-border px-3 last:border-b-0">
							<Skeleton className="size-4" />
							<Skeleton className="h-4 flex-1" />
							<Skeleton className="h-4 w-14" />
							<Skeleton className="h-5 w-16" />
							<Skeleton className="h-4 w-24" />
							<Skeleton className="h-4 w-20" />
						</div>
					))}
				</div>
			</div>
		</div>
	);
}
