/**
 * 新建与编辑浏览器的表单模型. 字段名与后端 models.Fingerprint / Environment / Launch 一致.
 * 所有值都按字符串或布尔存, 空串 = 留空 = 交给后端默认 (见 AUTO), 新建时不发, 编辑时只发改过的字段.
 */
import type { Env } from "./api";
import { kernelPatch, type Kernel } from "./kernels";

export type FpForm = {
	name: string;
	os: string;
	folder: string;
	tags: string;
	notes: string;
	proxyLink: string;
	kernel: string;
	device_type: string;
	resolution: string;
	color_depth: string;
	hardware_concurrency: string;
	device_memory: string;
	user_agent: string;
	language: string;
	timezone: string;
	webgl_vendor: string;
	webgl_renderer: string;
	canvas_noise: boolean;
	audio_noise: boolean;
	webgl_noise: boolean;
	webrtc_mode: string;
	webrtc_public_ip: string;
	urls: string;
	extra_args: string;
	headless: boolean;
	tls_mode: string;
	dark_mode: string; // "" 跟随全局, on, off
};

/** 留空时后端用的值 (models.Fingerprint 默认值), 只用于提示文字与编辑时清空字段. */
export const AUTO = {
	os: "Windows",
	device_type: "desktop",
	resolution: "1280x820",
	color_depth: "24",
	hardware_concurrency: "0",
	device_memory: "0",
	webrtc_mode: "real",
	tls_mode: "none",
} as const;

export const PRESETS = {
	resolution: ["1280x820", "1366x768", "1440x900", "1536x864", "1600x900", "1920x1080", "2560x1440", "1512x982", "1728x1117"],
	timezone: ["Asia/Shanghai", "Asia/Hong_Kong", "Asia/Tokyo", "Asia/Singapore", "Europe/London", "Europe/Berlin", "America/New_York", "America/Chicago", "America/Los_Angeles", "UTC"],
	language: ["zh-CN", "zh-TW", "en-US", "en-GB", "ja-JP", "ko-KR", "de-DE", "fr-FR"],
};

export const BLANK_FORM: FpForm = {
	name: "", os: "", folder: "", tags: "", notes: "", proxyLink: "", kernel: "",
	device_type: "", resolution: "", color_depth: "", hardware_concurrency: "", device_memory: "",
	user_agent: "", language: "", timezone: "", webgl_vendor: "", webgl_renderer: "",
	canvas_noise: false, audio_noise: false, webgl_noise: false, webrtc_mode: "", webrtc_public_ip: "",
	urls: "", extra_args: "", headless: false, tls_mode: "", dark_mode: "",
};

const FP_TEXT = ["os", "device_type", "resolution", "user_agent", "language", "timezone", "webgl_vendor", "webgl_renderer", "webrtc_mode", "webrtc_public_ip", "tls_mode"] as const;
const FP_NUM = ["color_depth", "hardware_concurrency", "device_memory"] as const;
const FP_BOOL = ["canvas_noise", "audio_noise", "webgl_noise", "headless"] as const;

const lines = (t: string): string[] => t.split(/\n+/).map((s) => s.trim()).filter(Boolean);
const tagList = (t: string): string[] => [...new Set(t.split(/[,\s]+/).map((s) => s.trim()).filter(Boolean))];
const str = (v: unknown): string => (v === undefined || v === null || v === 0 ? "" : String(v));

/** 编辑时的初始值: 取环境现有配置, 等于后端默认的显示为空 (= 自动). */
export function formOf(env: Env): FpForm {
	const fp = (env.fingerprint ?? {}) as Record<string, unknown>;
	const f: FpForm = { ...BLANK_FORM, name: env.name, folder: env.folder, tags: (env.tags ?? []).join(", "), notes: env.notes ?? "", kernel: str(fp.kernel) };
	for (const k of FP_TEXT) f[k] = str(fp[k]) === (AUTO as Record<string, string>)[k] ? "" : str(fp[k]);
	for (const k of FP_NUM) f[k] = str(fp[k]) === (AUTO as Record<string, string>)[k] ? "" : str(fp[k]);
	for (const k of FP_BOOL) f[k] = Boolean(fp[k]);
	f.dark_mode = fp.dark_mode === true ? "on" : fp.dark_mode === false ? "off" : "";
	f.urls = (env.launch?.urls ?? []).join("\n");
	f.extra_args = (env.launch?.extra_args ?? []).join("\n");
	return f;
}

/** 表单项的问题, 空数组 = 可以提交. 名称可以留空 (后端起名 Env-N). */
export function problems(f: FpForm): string[] {
	const out: string[] = [];
	if (f.resolution && !/^\d{3,5}x\d{3,5}$/.test(f.resolution)) out.push("分辨率写成 1920x1080");
	for (const k of FP_NUM) if (f[k] && !(Number(f[k]) > 0)) out.push(k + " 需为正数");
	if (f.webrtc_public_ip && !/^[\d.:a-fA-F]+$/.test(f.webrtc_public_ip)) out.push("WebRTC 公网 IP 格式不对");
	return out;
}

type Body = Record<string, unknown>;

/** 指纹段: before 为空时 (新建) 只发填了的; 有 before 时 (编辑) 只发改过的, 清空的发后端默认. */
function fpBody(f: FpForm, before: FpForm | null, kernels: Kernel[]): Body {
	const fp: Body = {};
	const changed = (k: keyof FpForm): boolean => (before ? before[k] !== f[k] : Boolean(f[k]));
	for (const k of FP_TEXT) if (changed(k)) fp[k] = f[k] || ((AUTO as Record<string, string>)[k] ?? "");
	for (const k of FP_NUM) if (changed(k)) fp[k] = Number(f[k] || (AUTO as Record<string, string>)[k]);
	for (const k of FP_BOOL) if (changed(k)) fp[k] = f[k];
	if (changed("dark_mode")) fp.dark_mode = f.dark_mode === "on" ? true : f.dark_mode === "off" ? false : null;
	if (changed("kernel")) Object.assign(fp, kernelPatch(kernels.find((k) => k.id === f.kernel)) ?? {});
	return fp;
}

/** POST /environments 或 PUT /environments/{id} 的请求体. */
export function bodyOf(f: FpForm, before: FpForm | null, kernels: Kernel[]): Body {
	const body: Body = {};
	const changed = (k: keyof FpForm): boolean => (before ? before[k] !== f[k] : Boolean(f[k]));
	if (changed("name")) body.name = f.name.trim();
	if (!before) body.folder = f.folder;
	else if (changed("folder")) body.folder = f.folder;
	if (changed("tags")) body.tags = tagList(f.tags);
	if (changed("notes")) body.notes = f.notes;
	if (f.proxyLink.trim()) body.proxy = { link: f.proxyLink.trim() };
	const fp = fpBody(f, before, kernels);
	if (Object.keys(fp).length) body.fingerprint = fp;
	if (changed("urls") || changed("extra_args")) body.launch = { urls: lines(f.urls), extra_args: lines(f.extra_args) };
	return body;
}
