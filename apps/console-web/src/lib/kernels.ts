/**
 * 浏览器内核: 列表只读 GET /api/v1/kernels (后端 kernels.list_kernels), 前端不维护内核清单.
 * 族决定怎么配置 (blink: Preferences JSON + CDP; gecko: user.js + 远程调试协议), 内核只决定用哪个二进制.
 * 每个环境实际用哪个内核, 判据与后端 kernels.resolve_executable 一致:
 *   fingerprint.kernel 有值 -> 就是它 (指定)
 *   为空 (旧环境) -> 族按 fingerprint.browser (flower = gecko, safari = webkit, 其余 blink),
 *                    二进制取设置里的 chromium_path / firefox_path / kernel_paths.safari (继承)
 *   运行中再用 runtime.kernel 核对 (实测)
 */
import { createContext } from "react";
import { API, getEnvelope, type Env, type Result } from "./api";

export type Family = "blink" | "gecko" | "webkit";

export type Kernel = { id: string; label: string; family: Family; path: string; available: boolean };

type FamilyStyle = { name: string; how: string; dot: string; chip: string };

/** 各族的展示: 名称, 配置方式, 颜色 (blink 天蓝, gecko 橙, webkit 灰紫). 只用于界面, 判据在后端 kernels.family_of. */
export const FAMILY: Record<Family, FamilyStyle> = {
	blink: { name: "Chromium 系", how: "Preferences JSON + CDP", dot: "bg-sky-500", chip: "border-sky-500/40 bg-sky-500/10 text-sky-500" },
	gecko: { name: "Firefox 系", how: "user.js + 远程调试协议", dot: "bg-orange-500", chip: "border-orange-500/40 bg-orange-500/10 text-orange-500" },
	webkit: { name: "WebKit 系", how: "用户脚本注入, 无 CDP / 无 headless", dot: "bg-violet-500", chip: "border-violet-500/40 bg-violet-500/10 text-violet-500" },
};

export const FAMILIES: Family[] = ["blink", "gecko", "webkit"];

/** 未知族兜底: 后端以后再加族时, 前端列表不因 FAMILY[x] 为 undefined 整页崩. */
const UNKNOWN_FAMILY: FamilyStyle = { name: "未知族", how: "前端未登记该族", dot: "bg-muted-foreground", chip: "border-border bg-muted text-muted-foreground" };

export function familyStyle(f: string): FamilyStyle {
	return (FAMILY as Record<string, FamilyStyle>)[f] ?? UNKNOWN_FAMILY;
}

/** 该族是否说 CDP (后端 kernels.speaks_cdp: 只有 webkit 不说). 不说 CDP 的环境没有 Cookie 注入/实时预览/指纹检测. */
export function speaksCdp(f: Family): boolean {
	return f !== "webkit";
}

/** 旧环境 (fingerprint.kernel 为空) 按 browser 字段判族, 与后端 kernels.family_of 的兜底一致. */
export function legacyFamily(browser: string | undefined): Family {
	return browser === "flower" ? "gecko" : browser === "safari" ? "webkit" : "blink";
}

/** 一个环境当前所属的族: kernel 对上已登记内核就取它的族, 否则按 browser 兜底. 编辑时锁族用它, 不再只看 flower. */
export function envFamily(env: Env, kernels: Kernel[]): Family {
	const fp = env.fingerprint ?? {};
	const k = kernels.find((x) => x.id === (fp.kernel ?? "").toLowerCase());
	return k ? k.family : legacyFamily(fp.browser);
}

export function listKernels(signal?: AbortSignal): Promise<Result<Kernel[]>> {
	return getEnvelope<{ kernels: Kernel[] }>(API + "/kernels", signal).then((r) => (r.ok ? { ok: true, data: r.data?.kernels ?? [] } : r));
}

/** 旧环境继承的可执行路径. GET /settings 还带别的字段 (含 api_key), 这里当场只取这三个, 其余丢弃. */
export type KernelPaths = Record<Family, string>;

export function kernelPaths(signal?: AbortSignal): Promise<Result<KernelPaths>> {
	return getEnvelope<{ chromium_path?: string; firefox_path?: string; kernel_paths?: Record<string, string> }>(API + "/settings", signal).then((r) =>
		r.ok
			? { ok: true, data: { blink: r.data?.chromium_path ?? "", gecko: r.data?.firefox_path ?? "", webkit: r.data?.kernel_paths?.safari ?? "" } }
			: r,
	);
}

