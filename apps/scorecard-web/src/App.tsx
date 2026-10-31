import { Alert, AlertDescription, AlertTitle, PageHeader, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Spin, Tabs, TabsContent, TabsList, TabsTrigger } from "@hx/ui";
import { useEffect, useMemo, useState } from "react";
import { CaseOverview } from "./components/CaseOverview";
import { CompareView } from "./components/CompareView";
import { SvgLinks } from "./components/SvgLinks";
import { CASE_NO, CASE_PR, casePage, loadCase, loadIndex } from "./lib/data.ts";
import { compareCases } from "./lib/diff.ts";
import { type CaseIndex, type CaseRecord, previousNo } from "./lib/record.ts";

type Loaded = { index: CaseIndex; current: CaseRecord; previous: CaseRecord | null };

/** 读 index 与选中的那次测评, 再读它的上一次, 上一次读不到当作错误而不是首次 */
async function loadAll(no: number): Promise<Loaded> {
	const index = await loadIndex(CASE_PR);
	const current = await loadCase(CASE_PR, no);
	const prev = previousNo(index, no);
	const previous = prev === null ? null : await loadCase(CASE_PR, prev);
	return { index, current, previous };
}

/** 评分卡页面: 选一次测评, 看本次各内核, 展开单个内核, 与上次对比, 打开 svg */
export function App() {
	const [no, setNo] = useState(CASE_NO);
	const [data, setData] = useState<Loaded | null>(null);
	const [error, setError] = useState("");
	useEffect(() => {
		setData(null);
		setError("");
		loadAll(no).then(setData, (reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason)));
	}, [no]);
	const diff = useMemo(() => (data ? compareCases(data.current, data.previous) : null), [data]);
	const runs = data?.index.runs ?? [];
	const picker = (
		<Select value={String(no)} onValueChange={(value) => setNo(Number(value))}>
			<SelectTrigger className="w-48">
				<SelectValue placeholder={`第 ${no} 次`} />
			</SelectTrigger>
			<SelectContent>
				{runs.map((run) => (
					<SelectItem key={run.no} value={String(run.no)}>{`第 ${run.no} 次 ${run.recordedAt}`}</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
	const header = (
		<PageHeader
			title={`PR #${CASE_PR} 第 ${no} 次测评`}
			desc={no === CASE_NO ? "本页对应的这次测评" : <a href={casePage(no)}>这一次的独立页面</a>}
			right={picker}
		/>
	);
	if (error) {
		return (
			<main className="mx-auto max-w-6xl p-6">
				{header}
				<Alert variant="danger">
					<AlertTitle>数据没取到, 不显示任何分数</AlertTitle>
					<AlertDescription>{error}</AlertDescription>
				</Alert>
			</main>
		);
	}
	if (!data || !diff) {
		return (
			<main className="mx-auto max-w-6xl p-6">
				{header}
				<Spin />
			</main>
		);
	}
	return (
		<main className="mx-auto flex max-w-6xl flex-col gap-4 p-6">
			{header}
			<Tabs defaultValue="now">
				<TabsList>
					<TabsTrigger value="now">本次</TabsTrigger>
					<TabsTrigger value="compare">{diff.first ? "对比 首次" : `对比第 ${diff.previousNo} 次`}</TabsTrigger>
					<TabsTrigger value="svg">图与链接</TabsTrigger>
				</TabsList>
				<TabsContent value="now" className="flex flex-col gap-4">
					<CaseOverview record={data.current} />
				</TabsContent>
				<TabsContent value="compare">
					<CompareView diff={diff} />
				</TabsContent>
				<TabsContent value="svg">
					<SvgLinks record={data.current} />
				</TabsContent>
			</Tabs>
		</main>
	);
}
