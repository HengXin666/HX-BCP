import type { JSX } from "react";
import { Badge, Card, CardContent } from "@hx/ui";
import { usePoll } from "../../lib/hooks";
import { FAMILIES, FAMILY, listKernels, shortName } from "../../lib/kernels";

/** 状态页的内核卡: 已装几个 / 共几个, 缺的列名字; 下面按族列出每个内核与路径. 数据只来自 GET /api/v1/kernels. */
export function KernelCard(): JSX.Element {
	const poll = usePoll(listKernels, 30000);
	const r = poll.result;
	if (!r) return <Card variant="solid"><CardContent className="p-4 t-aux text-muted-foreground">读取内核列表</CardContent></Card>;
	if (!r.ok) return <Card variant="solid"><CardContent className="p-4 t-aux text-muted-foreground">{"内核列表未读到: " + r.error}</CardContent></Card>;
	const all = r.data;
	const ok = all.filter((k) => k.available);
	const missing = all.filter((k) => !k.available);
	return (
		<Card variant="solid" data-kernel-card="">
			<CardContent className="flex flex-col gap-3 p-4">
				<div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
					<span className="t-card">{"已装 " + ok.length + " 个内核 / 共 " + all.length + " 个"}</span>
					<span className="t-aux text-muted-foreground">{missing.length ? "缺: " + missing.map((k) => shortName(k.label)).join(", ") : "全部已装"}</span>
				</div>
				<div className="grid gap-3 md:grid-cols-2">
					{FAMILIES.map((fam) => (
						<div key={fam} className="flex min-w-0 flex-col gap-1">
							<span className="flex items-center gap-2 t-aux text-muted-foreground">
								<span className={"size-2 rounded-full " + FAMILY[fam].dot} />
								{FAMILY[fam].name + ", " + FAMILY[fam].how}
							</span>
							{all.filter((k) => k.family === fam).map((k) => (
								<div key={k.id} className={"flex min-w-0 items-center gap-2 t-aux " + (k.available ? "" : "opacity-60")}>
									<span className="w-24 shrink-0 truncate">{k.label}</span>
									<span className="min-w-0 flex-1 truncate font-mono t-aux text-muted-foreground" title={k.path}>{k.path || "-"}</span>
									{k.available ? <Badge variant="success">已装</Badge> : <Badge variant="secondary">未安装</Badge>}
								</div>
							))}
						</div>
					))}
				</div>
			</CardContent>
		</Card>
	);
}
