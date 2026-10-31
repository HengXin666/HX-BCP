import type { CaseIndex, CaseRecord } from "./record.ts";

/** 本页面所属的 PR 号与测评序号, 构建时注入 */
export const CASE_PR = __CASE_PR__;
export const CASE_NO = __CASE_NO__;

/** 本次测评的根地址, 末尾一定带斜杠; 子页面是它的下一级 */
function caseBase(): URL {
	return new URL(import.meta.env.BASE_URL, window.location.origin);
}

/**
 * data 根目录。按 base 推算而不是按当前页面路径往回退:
 * 子页面 /ci/case/18/1/now/ 往回退四级会落到 /HX-BCP/ci/data/ 取不到,
 * 而 base 永远是 /ci/case/<pr>/<no>/, 从它退四级才稳定。可用 VITE_DATA_ROOT 覆盖。
 */
export function dataRoot(): URL {
	return new URL(import.meta.env.VITE_DATA_ROOT || "../../../../data/", caseBase());
}

/** 某个 PR 的数据目录 data/pr-<pr>/ */
export function prDir(pr: number): URL {
	return new URL(`pr-${pr}/`, dataRoot());
}

/** 同一个 PR 第 no 次测评的总览地址, 也是同次其它页面的父路径 */
export function casePage(no: number): string {
	return new URL(`../${no}/`, caseBase()).href;
}

/** svg 在站点上的地址与可复制的 raw 地址, rawBase 为空时 raw 地址就用站点地址 */
export function svgUrls(record: CaseRecord, path: string): { view: string; raw: string } {
	const view = new URL(path, prDir(record.pr)).href;
	const raw = record.rawBase ? `${record.rawBase.replace(/\/+$/, "")}/data/pr-${record.pr}/${path}` : view;
	return { view, raw };
}

/** 取 JSON, 非 2xx 时抛出带地址与状态码的错误 */
async function getJson<T>(url: URL): Promise<T> {
	const response = await fetch(url, { cache: "no-cache" });
	if (!response.ok) throw new Error(`没取到 ${url.pathname}, HTTP ${response.status}`);
	return (await response.json()) as T;
}

/** 读 data/pr-<pr>/index.json */
export function loadIndex(pr: number): Promise<CaseIndex> {
	return getJson<CaseIndex>(new URL("index.json", prDir(pr)));
}

/** 读 data/pr-<pr>/<no>.json */
export function loadCase(pr: number, no: number): Promise<CaseRecord> {
	return getJson<CaseRecord>(new URL(`${no}.json`, prDir(pr)));
}
