/**
 * 设置 #/system/settings: GET /settings 读, PUT /settings 只发改过的字段 (后端按字段合并).
 * 只读展示: data_dir (改它等于换数据目录, 不在这里做), api_port (后端 PUT 不收这个字段, 改端口要改启动参数后重启).
 * api_key 不回显: 只显示是否已设置与位数, 输入框留空表示不改. 内核路径改完对之后启动的浏览器生效.
 */
import { useEffect, useState, type JSX, type ReactNode } from "react";
import { Save } from "lucide-react";
import { Button, EmptyState, InputField, SettingSection, Switch, toast } from "@hx/ui";
import { systemApi, type Settings } from "../../lib/api-res";
import { usePoll } from "../../lib/hooks";

type Draft = Omit<Settings, "api_key"> & { api_key: string };

const TEXT: { key: "chromium_path" | "firefox_path" | "default_homepage"; label: string; hint: string }[] = [
	{ key: "chromium_path", label: "Chromium 路径", hint: "留空则自动探测; 单个内核的路径在状态页内核卡里设" },
	{ key: "firefox_path", label: "Firefox 路径", hint: "Firefox 系内核 (flower 等) 的默认可执行文件" },
	{ key: "default_homepage", label: "默认首页", hint: "环境没有设启动页时打开" },
];
const FLAGS: { key: "proxy_check_before_launch" | "headless_default" | "auto_match_timezone" | "dark_mode"; label: string }[] = [
	{ key: "proxy_check_before_launch", label: "启动前检测代理" },
	{ key: "headless_default", label: "默认不开窗口 (与环境自己的设置取或)" },
	{ key: "auto_match_timezone", label: "按出口 IP 自动对齐时区" },
	{ key: "dark_mode", label: "浏览器默认暗色主题" },
];

function Field(props: { label: string; hint?: string; children: ReactNode }): JSX.Element {
	return (
		<div className="grid gap-1 sm:grid-cols-[10rem_minmax(0,1fr)] sm:items-center sm:gap-3">
			<span className="t-body">{props.label}</span>
			<div className="flex min-w-0 flex-col gap-1">
				{props.children}
				{props.hint ? <span className="t-aux text-muted-foreground">{props.hint}</span> : null}
			</div>
		</div>
	);
}

export function SettingsPage(): JSX.Element {
	const res = usePoll(systemApi.settings, 3600000);
	const base = res.result?.ok ? res.result.data : null;
	const [d, setD] = useState<Draft | null>(null);
	const [busy, setBusy] = useState(false);
	useEffect(() => setD(base ? { ...base, api_key: "" } : null), [base]);

	if (res.result && !res.result.ok) return <EmptyState text="未连接" hint={"GET /api/v1/settings 失败: " + res.result.error} />;
	if (!base || !d) return <EmptyState text="加载中" />;
	const set = <K extends keyof Draft>(k: K, v: Draft[K]): void => setD({ ...d, [k]: v });
	const patch: Partial<Settings> = {};
	for (const k of Object.keys(d) as (keyof Draft)[]) {
		if (k === "api_key" || k === "api_port" || k === "data_dir") continue;
		if (d[k] !== base[k]) (patch as Record<string, unknown>)[k] = d[k];
	}
	if (d.api_key) patch.api_key = d.api_key;
	const dirty = Object.keys(patch).length;

	const save = async (): Promise<void> => {
		setBusy(true);
		const r = await systemApi.saveSettings(patch);
		setBusy(false);
		if (r.ok) (toast.success("设置已保存 (" + dirty + " 项)"), res.reload());
		else toast.error("保存失败: " + r.error);
	};

	return (
		<div className="flex flex-col gap-4" data-sys-page="settings">
			<SettingSection section={{ id: "paths", title: "路径与端口" }}>
				<div className="flex flex-col gap-3">
					{TEXT.map((f) => (
						<Field key={f.key} label={f.label} hint={f.hint}>
							<InputField className="font-mono" value={d[f.key]} onChange={(v) => set(f.key, v)} aria-label={f.label} />
						</Field>
					))}
					<Field label="CDP 起始端口" hint="每个环境的调试口从这里往上分配">
						<InputField value={String(d.cdp_base_port)} onChange={(v) => set("cdp_base_port", Number(v.replace(/\D/g, "")) || 0)} aria-label="CDP 起始端口" />
					</Field>
					<Field label="API 端口" hint="只读: 后端设置接口不改端口, 改启动参数后重启服务">
						<InputField value={String(base.api_port)} onChange={() => undefined} disabled aria-label="API 端口" />
					</Field>
					<Field label="数据目录" hint="只读: profile, 日志, 资源库都在这里">
						<InputField className="font-mono" value={base.data_dir} onChange={() => undefined} disabled aria-label="数据目录" />
					</Field>
					<Field label="API 密钥" hint={base.api_key ? "已设置 (" + base.api_key.length + " 位), 不回显; 留空不改" : "未设置; 设置后 api 通道请求需带此密钥"}>
						<InputField type="password" value={d.api_key} onChange={(v) => set("api_key", v)} placeholder={base.api_key ? "留空不改" : "输入新密钥"} aria-label="API 密钥" />
					</Field>
				</div>
			</SettingSection>
			<SettingSection section={{ id: "defaults", title: "默认行为" }}>
				<div className="flex flex-col gap-2">
					{FLAGS.map((f) => (
						<label key={f.key} className="flex items-center gap-2 t-body">
							<Switch checked={d[f.key]} onCheckedChange={(v) => set(f.key, v)} />
							{f.label}
						</label>
					))}
				</div>
			</SettingSection>
			<div className="flex items-center gap-2">
				<Button disabled={!dirty || busy} onClick={() => void save()}><Save className="size-4" />{busy ? "保存中" : dirty ? "保存 " + dirty + " 项" : "没有改动"}</Button>
				{dirty ? <Button variant="ghost" onClick={() => setD({ ...base, api_key: "" })}>还原</Button> : null}
			</div>
		</div>
	);
}
