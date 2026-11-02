import type { JSX, ReactNode } from "react";
import { Badge, InputField, SelectField, Switch, Textarea } from "@hx/ui";
import { AUTO, PRESETS, type FpForm } from "../../lib/fp-form";
import type { Kernel } from "../../lib/kernels";
import { KernelPicker } from "./KernelBits";

export type SectionProps = { form: FpForm; set: (patch: Partial<FpForm>) => void };

const ROOT = "__root__";
const PICK = "__pick__";

/** 一个表单项: 标题, 可选的 "留空=自动 (默认值)" 标记, 一句说明, 控件. */
function F(props: { label: string; auto?: string; hint?: string; children: ReactNode; wide?: boolean }): JSX.Element {
	return (
		<label className={"flex min-w-0 flex-col gap-1 t-aux text-muted-foreground" + (props.wide ? " col-span-2" : "")}>
			<span className="flex items-center gap-2 text-foreground">
				{props.label}
				{props.auto !== undefined ? <Badge variant="secondary" className="px-1 t-aux font-normal">留空=自动{props.auto ? " (" + props.auto + ")" : ""}</Badge> : null}
			</span>
			{props.children}
			{props.hint ? <span className="t-aux">{props.hint}</span> : null}
		</label>
	);
}

/** 文本框 + 常用值下拉: 选预设填进文本框, 也可以直接手填. */
function Preset(props: { value: string; onChange: (v: string) => void; presets: string[]; placeholder: string; label: string }): JSX.Element {
	return (
		<span className="flex min-w-0 gap-2">
			<InputField className="min-w-0 flex-1" value={props.value} onChange={props.onChange} placeholder={props.placeholder} aria-label={props.label} />
			<span className="w-24 shrink-0">
				<SelectField value={PICK} onChange={(v) => (v === PICK ? undefined : props.onChange(v))} options={[{ value: PICK, label: "常用值" }, ...props.presets.map((p) => ({ value: p, label: p }))]} />
			</span>
		</span>
	);
}

/** 自动 + 枚举的下拉, 空串选项写成 auto 哨兵 (radix Select 不接受空值). */
function Choice(props: { value: string; onChange: (v: string) => void; auto: string; options: string[][] }): JSX.Element {
	return (
		<SelectField value={props.value || "auto"} onChange={(v) => props.onChange(v === "auto" ? "" : v)} options={[{ value: "auto", label: props.auto }, ...props.options.map(([value, label]) => ({ value, label }))]} />
	);
}

/** 开关 + 一句话说明. */
function Toggle(props: { label: string; hint: string; checked: boolean; onChange: (v: boolean) => void }): JSX.Element {
	return (
		<label className="col-span-2 flex items-start justify-between gap-3 rounded-md border border-border px-3 py-2">
			<span className="flex flex-col gap-1">
				<span className="t-aux text-foreground">{props.label}</span>
				<span className="t-aux text-muted-foreground">{props.hint}</span>
			</span>
			<Switch checked={props.checked} onCheckedChange={props.onChange} aria-label={props.label} />
		</label>
	);
}

const GRID = "grid grid-cols-2 gap-x-3 gap-y-3";

export function BasicSection({ form, set, folders }: SectionProps & { folders: string[] }): JSX.Element {
	return (
		<div className={GRID}>
			<F label="名称" auto="Env-N">
				<InputField value={form.name} onChange={(name) => set({ name })} autoFocus aria-label="名称" placeholder="例如 github-reg-12" />
			</F>
			<F label="平台 (操作系统)" auto={AUTO.os}>
				<Choice value={form.os} onChange={(os) => set({ os })} auto={"自动 (" + AUTO.os + ")"} options={[["Windows", "Windows"], ["macOS", "macOS"], ["Linux", "Linux"]]} />
			</F>
			<F label="文件夹">
				<SelectField value={form.folder || ROOT} onChange={(f) => set({ folder: f === ROOT ? "" : f })} options={[{ value: ROOT, label: "根目录" }, ...folders.map((f) => ({ value: f, label: f }))]} />
			</F>
			<F label="标签" auto="" hint="逗号或空格分隔">
				<InputField value={form.tags} onChange={(tags) => set({ tags })} aria-label="标签" placeholder="reg, us" />
			</F>
			<F label="代理" auto="直连" hint="http, https 或 socks5 链接" wide>
				<InputField value={form.proxyLink} onChange={(proxyLink) => set({ proxyLink })} aria-label="代理" placeholder="socks5://user:pass@host:port" />
			</F>
			<F label="备注" auto="" wide>
				<Textarea value={form.notes} onChange={(e) => set({ notes: e.target.value })} rows={2} aria-label="备注" />
			</F>
		</div>
	);
}

