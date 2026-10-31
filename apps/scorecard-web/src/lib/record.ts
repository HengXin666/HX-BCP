/** 一个站在一个内核上的结论, record 是只记录不打分的站 */
export type CellVerdict = "pass" | "fail" | "unmeasured" | "record";

/** 内核整体结论, 与 gate 的 Verdict 一致 */
export type KernelVerdict = "pass" | "block" | "incomplete";

/** 一个站的精简结果, note 只在未测或记录时有值, 未测时就是未测原因 */
export type SiteCell = {
	id: string;
	name: string;
	verdict: CellVerdict;
	score: number | null;
	note: string;
};

/** 一个内核一次测评的精简结果, reason 非空表示整个内核未测 */
export type KernelRecord = {
	kernel: string;
	label: string;
	ua: string;
	capturedAt: string;
	score: number | null;
	verdict: KernelVerdict;
	reason: string;
	sites: SiteCell[];
};

/** data/pr-<pr>/<no>.json 的内容, svg 路径相对 data/pr-<pr>/ */
export type CaseRecord = {
	schema: 1;
	pr: number;
	no: number;
	recordedAt: string;
	commit: string;
	rawBase: string;
	svg: { overview: string; kernels: Record<string, string> };
	kernels: KernelRecord[];
};

/** data/pr-<pr>/index.json 的内容, runs 从新到旧 */
export type CaseIndex = {
	pr: number;
	runs: { no: number; recordedAt: string; commit: string }[];
};

/** 内核里未测的站数 */
export function unmeasuredCount(kernel: KernelRecord): number {
	return kernel.sites.filter((site) => site.verdict === "unmeasured").length;
}

/** 内核是否有分, 整体未测或没有分数都算没有 */
export function hasScore(kernel: KernelRecord): boolean {
	return kernel.reason === "" && kernel.score !== null;
}

/** index 里排在 no 后面的那一次, 即上一次测评, 没有则为 null */
export function previousNo(index: CaseIndex, no: number): number | null {
	const older = index.runs.map((run) => run.no).filter((item) => item < no);
	return older.length ? Math.max(...older) : null;
}