const INHERIT_KEY: Record<Family, string> = { blink: "chromium_path", gecko: "firefox_path", webkit: "kernel_paths.safari" };

/** 短名: 去掉括号说明, 取最后一个词, 去掉 Browser 尾巴. "Google Chrome" -> Chrome, "CloakBrowser (Chromium 补丁版)" -> Cloak. */
export function shortName(label: string): string {
	const words = label.replace(/\(.*?\)/g, "").trim().split(/\s+/);
	const last = words[words.length - 1] || label;
	return last.length > 7 && last.endsWith("Browser") ? last.slice(0, -7) : last;
}

/** 一个环境的内核结论. how: 指定 / 继承设置 / 运行中实测. id 为空表示继承的路径没有对上任何已登记内核. */
export type KernelView = { id: string; short: string; family: Family; how: "指定" | "继承" | "实测"; path: string; title: string };

export function kernelView(env: Env, kernels: Kernel[], paths: KernelPaths | null): KernelView {
	const fp = env.fingerprint ?? {};
	const byId = (id: string): Kernel | undefined => kernels.find((k) => k.id === (id || "").toLowerCase());
	const ran = byId(String(env.runtime?.kernel ?? ""));
	const set = byId(fp.kernel ?? "");
	let k = set;
	let how: KernelView["how"] = "指定";
	let family: Family = legacyFamily(fp.browser);
	let path = "";
	if (ran) (k = ran), (how = "实测");
	if (!k) {
		how = "继承";
		path = paths ? paths[family] ?? "" : "";
		k = path ? kernels.find((x) => x.family === family && x.path === path) : undefined;
	}
	if (k) (family = k.family), (path = k.path || path);
	const short = k ? shortName(k.label) : "未登记";
	const from = how === "指定" ? "环境指定 fingerprint.kernel" : how === "实测" ? "运行中实测 runtime.kernel" : "旧环境未指定, 继承设置里的 " + (INHERIT_KEY[family] ?? "chromium_path");
	const fs = familyStyle(family);
	const title = [(k ? k.label : "路径未对上已登记内核"), fs.name + " (" + fs.how + ")", "来源: " + from, "路径: " + (path || "未上报")].join("\n");
	return { id: k?.id ?? "", short, family, how, path, title };
}

/** 列表各处读同一个判定函数, 由浏览器页提供. 默认值只在没包 Provider 时用到. */
export const KernelContext = createContext<(env: Env) => KernelView>((env) => kernelView(env, [], null));

/** 新建时的默认内核: cloak 可用就用 cloak, 否则第一个可用的. */
export function defaultKernel(kernels: Kernel[]): string {
	return kernels.find((k) => k.id === "cloak" && k.available)?.id ?? kernels.find((k) => k.available)?.id ?? "";
}

/** 写回后端的指纹片段: kernel 加上与之对应的旧 browser 字段 (后端 legacy_browser_for: gecko = flower, webkit = safari, 其余 sun). */
export function kernelPatch(k: Kernel | undefined): { kernel: string; browser: string } | null {
	return k ? { kernel: k.id, browser: k.family === "gecko" ? "flower" : k.family === "webkit" ? "safari" : "sun" } : null;
}

/**
 * 工具条的内核筛选项, 带当前范围内的个数: 全部, 每族, 未指定 (继承), 再逐个内核. 未安装的内核也列出 (标 未安装),
 * 好看出 "有没有环境指定了一个本机没装的内核".
 */
export function kernelFilterOptions(kernels: Kernel[], envs: Env[], view: (env: Env) => KernelView): { value: string; label: string }[] {
	const views = envs.map(view);
	const n = (hit: (v: KernelView) => boolean): number => views.filter(hit).length;
	const out = [{ value: "", label: "全部内核 (" + envs.length + ")" }];
	for (const fam of FAMILIES) out.push({ value: "fam:" + fam, label: familyStyle(fam).name + " (" + n((v) => v.family === fam) + ")" });
	out.push({ value: "inherit", label: "未指定, 继承设置 (" + n((v) => v.how === "继承") + ")" });
	for (const k of kernels) out.push({ value: k.id, label: shortName(k.label) + (k.available ? "" : " 未安装") + " (" + n((v) => v.id === k.id) + ")" });
	return out;
}