export function DeviceSection({ form, set, kernels, lockFamily }: SectionProps & { kernels: Kernel[]; lockFamily?: "blink" | "gecko" }): JSX.Element {
	return (
		<div className={GRID}>
			<F label={lockFamily ? "内核 (同族内切换, 换族需新建)" : "内核"} wide>
				<KernelPicker kernels={kernels} value={form.kernel} onChange={(kernel) => set({ kernel })} lockFamily={lockFamily} />
			</F>
			<F label="设备类型" auto={AUTO.device_type}>
				<Choice value={form.device_type} onChange={(device_type) => set({ device_type })} auto="自动 (桌面)" options={[["desktop", "桌面"], ["mobile", "手机"], ["tablet", "平板"]]} />
			</F>
			<F label="分辨率" auto={AUTO.resolution}>
				<Preset value={form.resolution} onChange={(resolution) => set({ resolution })} presets={PRESETS.resolution} placeholder="1920x1080" label="分辨率" />
			</F>
			<F label="色彩深度" auto={AUTO.color_depth}>
				<Choice value={form.color_depth} onChange={(color_depth) => set({ color_depth })} auto="自动 (24)" options={[["24", "24"], ["30", "30"], ["32", "32"]]} />
			</F>
			<F label="硬件并发 (CPU 线程)" auto="跟随本机">
				<InputField type="number" value={form.hardware_concurrency} onChange={(hardware_concurrency) => set({ hardware_concurrency })} aria-label="硬件并发" placeholder="8" />
			</F>
			<F label="设备内存 (GB)" auto="跟随本机">
				<InputField type="number" value={form.device_memory} onChange={(device_memory) => set({ device_memory })} aria-label="设备内存" placeholder="8" />
			</F>
		</div>
	);
}

const NOISE = "改的是看起来像谁, 不影响出口 IP 与代理";

export function FingerprintSection({ form, set }: SectionProps): JSX.Element {
	return (
		<div className={GRID}>
			<F label="User-Agent" auto="跟随内核" wide>
				<InputField value={form.user_agent} onChange={(user_agent) => set({ user_agent })} aria-label="User-Agent" placeholder="留空 = 用内核自己的 UA" />
			</F>
			<F label="语言" auto="按出口 IP">
				<Preset value={form.language} onChange={(language) => set({ language })} presets={PRESETS.language} placeholder="en-US" label="语言" />
			</F>
			<F label="时区" auto="按出口 IP">
				<Preset value={form.timezone} onChange={(timezone) => set({ timezone })} presets={PRESETS.timezone} placeholder="America/New_York" label="时区" />
			</F>
			<F label="WebGL vendor" auto="跟随本机">
				<InputField value={form.webgl_vendor} onChange={(webgl_vendor) => set({ webgl_vendor })} aria-label="WebGL vendor" placeholder="Google Inc. (NVIDIA)" />
			</F>
			<F label="WebGL renderer" auto="跟随本机">
				<InputField value={form.webgl_renderer} onChange={(webgl_renderer) => set({ webgl_renderer })} aria-label="WebGL renderer" placeholder="ANGLE (NVIDIA ...)" />
			</F>
			<Toggle label="Canvas 噪声" hint={"给画布像素读数加微小扰动, " + NOISE} checked={form.canvas_noise} onChange={(canvas_noise) => set({ canvas_noise })} />
			<Toggle label="Audio 噪声" hint={"给 AudioContext 采样加微小扰动, " + NOISE} checked={form.audio_noise} onChange={(audio_noise) => set({ audio_noise })} />
			<Toggle label="WebGL 噪声" hint={"给 WebGL 读回像素与参数加扰动, " + NOISE} checked={form.webgl_noise} onChange={(webgl_noise) => set({ webgl_noise })} />
			<F label="WebRTC" auto="真实">
				<Choice value={form.webrtc_mode} onChange={(webrtc_mode) => set({ webrtc_mode })} auto="自动 (真实)" options={[["real", "真实"], ["proxy", "替换为代理 IP"], ["disable", "禁用"]]} />
			</F>
			<F label="WebRTC 公网 IP" auto="代理出口" hint="仅 替换为代理 IP 时生效">
				<InputField value={form.webrtc_public_ip} onChange={(webrtc_public_ip) => set({ webrtc_public_ip })} aria-label="WebRTC 公网 IP" placeholder="203.0.113.7" />
			</F>
		</div>
	);
}

export function AdvancedSection({ form, set }: SectionProps): JSX.Element {
	return (
		<div className={GRID}>
			<F label="启动 URL" auto="空白页" hint="每行一个" wide>
				<Textarea value={form.urls} onChange={(e) => set({ urls: e.target.value })} rows={3} aria-label="启动 URL" placeholder="https://github.com/login" />
			</F>
			<F label="额外启动参数" auto="" hint="每行一个, 原样传给浏览器" wide>
				<Textarea value={form.extra_args} onChange={(e) => set({ extra_args: e.target.value })} rows={2} aria-label="额外启动参数" placeholder="--disable-gpu" className="font-mono" />
			</F>
			<F label="TLS 模式" auto={AUTO.tls_mode}>
				<Choice value={form.tls_mode} onChange={(tls_mode) => set({ tls_mode })} auto="自动 (不处理)" options={[["none", "不处理"], ["chrome", "Chrome 指纹 (tls-proxy)"]]} />
			</F>
			<F label="暗色主题" auto="跟随全局">
				<Choice value={form.dark_mode} onChange={(dark_mode) => set({ dark_mode })} auto="跟随全局设置" options={[["on", "强制暗色"], ["off", "强制亮色"]]} />
			</F>
			<Toggle label="无头运行 (headless)" hint="不开窗口, 与全局 headless 设置取或; 需要人工登录时别开" checked={form.headless} onChange={(headless) => set({ headless })} />
		</div>
	);
}
