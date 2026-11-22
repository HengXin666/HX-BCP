/**
 * 浏览器内核: 列表只读 GET /api/v1/kernels (后端 kernels.list_kernels), 前端不维护内核清单.
 * 族决定怎么配置 (blink: Preferences JSON + CDP; gecko: user.js + 远程调试协议; webkit: 本机 Playwright WebKit 构建经 runner 启动,
 * 或外部应用 Safarier 经它的 CLI 启动), 内核只决定用哪个二进制.
 * 每个环境实际用哪个内核, 判据与后端 kernels.resolve_executable 一致:
 *   fingerprint.kernel 有值 -> 就是它 (指定)
 *   为空 (旧环境) -> 族按 fingerprint.browser (flower = gecko, safari = webkit, 其余 blink),
 *                    二进制取设置里的 chromium_path / firefox_path / kernel_paths.safari (继承)
 *   运行中再用 runtime.kernel 核对 (实测)
 */
import { createContext } from "react";
import { API, getEnvelope, type Env, type Result } from "./api";
import { KEYS, invalidate, keyed } from "./cache";
import { sendCompat } from "./http";

export type Family = "blink" | "gecko" | "webkit";

/** 一项能力在这个内核上是否可用; 判据在后端 kernels.FEATURES. */
export type KernelFeatures = Partial<Record<"cdp" | "live_view" | "cookies" | "sandbox" | "fingerprint_check" | "proxy" | "agent_cli", boolean>>;

/**
 * 后端 kernels.list_kernels 的一行. source: configured (设置里填了路径) / detected (自动探测) / missing.
 * mode: binary (我们起进程) / runner (本机 runner 进程, 如 Linux WebKit) / external (外部应用自管进程与 profile, 如 Safarier). 旧后端没有的字段都是可选.
 */
export type Kernel = {
	id: string;
	label: string;
	family: Family;
	path: string;
	available: boolean;
	mode?: "binary" | "runner" | "external";
	configured?: string;
	source?: "configured" | "detected" | "missing";
	note?: string;
	how?: string;
	features?: KernelFeatures;
	default?: boolean;
	envs?: number;
	/** 只能跑在这些宿主系统上 (sys.platform 前缀, 如 darwin), 空 = 不限 */
	platforms?: string[];
	/** 本宿主能否直接跑; false 时只能经远程命令使用 */
	host_supported?: boolean;
};

const PLATFORM_NAME: Record<string, string> = { darwin: "macOS", linux: "Linux", win32: "Windows" };

/** 平台限制的标签文案, 如 "仅 macOS"; 不限平台时为空 */
export function platformTag(k: Kernel): string {
	const p = k.platforms ?? [];
	return p.length ? "仅 " + p.map((x) => PLATFORM_NAME[x] ?? x).join(" / ") : "";
}

/** 不可用时的短原因: 宿主系统不支持时写 "需要 macOS" 等, 否则 "未安装" */
export function missingText(k: Kernel): string {
	return k.host_supported === false ? "需要 " + (k.platforms ?? []).map((x) => PLATFORM_NAME[x] ?? x).join(" / ") : "未安装";
}

export type KernelsDetail = { kernels: Kernel[]; default: string; inherit: { envs: number; blink: string; gecko: string } };

type FamilyStyle = { name: string; how: string; dot: string; chip: string };

