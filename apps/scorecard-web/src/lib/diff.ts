import { type CaseRecord, type CellVerdict, type KernelRecord, unmeasuredCount } from "./record.ts";

/** 升降方向, first 表示没有上一次, new 表示上次没有这个内核, gone 表示这次没有 */
export type Trend = "better" | "worse" | "same" | "first" | "new" | "gone";

/** 一个站的结论变化, better 是不过变过, worse 是过变不过, changed 是涉及未测或记录的其他变化 */
export type SiteFlip = {
	id: string;
	name: string;
	from: CellVerdict;
	to: CellVerdict;
	direction: "better" | "worse" | "changed";
};

/** 一个内核这次与上次的对比 */
export type KernelDiff = {
	kernel: string;
	label: string;
	before: number | null;
	after: number | null;
	delta: number | null;
	trend: Trend;
	flips: SiteFlip[];
	unmeasuredBefore: number | null;
	unmeasuredAfter: number | null;
};

/** 整次测评的对比, previousNo 为 null 时 first 为真 */
export type CaseDiff = {
	first: boolean;
	previousNo: number | null;
	kernels: KernelDiff[];
	better: number;
	worse: number;
	same: number;
	unmeasuredDelta: number | null;
};

const EPSILON = 0.05;

/** 两个站结论之间的方向 */
export function flipDirection(from: CellVerdict, to: CellVerdict): SiteFlip["direction"] {
	if (from === "pass" && to === "fail") return "worse";
	if (from === "fail" && to === "pass") return "better";
	return "changed";
}

/** 按站 id 对齐两次结果, 只返回结论变了的站 */
export function siteFlips(before: KernelRecord, after: KernelRecord): SiteFlip[] {
	const flips: SiteFlip[] = [];
	for (const site of after.sites) {
		const old = before.sites.find((item) => item.id === site.id);
		const from: CellVerdict = old ? old.verdict : "unmeasured";
		if (from === site.verdict) continue;
		flips.push({ id: site.id, name: site.name, from, to: site.verdict, direction: flipDirection(from, site.verdict) });
	}
	return flips;
}

/** 两次都有分时按分差判断, 否则按过与不过的翻转数判断, 都持平就是没变 */
export function kernelTrend(before: number | null, after: number | null, flips: SiteFlip[]): Trend {
	if (before !== null && after !== null && Math.abs(after - before) >= EPSILON) return after > before ? "better" : "worse";
	const up = flips.filter((flip) => flip.direction === "better").length;
	const down = flips.filter((flip) => flip.direction === "worse").length;
	if (up > down) return "better";
	if (down > up) return "worse";
	return "same";
}

/** 一个内核的对比, before 为 undefined 时表示上一次没有这个内核 */
export function compareKernel(after: KernelRecord, before: KernelRecord | undefined, first: boolean): KernelDiff {
	const base = { kernel: after.kernel, label: after.label, after: after.score, unmeasuredAfter: unmeasuredCount(after) };
	if (!before) return { ...base, before: null, delta: null, trend: first ? "first" : "new", flips: [], unmeasuredBefore: null };
	const flips = siteFlips(before, after);
	const delta = before.score !== null && after.score !== null ? Math.round((after.score - before.score) * 10) / 10 : null;
	return { ...base, before: before.score, delta, trend: kernelTrend(before.score, after.score, flips), flips, unmeasuredBefore: unmeasuredCount(before) };
}

/** 本次与上次的完整对比, previous 为 null 时所有内核都是首次测评 */
export function compareCases(current: CaseRecord, previous: CaseRecord | null): CaseDiff {
	const first = previous === null;
	const kernels = current.kernels.map((kernel) => compareKernel(kernel, previous?.kernels.find((item) => item.kernel === kernel.kernel), first));
	for (const gone of previous?.kernels ?? []) {
		if (current.kernels.some((item) => item.kernel === gone.kernel)) continue;
		kernels.push({
			kernel: gone.kernel,
			label: gone.label,
			before: gone.score,
			after: null,
			delta: null,
			trend: "gone",
			flips: [],
			unmeasuredBefore: unmeasuredCount(gone),
			unmeasuredAfter: null,
		});
	}
	const total = (record: CaseRecord) => record.kernels.reduce((sum, kernel) => sum + unmeasuredCount(kernel), 0);
	const count = (trend: Trend) => kernels.filter((kernel) => kernel.trend === trend).length;
	return {
		first,
		previousNo: previous ? previous.no : null,
		kernels,
		better: count("better"),
		worse: count("worse"),
		same: count("same"),
		unmeasuredDelta: previous ? total(current) - total(previous) : null,
	};
}
