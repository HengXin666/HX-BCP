import type { JSX } from "react";
import { Search } from "lucide-react";
import { InputGroup, InputGroupAddon, InputGroupInput, SelectField } from "@hx/ui";
import { PAGE_SIZES, type EnvQuery, type ExitFilter, type SortKey, type StatusFilter } from "../../lib/env-query";

const ALL_TAGS = "__all__";
const ALL_KERNELS = "__all__";

const STATUS_OPTIONS: { value: StatusFilter; label: string }[] = [
	{ value: "all", label: "全部状态" },
	{ value: "running", label: "运行中" },
	{ value: "stopped", label: "已停止" },
	{ value: "never", label: "未启动" },
	{ value: "busy", label: "API 占用中" },
	{ value: "restart", label: "待重启" },
];

const SORT_OPTIONS: { value: string; label: string }[] = [
	{ value: "last:desc", label: "最近打开" },
	{ value: "name:asc", label: "名称 A-Z" },
	{ value: "name:desc", label: "名称 Z-A" },
	{ value: "opens:desc", label: "打开次数" },
	{ value: "created:desc", label: "最新创建" },
	{ value: "created:asc", label: "最早创建" },
];

/** 一个窄屏可换行的筛选项. */
function Slot({ children, wide }: { children: JSX.Element; wide?: boolean }): JSX.Element {
	return <div className={wide ? "min-w-48 flex-1" : "w-36 shrink-0"}>{children}</div>;
}

/** 表格上方的工具条: 搜索, 状态, 内核 (族, 未指定, 逐个内核, 带个数), 出口 (按检测结果, 带个数), 标签, 排序, 每页条数. 改任一条件都回到第 1 页. */
export function EnvToolbar(props: {
	query: EnvQuery;
	tags: string[];
	kernelOptions: { value: string; label: string }[];
	exitOptions: { value: ExitFilter; label: string }[];
	total: number;
	onChange: (patch: Partial<EnvQuery>) => void;
}): JSX.Element {
	const { query: q, onChange } = props;
	const set = (patch: Partial<EnvQuery>): void => onChange({ ...patch, page: 1 });
	const tagOptions = [{ value: ALL_TAGS, label: "全部标签" }, ...props.tags.map((t) => ({ value: t, label: t }))];

	return (
		<div className="flex flex-wrap items-center gap-2">
			<Slot wide>
				<InputGroup>
					<InputGroupAddon><Search className="size-4" /></InputGroupAddon>
					<InputGroupInput value={q.q} onChange={(e) => set({ q: e.target.value })} placeholder="搜索名称, id, 代理, 标签" aria-label="搜索浏览器" />
				</InputGroup>
			</Slot>
			<Slot>
				<SelectField value={q.status} onChange={(v) => set({ status: v as StatusFilter })} options={STATUS_OPTIONS} />
			</Slot>
			<Slot>
				<SelectField
					value={q.kernel || ALL_KERNELS}
					onChange={(v) => set({ kernel: v === ALL_KERNELS ? "" : v })}
					options={props.kernelOptions.map((o) => ({ value: o.value || ALL_KERNELS, label: o.label }))}
				/>
			</Slot>
			<Slot>
				<div data-exit-filter={q.exit} className="contents">
					<SelectField value={q.exit} onChange={(v) => set({ exit: v as ExitFilter })} options={props.exitOptions} />
				</div>
			</Slot>
			<Slot>
				<SelectField value={q.tag || ALL_TAGS} onChange={(v) => set({ tag: v === ALL_TAGS ? "" : v })} options={tagOptions} />
			</Slot>
			<Slot>
				<SelectField
					value={q.sort + ":" + q.dir}
					onChange={(v) => {
						const [sort, dir] = v.split(":");
						set({ sort: sort as SortKey, dir: dir === "asc" ? "asc" : "desc" });
					}}
					options={SORT_OPTIONS}
				/>
			</Slot>
			<Slot>
				<SelectField
					value={String(q.size)}
					onChange={(v) => set({ size: Number(v) })}
					options={PAGE_SIZES.map((n) => ({ value: String(n), label: "每页 " + n }))}
				/>
			</Slot>
			<span className="ml-auto t-aux text-muted-foreground">共 {props.total} 个</span>
		</div>
	);
}