/** 各族的展示: 名称, 配置方式, 颜色 (blink 天蓝, gecko 橙, webkit 灰紫). 只用于界面, 判据在后端 kernels.family_of. */
export const FAMILY: Record<Family, FamilyStyle> = {
	blink: { name: "Chromium 系", how: "Preferences JSON + CDP", dot: "bg-sky-500", chip: "border-sky-500/40 bg-sky-500/10 text-sky-500" },
	gecko: { name: "Firefox 系", how: "user.js + 远程调试协议", dot: "bg-orange-500", chip: "border-orange-500/40 bg-orange-500/10 text-orange-500" },
	webkit: { name: "WebKit 系", how: "MiniBrowser 用户脚本 / 本机 runner / Safarier CLI, 均无 CDP", dot: "bg-violet-500", chip: "border-violet-500/40 bg-violet-500/10 text-violet-500" },
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

/** 能力名的中文, 内核页与选择器共用. */
export const FEATURE_LABEL: Record<keyof KernelFeatures, string> = {
	cdp: "CDP 接管",
	live_view: "实时预览",
	cookies: "Cookie 注入",
	sandbox: "沙箱隔离",
	fingerprint_check: "指纹体检",
	proxy: "代理",
	agent_cli: "自带 Agent CLI",
};

/** 内核管理页的完整数据 (含每个内核被多少环境使用, 默认内核, 继承环境数). */
export const kernelsDetail = keyed<KernelsDetail>(KEYS.kernels + ":detail", 10000, (signal) =>
	getEnvelope<KernelsDetail>(API + "/kernels", signal).then((r): Result<KernelsDetail> =>
		r.ok ? { ok: true, data: { kernels: r.data?.kernels ?? [], default: r.data?.default ?? "", inherit: r.data?.inherit ?? { envs: 0, blink: "", gecko: "" } } } : r,
	),
);

export type ProbeResult = { id: string; ok: boolean; version?: string; error?: string; profiles?: number; running?: number };

/** 内核写操作: 只有人类通道能调, 成功后整组 kernels 缓存作废 (列表与详情一起重拉). */
export const kernelApi = {
	setPath: async (id: string, path: string): Promise<Result<Kernel>> => {
		const r = await sendCompat<Kernel>("PUT", "/kernels/" + encodeURIComponent(id) + "/path", { path });
		if (r.ok) invalidate(KEYS.kernels);
		return r;
	},
	setDefault: async (id: string): Promise<Result<{ default: string }>> => {
		const r = await sendCompat<{ default: string }>("PUT", "/kernels/" + encodeURIComponent(id) + "/default");
		if (r.ok) invalidate(KEYS.kernels);
		return r;
	},
	probe: (id: string): Promise<Result<ProbeResult>> => sendCompat<ProbeResult>("POST", "/kernels/" + encodeURIComponent(id) + "/probe"),
};

/** 内核清单很少变, 会话内 25 秒新鲜期. */
export const listKernels = keyed<Kernel[]>(KEYS.kernels, 25000, (signal) =>
	getEnvelope<{ kernels: Kernel[] }>(API + "/kernels", signal).then((r): Result<Kernel[]> => (r.ok ? { ok: true, data: r.data?.kernels ?? [] } : r)),
);

/** 旧环境继承的可执行路径. GET /settings 还带别的字段 (含 api_key), 这里当场只取这三个, 其余丢弃. */
export type KernelPaths = Record<Family, string>;

/** 键挂在 settings 下: 保存设置后 invalidate(settings) 一并作废. 缓存里只存路径, 不存 api_key. */
export const kernelPaths = keyed<KernelPaths>(KEYS.settings + ":paths", 25000, (signal) =>
	getEnvelope<{ chromium_path?: string; firefox_path?: string; kernel_paths?: Record<string, string> }>(API + "/settings", signal).then((r): Result<KernelPaths> =>
		r.ok ? { ok: true, data: { blink: r.data?.chromium_path ?? "", gecko: r.data?.firefox_path ?? "", webkit: r.data?.kernel_paths?.safari ?? "" } } : r,
	),
);

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

/** 新建时的默认内核: 后端标了 default 就用它 (内核页可改), 否则 cloak 可用就用 cloak, 否则第一个可用的. */
export function defaultKernel(kernels: Kernel[]): string {
	return kernels.find((k) => k.default && k.available)?.id ?? kernels.find((k) => k.id === "cloak" && k.available)?.id ?? kernels.find((k) => k.available)?.id ?? "";
}

/** 环境当前所属族: 指定了已登记内核就按内核, 否则按旧 browser 字段. 切换内核时只能在同族里换. */
/** 旧名, 同 envFamily */
export const familyOf = envFamily;

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
	for (const k of kernels) out.push({ value: k.id, label: shortName(k.label) + (k.available ? "" : " " + missingText(k)) + " (" + n((v) => v.id === k.id) + ")" });
	return out;
}
