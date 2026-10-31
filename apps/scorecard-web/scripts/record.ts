import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { isBundle, loadCards, stateAt } from "../../../packages/browser/gate/multi.ts";
import type { CaseIndex, CaseRecord, KernelRecord } from "../src/lib/record.ts";

const NOTE_MAX = 160;

/** 解析 --key value 形式的参数, 缺必填项时报错退出 */
function parseArgs(argv: string[]): Record<string, string> {
	const out: Record<string, string> = {};
	for (let i = 0; i < argv.length; i += 2) {
		if (!argv[i].startsWith("--") || argv[i + 1] === undefined) throw new Error(`参数不成对 ${argv[i]}`);
		out[argv[i].slice(2)] = argv[i + 1];
	}
	for (const key of ["bundle", "pr", "data"]) if (!out[key]) throw new Error(`缺 --${key}`);
	return out;
}

/** 把 gate 的多内核输入摘成只含必要字段的内核记录, 不带 excerpt */
export function toKernels(bundlePath: string): KernelRecord[] {
	const data: unknown = JSON.parse(readFileSync(bundlePath, "utf8"));
	if (!isBundle(data)) throw new Error(`${bundlePath} 不是多内核输入`);
	return loadCards(data).map((card) => ({
		kernel: card.kernel,
		label: card.label,
		ua: card.ua,
		capturedAt: card.capturedAt,
		score: card.reason ? null : card.report.score,
		verdict: card.report.verdict,
		reason: card.reason,
		sites: card.report.results.map((result) => {
			const verdict = stateAt(card, result.siteId);
			const note = verdict === "unmeasured" || verdict === "record" ? result.headline.slice(0, NOTE_MAX) : "";
			return { id: result.siteId, name: result.name, verdict, score: result.score, note };
		}),
	}));
}

/** 读已有 index, 不存在时返回空 index */
function readIndex(path: string, pr: number): CaseIndex {
	return existsSync(path) ? (JSON.parse(readFileSync(path, "utf8")) as CaseIndex) : { pr, runs: [] };
}

/** 写 data/pr-<pr>/<no>.json 并更新 index.json, no 省略时取已有最大值加 1, svg 拷到 data/pr-<pr>/<no>/ */
function main(): void {
	const args = parseArgs(process.argv.slice(2));
	const pr = Number(args.pr);
	const dir = join(args.data, `pr-${pr}`);
	mkdirSync(dir, { recursive: true });
	const indexPath = join(dir, "index.json");
	const index = readIndex(indexPath, pr);
	const no = args.no ? Number(args.no) : Math.max(0, ...index.runs.map((run) => run.no)) + 1;
	if (!Number.isInteger(pr) || pr < 1 || !Number.isInteger(no) || no < 1) throw new Error(`pr 与 no 必须是从 1 起的整数, 拿到 ${pr} ${no}`);
	const svg: CaseRecord["svg"] = { overview: "", kernels: {} };
	if (args.svg) {
		mkdirSync(join(dir, String(no)), { recursive: true });
		for (const name of readdirSync(args.svg).filter((item) => item.endsWith(".svg"))) {
			copyFileSync(join(args.svg, name), join(dir, String(no), name));
			if (name === "all-kernels.svg") svg.overview = `${no}/${name}`;
			else if (name.startsWith("kernel-")) svg.kernels[name.slice("kernel-".length, -".svg".length)] = `${no}/${name}`;
		}
	}
	const recordedAt = args.at || new Date().toISOString();
	const commit = args.commit || "";
	const record: CaseRecord = { schema: 1, pr, no, recordedAt, commit, rawBase: args["raw-base"] || "", svg, kernels: toKernels(args.bundle) };
	writeFileSync(join(dir, `${no}.json`), `${JSON.stringify(record, null, 1)}\n`);
	const runs = [{ no, recordedAt, commit }, ...index.runs.filter((run) => run.no !== no)].sort((a, b) => b.no - a.no);
	writeFileSync(indexPath, `${JSON.stringify({ pr, runs }, null, 1)}\n`);
	console.log(`wrote ${join(dir, `${no}.json`)} kernels=${record.kernels.length} runs=${runs.map((run) => run.no).join(",")}`);
}

if (process.argv[1]?.endsWith("record.ts")) main();
