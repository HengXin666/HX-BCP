/**
 * 内核管理 #/system/kernels: 每个内核一张卡, 看得清 "它是什么族, 用哪个文件, 这个路径从哪来, 有几个环境在用, 能做什么".
 * 读 GET /kernels (kernelsDetail), 写 PUT /kernels/{id}/path 与 /default, 检测 POST /kernels/{id}/probe.
 * 判据全在后端 kernels.py; 这里只展示与转发. Safarier 的 "路径" 是一条命令, 可以是远程 ssh; 本机 WebKit 的路径是 Playwright 的 pw_run.sh.
 */
import { useEffect, useState, type JSX } from "react";
import { Check, FlaskConical, RotateCw, Save, Star, X } from "lucide-react";
import { Badge, Button, Card, CardContent, EmptyState, InputField, toast } from "@hx/ui";
import { PageLoading } from "../PageLoading";
import { usePoll } from "../../lib/hooks";
import { FAMILIES, FAMILY, FEATURE_LABEL, kernelApi, kernelsDetail, missingText, platformTag, type Kernel, type KernelFeatures, type ProbeResult } from "../../lib/kernels";

const SOURCE: Record<string, string> = { configured: "自定义路径", detected: "自动探测", missing: "未找到" };
const PLACEHOLDER: Record<string, string> = {
	webkit: "留空 = 用 Playwright 安装的 WebKit 构建, 或填 pw_run.sh 绝对路径",
	safarier: "/Applications/Safarier.app/Contents/MacOS/Safarier 或 ssh my-mac /Applications/Safarier.app/Contents/MacOS/Safarier",
};

function Features({ f }: { f: KernelFeatures | undefined }): JSX.Element | null {
	if (!f) return null;
	return (
		<div className="flex flex-wrap gap-1" data-kernel-features="">
			{(Object.keys(FEATURE_LABEL) as (keyof KernelFeatures)[]).filter((k) => k in f).map((k) => (
				<span key={k} className={"inline-flex items-center gap-1 rounded border px-1.5 t-micro " + (f[k] ? "border-border text-foreground" : "border-dashed border-border/60 text-muted-foreground line-through")}>
					{f[k] ? <Check className="size-3" /> : <X className="size-3" />}
					{FEATURE_LABEL[k]}
				</span>
			))}
		</div>
	);
}

function probeLine(p: ProbeResult): string {
	if (!p.ok) return "检测失败: " + (p.error || "未知原因");
	if (p.version !== undefined) return "可运行: " + (p.version || "未输出版本");
	return "已连通: " + (p.profiles ?? 0) + " 个 profile, " + (p.running ?? 0) + " 个在跑";
}

