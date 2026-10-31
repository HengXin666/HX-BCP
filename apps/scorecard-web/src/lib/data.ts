import type { CaseIndex, CaseRecord } from "./record.ts";

/** 本页面所属的 PR 号与测评序号, 构建时注入 */
export const CASE_PR = __CASE_PR__;
export const CASE_NO = __CASE_NO__;

/** 本页面的绝对 base, 由 Vite base 与当前域名拼成 */
function pageBase(): URL {
	return new URL(import.meta.env.BASE_URL, window.location.origin);
}

/** data 根目录, 默认是 /ci/case/<pr>/<no>/ 往上四级的 data/, 可用 VITE_DATA_ROOT 覆盖 */
export function dataRoot(): URL {
	return new URL(import.meta.env.VITE_DATA_ROOT || "../../../../data/", pageBase());
}

/** 某个 PR 的数据目录 data/pr-<pr>/ */
export function prDir(pr: number): URL {
	return new URL(`pr-${pr}/`, dataRoot());
}

/** 同一个 PR 第 no 次测评的页面地址 */
export function casePage(no: number): string {
	return new URL(`../${no}/`, pageBase()).href;
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