function KernelRow(props: { k: Kernel; onChanged: () => void }): JSX.Element {
	const { k } = props;
	const [path, setPath] = useState(k.configured ?? "");
	const [busy, setBusy] = useState("");
	const [probe, setProbe] = useState<ProbeResult | null>(null);
	useEffect(() => setPath(k.configured ?? ""), [k.configured]);
	const dirty = path.trim() !== (k.configured ?? "");

	const save = async (value: string): Promise<void> => {
		setBusy("save");
		const r = await kernelApi.setPath(k.id, value);
		setBusy("");
		if (!r.ok) return void toast.error("保存失败: " + r.error);
		toast.success(value ? k.label + " 路径已保存" + (r.data.available ? "" : ", 但还不可用") : k.label + " 回到自动探测");
		setProbe(null);
		props.onChanged();
	};
	const runProbe = async (): Promise<void> => {
		setBusy("probe");
		const r = await kernelApi.probe(k.id);
		setBusy("");
		setProbe(r.ok ? r.data : { id: k.id, ok: false, error: r.error });
	};
	const makeDefault = async (): Promise<void> => {
		setBusy("default");
		const r = await kernelApi.setDefault(k.id);
		setBusy("");
		if (r.ok) (toast.success("新建环境默认用 " + k.label), props.onChanged());
		else toast.error(r.error);
	};

	return (
		<Card variant="solid" data-kernel-row={k.id} data-available={String(k.available)}>
			<CardContent className="flex flex-col gap-3 p-4">
				<div className="flex flex-wrap items-center gap-2">
					<span className={"size-2.5 rounded-full " + FAMILY[k.family].dot} />
					<span className="t-card">{k.label}</span>
					{k.available ? <Badge variant="success">可用</Badge> : <Badge variant={k.source === "configured" ? "destructive" : "secondary"}>{k.source === "configured" ? "路径无效" : k.host_supported === false ? missingText(k) : "未找到"}</Badge>}
					{platformTag(k) ? <Badge variant="warning" data-kernel-platform={k.id}>{platformTag(k)}</Badge> : null}
					<Badge variant="outline">{SOURCE[k.source ?? ""] ?? "未上报"}</Badge>
					{k.mode === "external" ? <Badge variant="warning">外部应用</Badge> : k.mode === "runner" ? <Badge variant="outline">本机 runner</Badge> : null}
					{k.default ? <Badge variant="default"><Star className="size-3" />默认</Badge> : null}
					<span className="ml-auto t-aux tabular-nums text-muted-foreground">{(k.envs ?? 0) + " 个环境在用"}</span>
				</div>
				<div className="flex min-w-0 flex-col gap-1">
					<span className="t-aux text-muted-foreground">{"生效: "}<span className="font-mono text-foreground" title={k.path}>{k.path || "无"}</span></span>
					{k.note ? <span className="t-aux text-warning-fg">{k.note}</span> : null}
				</div>
				<div className="flex flex-col gap-2 sm:flex-row sm:items-center">
					<InputField className="min-w-0 flex-1 font-mono" value={path} onChange={setPath} placeholder={PLACEHOLDER[k.id] ?? "留空 = 自动探测, 或填可执行文件绝对路径"} aria-label={k.label + " 路径"} onKeyDown={(e) => (e.key === "Enter" && dirty ? void save(path.trim()) : undefined)} />
					<div className="flex shrink-0 gap-2">
						<Button size="sm" disabled={!dirty || busy !== ""} onClick={() => void save(path.trim())}><Save className="size-4" />保存</Button>
						{k.configured ? <Button size="sm" variant="ghost" disabled={busy !== ""} onClick={() => void save("")}>清除</Button> : null}
						<Button size="sm" variant="outline" disabled={busy !== ""} onClick={() => void runProbe()}><FlaskConical className="size-4" />{busy === "probe" ? "检测中" : "检测"}</Button>
						<Button size="sm" variant="outline" disabled={!k.available || !!k.default || busy !== ""} onClick={() => void makeDefault()}><Star className="size-4" />设为默认</Button>
					</div>
				</div>
				{probe ? <span className={"t-aux " + (probe.ok ? "text-success-fg" : "text-destructive")} data-kernel-probe={String(probe.ok)}>{probeLine(probe)}</span> : null}
				<Features f={k.features} />
			</CardContent>
		</Card>
	);
}

export function KernelsPage(): JSX.Element {
	const res = usePoll(kernelsDetail, 30000);
	if (res.result && !res.result.ok) return <EmptyState text="未连接" hint={"GET /api/v1/kernels 失败: " + res.result.error} />;
	if (!res.result) return <PageLoading />;
	const { kernels, inherit } = res.result.data;
	const usable = kernels.filter((k) => k.available).length;
	const def = kernels.find((k) => k.default);
	return (
		<div className="flex min-w-0 flex-col gap-4" data-sys-page="kernels">
			<div className="flex flex-wrap items-center gap-x-4 gap-y-1">
				<span className="t-body tabular-nums">{"可用 " + usable + " / 共 " + kernels.length + " 个内核"}</span>
				<span className="t-aux text-muted-foreground">{"新建默认: " + (def ? def.label : "无可用内核")}</span>
				<span className="t-aux text-muted-foreground" title={"Chromium: " + (inherit.blink || "未设") + "\nFirefox: " + (inherit.gecko || "未设")}>{"未指定内核的旧环境 " + inherit.envs + " 个, 继承设置页的两个路径"}</span>
				<Button size="sm" variant="outline" className="ml-auto" onClick={res.reload}><RotateCw className="size-4" />重新探测</Button>
			</div>
			{FAMILIES.map((fam) => {
				const list = kernels.filter((k) => k.family === fam);
				if (!list.length) return null;
				return (
					<section key={fam} className="flex min-w-0 flex-col gap-2" data-kernel-family={fam}>
						<h3 className="flex items-center gap-2 t-card">
							<span className={"size-2 rounded-full " + FAMILY[fam].dot} />
							{FAMILY[fam].name}
							<span className="t-aux font-normal text-muted-foreground">{list[0].how ?? FAMILY[fam].how}</span>
						</h3>
						<div className="grid min-w-0 gap-3 xl:grid-cols-2">
							{list.map((k) => <KernelRow key={k.id} k={k} onChanged={res.reload} />)}
						</div>
					</section>
				);
			})}
		</div>
	);
}
