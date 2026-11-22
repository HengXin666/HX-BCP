/**
 * 环境详情抽屉: 右侧滑出, 约 520px 宽, 内容在抽屉内滚动. 非模态不带遮罩, 列表仍可滚可点, 点另一行直接换成那一行.
 * 顶部是常驻操作条 (启停 / 重启 / 窗口前置 / 复制 / 编辑 / 删除), 下面六页: 概览, 运维, 指纹, Cookies, 缓存, 账号包.
 * 运维页集中 opsApi 的六项: 进程退出检测, 重启, 窗口前置, 复制, 出口检测, 体检与修复, Cookie 注入.
 * 进程退出检测在抽屉层常驻 (看列表刷新的 env.runtime, 运维页打开时再加 5 秒状态轮询): 运行中变未运行即记一次退出,
 * 经本抽屉停止或重启的 30 秒内算主动, 其余算意外退出, 弹提示并让列表刷新.
 * 打开哪个环境由 BrowsersPage 决定 (hash 的 env=), 命令面板的 "打开环境" 也落在这里.
 * API 组 (lane=agent) 或 readonly 环境只读: 写按钮 disabled 带提示; 窗口前置与出口检测不改配置, 只读也可用.
 * 新能力全部走 lib/api-ops 的 opsApi, 出口检测走 lib/hooks 的 useExitChecks (全站共用结果), 不另起请求层.
 */
import { useCallback, useEffect, useMemo, useRef, useState, type JSX } from "react";
import { Activity, AppWindow, Copy, Lock, Pencil, Play, RotateCw, Route, Square, Stethoscope, Trash2, Upload, Wifi } from "lucide-react";
import {
	Badge,
	Button,
	Checkbox,
	Sheet,
	SheetContent,
	SheetDescription,
	SheetHeader,
	SheetTitle,
	Spinner,
	Tabs,
	TabsContent,
	TabsList,
	TabsTrigger,
	Textarea,
	toast,
} from "@hx/ui";
import { envStatus, type Env, type Result } from "../../lib/api";
import { opsApi, type DoctorCheck, type DoctorReport } from "../../lib/api-ops";
import { STATE_TEXT, upstreamKey, type Seg } from "../../lib/chain";
import { usePoll, type ExitChecks } from "../../lib/hooks";
import { loggedIn, useLogin, type EnvLogin } from "../../lib/login";
import { KV } from "./ChainParts";
import { CookiesTab, FingerprintTab } from "./DetailFp";
import { CacheTab, PackTab } from "./DetailOps";
import { RO_HINT, Section, WriteButton, readOnly, ts } from "./DetailParts";
import type { EnvAction } from "./EnvTable";
import { proxyLabel, whereLabel } from "./EnvCells";

function rtv(env: Env, k: string): string | number | null {
	const v = env.runtime?.[k];
	return typeof v === "string" || typeof v === "number" ? (v === "" || v === 0 ? null : v) : null;
}

const LOGIN_TEXT: Record<string, string> = { ok: "已登录", expired: "已过期", empty: "无会话" };

/** 登录态逐站一行: 站点 -> 状态 (带账号身份); 接口没返回或这个环境没有记录写 "未上报, 环境未运行"; 最后一行写数据来源. */
function loginRows(login: EnvLogin | null | undefined, running: boolean): [string, string | null][] {
	const plats = Object.entries(login?.platforms ?? {}).sort(([, a], [, b]) => (a.state === "ok" ? 0 : 1) - (b.state === "ok" ? 0 : 1));
	if (!login || !plats.length) return [["站点", login === null ? "未上报, 接口未返回" : running ? "未检出登录会话" : "未上报, 环境未运行"]];
	return [
		...plats.map(([k, v]) => [k, (LOGIN_TEXT[v.state] ?? v.state) + (v.identity ? " (" + v.identity + ")" : "")] as [string, string]),
		["来源", login.running ? "运行中, CDP 实测" : "离线, 读 cookie 库"],
	];
}

/** 概览: 链路四段摘要 (点按钮开链路抽屉) + 出口检测 + 登录态 + 运行时 (GET /status, 5 秒轮询) + 环境基础字段. */
function OverviewTab(props: { env: Env; segs: Seg[]; onChain: () => void; checks: ExitChecks }): JSX.Element {
	const id = props.env.id;
	const live = usePoll(useCallback((s: AbortSignal) => envStatus(id, s), [id]));
	const env = live.result?.ok ? live.result.data : props.env;
	const running = !!env.runtime;
	const fp = env.fingerprint ?? {};
	const login = useLogin(id);
	const sites = loggedIn(login);
	return (
		<div className="flex flex-col gap-3">
			<Section title="链路" right={<Button size="xs" variant="outline" onClick={props.onChain} data-open-chain=""><Route className="size-4" />链路详情</Button>}>
				<KV rows={props.segs.map((s) => [s.name, STATE_TEXT[s.state] + ": " + s.value] as [string, string])} />
			</Section>
			<ExitSection env={env} checks={props.checks} />
			<Section title="登录态" right={<Badge variant={sites.length ? "success" : "secondary"} data-login-count={login ? sites.length : ""}>{login ? sites.length + " 个已登录" : "未上报"}</Badge>}>
				<KV rows={loginRows(login, running)} />
			</Section>
			<Section title="运行时" right={<Badge variant={running ? "success" : "secondary"}>{running ? "运行中" : "未运行"}</Badge>}>
				<KV
					rows={[
						["状态接口", live.result && !live.result.ok ? "不可达: " + live.result.error : running ? "运行中" : "未运行"],
						["PID", rtv(env, "pid")],
						["调试口", rtv(env, "debug_port")],
						["实际内核", rtv(env, "kernel")],
						["配置内核", fp.kernel || fp.browser || null],
						["窗口", running ? (env.visible ? env.window_title || "可见 (无标题)" : env.visible_reason || null) : null],
						["显示", rtv(env, "display_source")],
						["隔离", rtv(env, "isolation")],
						["启动于", ts(Number(rtv(env, "started_at") ?? 0))],
						["待重启", running ? (env.pending_restart ? "是, 配置已改" : "否") : null],
					]}
				/>
			</Section>
			<Section title="基础">
				<KV
					rows={[
						["ID", env.id],
						["通道", env.lane === "agent" ? "API 组" + (env.project ? " / " + env.project : "") : "人类组"],
						["创建时间", ts(env.created_at)],
						["打开次数", env.open_count ?? null],
						["最近打开", ts(env.last_open_at)],
						["位置", whereLabel(env)],
						["代理", proxyLabel(env)],
						["出口对齐", env.last_region?.ip ? env.last_region.ip + " " + (env.last_region.country ?? "") : null],
						["标签", (env.tags ?? []).join(", ") || null],
						["备注", env.notes || null],
					]}
				/>
			</Section>
		</div>
	);
}

/** 出口检测: 读全站共用的结果, 按钮重测这个环境的上游. 直连环境没有上游可测. */
function ExitSection(props: { env: Env; checks: ExitChecks }): JSX.Element {
	const key = upstreamKey(props.env.proxy);
	const r = key ? props.checks.results[key] : undefined;
	const testing = key ? props.checks.pending.has(key) : false;
	const badge = !key ? <Badge variant="secondary">直连</Badge> : testing ? <Badge variant="secondary">检测中</Badge> : !r ? <Badge variant="outline">未检测</Badge> : r.ok ? <Badge variant="success">通</Badge> : <Badge variant="destructive">不通</Badge>;
	const run = (): void => void props.checks.run([{ key, proxy: props.env.proxy ?? null }]);
	return (
		<Section
			title="出口检测"
			right={
				<>
					{badge}
					<Button size="xs" variant="outline" disabled={!key || testing} onClick={run} data-exit-check="">
						{testing ? <Spinner /> : <Wifi className="size-4" />}重测
					</Button>
				</>
			}
		>
			<KV
				rows={
					!key
						? [["上游", "未挂上游 (直连或系统代理), 无需检测"]]
						: [
								["上游", proxyLabel(props.env)],
								["出口 IP", r?.ip ?? null],
								["地区", r ? [r.country, r.region, r.city].filter(Boolean).join(" / ") || null : null],
								["运营商", r ? [r.isp, r.asn].filter(Boolean).join(" ") || null : null],
								["延迟", r?.latency_ms != null ? r.latency_ms + " ms" : null],
								["错误", r && !r.ok ? r.error || "不通" : null],
							]
				}
			/>
		</Section>
	);
}

const CHECK_TONE: Record<string, "success" | "warning" | "destructive" | "secondary"> = {
	ok: "success",
	pass: "success",
	green: "success",
	warn: "warning",
	warning: "warning",
	yellow: "warning",
	fail: "destructive",
	error: "destructive",
	red: "destructive",
};

/**
 * 体检: 按需跑 (要测网络, 不轮询). 列出每一项的状态, 值与说明; 可修复的项能勾选后一键修复,
 * 修完自动复检并通知列表刷新. 只读环境可以体检, 不能修复.
 */
function DoctorTab(props: { env: Env; ro: boolean; onChanged: () => void }): JSX.Element {
	const id = props.env.id;
	const [report, setReport] = useState<Result<DoctorReport> | null>(null);
	const [running, setRunning] = useState(false);
	const [fixing, setFixing] = useState(false);
	const [picked, setPicked] = useState<Set<string>>(new Set());

	const run = async (): Promise<void> => {
		setRunning(true);
		const r = await opsApi.doctor(id);
		setRunning(false);
		setReport(r);
		setPicked(new Set(r.ok ? (r.data?.checks ?? []).filter((c) => c.fixable && !isOk(c)).map((c) => c.id) : []));
	};
	const fix = async (): Promise<void> => {
		setFixing(true);
		const r = await opsApi.fix(id, [...picked]);
		setFixing(false);
		if (!r.ok) return void toast.error("修复失败: " + r.error);
		toast.success("已修复 " + picked.size + " 项, 正在复检");
		props.onChanged();
		await run();
	};

	const checks: DoctorCheck[] = report?.ok ? report.data?.checks ?? [] : [];
	const bad = checks.filter((c) => !isOk(c)).length;
	const toggle = (cid: string, v: boolean): void =>
		setPicked((p) => {
			const n = new Set(p);
			v ? n.add(cid) : n.delete(cid);
			return n;
		});

	return (
		<div className="flex flex-col gap-3">
			<Section
				title="体检"
				right={
					<>
						{report?.ok ? <Badge variant={bad ? "warning" : "success"}>{bad ? bad + " 项异常" : "全部通过"}</Badge> : null}
						<Button size="sm" variant="outline" disabled={running} onClick={() => void run()} data-doctor-run="">
							{running ? <Spinner /> : <Stethoscope className="size-4" />}
							{report ? "重新体检" : "开始体检"}
						</Button>
					</>
				}
			>
				{!report ? (
					<p className="t-aux text-muted-foreground">检查代理, 出口与时区/语言对齐, 指纹一致性等, 需要访问网络, 约十几秒.</p>
				) : !report.ok ? (
					<p className="t-aux text-destructive">体检失败: {report.error}</p>
				) : (
					<ul className="flex flex-col divide-y divide-border/60" data-doctor-list="">
						{checks.map((c) => (
							<li key={c.id} className="flex items-start gap-2 py-2" data-doctor-check={c.id}>
								<span className="flex h-5 w-4 items-center">
									{c.fixable && !isOk(c) ? <Checkbox checked={picked.has(c.id)} disabled={props.ro} onCheckedChange={(v) => toggle(c.id, v === true)} aria-label={"修复 " + c.id} /> : null}
								</span>
								<div className="flex min-w-0 flex-1 flex-col gap-0.5">
									<div className="flex items-center gap-2">
										<span className="font-mono t-aux">{c.id}</span>
										<Badge variant={CHECK_TONE[c.status] ?? "secondary"} className="ml-auto shrink-0">{c.status}</Badge>
									</div>
									{c.value ? <span className="truncate" title={c.value}>{c.value}</span> : null}
									{c.detail ? <span className="t-aux text-muted-foreground">{c.detail}</span> : null}
								</div>
							</li>
						))}
					</ul>
				)}
				{report?.ok && checks.some((c) => c.fixable && !isOk(c)) ? (
					<div className="flex items-center justify-end gap-2 border-t border-border/60 pt-2">
						<span className="t-aux text-muted-foreground">修复会改写配置, 运行中的环境重启后生效</span>
						<WriteButton ro={props.ro} busy={fixing || picked.size === 0} onClick={() => void fix()}>
							{fixing ? <Spinner /> : null}修复选中 {picked.size || ""}
						</WriteButton>
					</div>
				) : null}
			</Section>
		</div>
	);
}

function isOk(c: DoctorCheck): boolean {
	return CHECK_TONE[c.status] === "success";
}

/** 从粘贴或文件读 cookie: 接受数组, {cookies: [...]}, 或浏览器扩展导出的同形 JSON. */
function parseCookies(text: string): unknown[] | string {
	try {
		const v: unknown = JSON.parse(text);
		const list = Array.isArray(v) ? v : v && typeof v === "object" && Array.isArray((v as { cookies?: unknown }).cookies) ? (v as { cookies: unknown[] }).cookies : null;
		if (!list) return "需要 cookie 数组, 或带 cookies 字段的对象";
		if (!list.length) return "列表是空的";
		return list;
	} catch (e) {
		return "不是合法 JSON: " + (e instanceof Error ? e.message : String(e));
	}
}

/** Cookie 注入: 粘贴 JSON 或选文件, 注入到在跑的浏览器. 只读环境不可注入. */
function InjectCookies(props: { env: Env; ro: boolean; onChanged: () => void }): JSX.Element {
	const [text, setText] = useState("");
	const [busy, setBusy] = useState(false);
	const running = !!props.env.runtime;
	const parsed = text.trim() ? parseCookies(text) : null;
	const err = typeof parsed === "string" ? parsed : "";
	const onFile = (f: File | undefined): void => void f?.text().then(setText);
	const go = async (): Promise<void> => {
		if (!Array.isArray(parsed)) return;
		setBusy(true);
		const r = await opsApi.injectCookies(props.env.id, parsed);
		setBusy(false);
		if (!r.ok) return void toast.error("注入失败: " + r.error);
		toast.success("已注入 " + (r.data?.injected ?? parsed.length) + " 条 cookie");
		setText("");
		props.onChanged();
	};
	return (
		<Section
			title="注入 Cookie"
			right={
				<label className={"inline-flex cursor-pointer items-center gap-1 t-aux text-muted-foreground hover:text-foreground" + (props.ro ? " pointer-events-none opacity-50" : "")}>
					<Upload className="size-4" />选文件
					<input type="file" accept=".json,application/json" className="hidden" disabled={props.ro} onChange={(e) => onFile(e.target.files?.[0])} />
				</label>
			}
		>
			<Textarea
				rows={5}
				className="font-mono t-aux"
				placeholder={'[{"name":"sid","value":"…","domain":".example.com","path":"/"}]'}
				value={text}
				disabled={props.ro}
				onChange={(e) => setText(e.target.value)}
				data-cookie-inject=""
			/>
			<div className="flex items-center gap-2">
				<span className={"t-aux " + (err ? "text-destructive" : "text-muted-foreground")}>
					{err || (Array.isArray(parsed) ? parsed.length + " 条待注入" : running ? "粘贴 JSON 或选文件" : "环境未运行, 注入需要先启动")}
				</span>
				<span className="ml-auto">
					<WriteButton ro={props.ro} busy={busy || !Array.isArray(parsed) || !running} onClick={() => void go()} variant="default">
						{busy ? <Spinner /> : null}注入
					</WriteButton>
				</span>
			</div>
		</Section>
	);
}

/** 进程退出记录: 每个环境最近一次从运行中变为未运行的时间, expected 表示是经本抽屉停止或重启引起的. */
type ExitMark = { ms: number; expected: boolean };
type ExitWatch = {
	at: Record<string, ExitMark>;
	observe: (id: string, running: boolean, name: string) => void;
	expect: (id: string) => void;
};

/** 进程退出检测: observe 喂当前运行态 (同一环境前后两次比较, 换环境只记基线), expect 标记接下来 30 秒内的退出是主动的. */
function useExitWatch(onExit: (id: string, name: string, expected: boolean) => void): ExitWatch {
	const prev = useRef<{ id: string; running: boolean } | null>(null);
	const expected = useRef(new Map<string, number>());
	const cb = useRef(onExit);
	cb.current = onExit;
	const [at, setAt] = useState<Record<string, ExitMark>>({});
	const observe = useCallback((id: string, running: boolean, name: string): void => {
		const p = prev.current;
		prev.current = { id, running };
		if (!p || p.id !== id || !p.running || running) return;
		const exp = (expected.current.get(id) ?? 0) > Date.now();
		expected.current.delete(id);
		setAt((a) => ({ ...a, [id]: { ms: Date.now(), expected: exp } }));
		cb.current(id, name, exp);
	}, []);
	const expect = useCallback((id: string): void => void expected.current.set(id, Date.now() + 30_000), []);
	return useMemo(() => ({ at, observe, expect }), [at, observe, expect]);
}

/** 重启 / 前置窗口 / 复制: 操作条与运维页共用, 各自持有 busy. 复制成功后打开新环境. */
function useEnvOps(env: Env, onChanged: () => void, onOpenEnv: (id: string) => void, expect: (id: string) => void) {
	const [busy, setBusy] = useState("");
	const op = async <T,>(name: string, call: () => Promise<Result<T>>, ok: (d: T) => string): Promise<T | undefined> => {
		setBusy(name);
		const r = await call();
		setBusy("");
		if (!r.ok) return void toast.error(r.error);
		toast.success(ok(r.data));
		onChanged();
		return r.data;
	};
	return {
		busy,
		restart: (): void => {
			expect(env.id);
			void op("restart", () => opsApi.restart(env.id), () => "已重启 " + env.name);
		},
		focus: (): void => void op("focus", () => opsApi.focus(env.id), (d) => (d?.visible_to_user === false ? "已前置, 但窗口当前对你不可见" : "窗口已前置")),
		duplicate: (): void =>
			void op("duplicate", () => opsApi.duplicate(env.id), (d) => "已复制为 " + (d?.name ?? "新环境")).then((d) => {
				if (d?.id) onOpenEnv(d.id);
			}),
	};
}

function exitText(m: ExitMark | undefined): string {
	if (!m) return "本次打开后未检测到退出";
	return new Date(m.ms).toLocaleTimeString() + (m.expected ? ", 主动停止或重启" : ", 意外退出");
}

/**
 * 运维: 进程与窗口 (运行态 5 秒轮询 + 退出记录, 重启 / 前置 / 复制), 出口检测, 体检与修复, Cookie 注入.
 * 只读环境: 前置窗口, 出口检测, 体检可用; 重启, 复制, 修复, 注入禁用.
 */
function OpsTab(props: {
	env: Env;
	ro: boolean;
	checks: ExitChecks;
	watch: ExitWatch;
	onChanged: () => void;
	onOpenEnv: (id: string) => void;
}): JSX.Element {
	const id = props.env.id;
	const live = usePoll(useCallback((s: AbortSignal) => envStatus(id, s), [id]));
	const env = live.result?.ok ? live.result.data : props.env;
	const running = !!env.runtime;
	const { observe, expect } = props.watch;
	useEffect(() => observe(env.id, running, env.name), [observe, env.id, running, env.name]);
	const ops = useEnvOps(env, props.onChanged, props.onOpenEnv, expect);
	const mark = props.watch.at[id];
	const b = (name: string, label: string, icon: JSX.Element, onClick: () => void, disabled: boolean): JSX.Element => (
		<Button size="xs" variant="outline" disabled={disabled || !!ops.busy} onClick={onClick} data-ops={name} title={label}>
			{ops.busy === name ? <Spinner /> : icon}
			{label}
		</Button>
	);
	return (
		<div className="flex flex-col gap-3">
			<Section
				title="进程与窗口"
				right={
					<Badge variant={running ? "success" : mark && !mark.expected ? "destructive" : "secondary"}>
						{running ? "运行中" : mark && !mark.expected ? "已退出" : "未运行"}
					</Badge>
				}
			>
				<KV
					rows={[
						["状态接口", live.result && !live.result.ok ? "不可达: " + live.result.error : running ? "运行中" : "未运行"],
						["PID", running ? rtv(env, "pid") : null],
						["窗口", running ? (env.visible ? env.window_title || "可见 (无标题)" : env.visible_reason || "不可见") : null],
						["最近退出", exitText(mark)],
					]}
				/>
				<div className="flex flex-wrap items-center gap-2 pt-1">
					<span title={props.ro ? RO_HINT : running ? "重启浏览器进程" : "环境未运行"}>
						{b("restart", "重启", <RotateCw className="size-4" />, ops.restart, props.ro || !running)}
					</span>
					<span title={running ? "把浏览器窗口拉到最前" : "环境未运行"}>
						{b("focus", "前置窗口", <AppWindow className="size-4" />, ops.focus, !running)}
					</span>
					<span title={props.ro ? RO_HINT : "复制为新环境并打开"}>
						{b("duplicate", "复制", <Copy className="size-4" />, ops.duplicate, props.ro)}
					</span>
				</div>
			</Section>
			<ExitSection env={env} checks={props.checks} />
			<DoctorTab env={env} ro={props.ro} onChanged={props.onChanged} />
			<InjectCookies env={env} ro={props.ro} onChanged={props.onChanged} />
		</div>
	);
}

/** 抽屉顶部常驻操作条. 启停 / 编辑 / 删除交给页面的 useEnvActions (统一确认与行内状态), 其余直接调 opsApi. */
function ActionBar(props: {
	env: Env;
	ro: boolean;
	onAction: (a: EnvAction, env: Env) => void;
	onOpenEnv: (id: string) => void;
	onChanged: () => void;
	expect: (id: string) => void;
}): JSX.Element {
	const { env, ro } = props;
	const running = !!env.runtime;
	const ops = useEnvOps(env, props.onChanged, props.onOpenEnv, props.expect);
	const busy = ops.busy;
	const btn = (name: string, label: string, icon: JSX.Element, onClick: () => void, opts: { write?: boolean; disabled?: boolean } = {}): JSX.Element => (
		<span title={opts.write && ro ? RO_HINT : label}>
			<Button size="sm" variant="ghost" className="gap-1 px-2" disabled={(opts.write && ro) || opts.disabled || !!busy} onClick={onClick} data-detail-op={name}>
				{busy === name ? <Spinner /> : icon}
				<span className="hidden sm:inline">{label}</span>
			</Button>
		</span>
	);
	return (
		<div className="flex flex-wrap items-center gap-0.5 border-y border-border/60 px-2 py-1" data-detail-actions="">
			{running
				? btn("stop", "停止", <Square className="size-4" />, () => {
						props.expect(env.id);
						props.onAction("stop", env);
					}, { write: true })
				: btn("start", "启动", <Play className="size-4" />, () => props.onAction("start", env), { write: true })}
			{btn("restart", "重启", <RotateCw className="size-4" />, ops.restart, { write: true, disabled: !running })}
			{btn("focus", "前置窗口", <AppWindow className="size-4" />, ops.focus, { disabled: !running })}
			{btn("duplicate", "复制", <Copy className="size-4" />, ops.duplicate, { write: true })}
			{btn("edit", "编辑", <Pencil className="size-4" />, () => props.onAction("edit", env), { write: true })}
			<span className="ml-auto">{btn("delete", "删除", <Trash2 className="size-4" />, () => props.onAction("delete", env), { write: true })}</span>
		</div>
	);
}

const TABS = [
	{ id: "overview", label: "概览" },
	{ id: "ops", label: "运维" },
	{ id: "fp", label: "指纹" },
	{ id: "cookies", label: "Cookies" },
	{ id: "cache", label: "缓存" },
	{ id: "pack", label: "账号包" },
] as const;

/** 右侧详情抽屉. env 为 null 时关闭; 只挂当前 Tab 的内容, 切走即停止该页轮询. */
export function DetailSheet(props: {
	env: Env | null;
	onClose: () => void;
	segs: Seg[];
	onChain: () => void;
	onAction: (a: EnvAction, env: Env) => void;
	onOpenEnv: (id: string) => void;
	onChanged: () => void;
	checks: ExitChecks;
}): JSX.Element {
	const env = props.env;
	const ro = env ? readOnly(env) : true;
	const onChanged = props.onChanged;
	const watch = useExitWatch((_id, name, expected) => {
		if (expected) return;
		toast.error("浏览器进程已退出: " + name);
		onChanged();
	});
	const { observe } = watch;
	const envId = env?.id;
	const envRunning = !!env?.runtime;
	const envName = env?.name ?? "";
	useEffect(() => {
		if (envId) observe(envId, envRunning, envName);
	}, [observe, envId, envRunning, envName]);
	const mark = env ? watch.at[env.id] : undefined;
	return (
		<Sheet open={!!env} onOpenChange={(o) => (o ? undefined : props.onClose())} modal={false}>
			<SheetContent
				side="right"
				className="w-full gap-0 sm:w-[520px] sm:max-w-[520px]"
				overlayClassName="hidden"
				onInteractOutside={(e) => e.preventDefault()}
				onOpenAutoFocus={(e) => e.preventDefault()}
				data-detail-sheet=""
			>
				<SheetHeader className="pb-2">
					<SheetTitle className="flex items-center gap-2 truncate font-mono">
						<span className={"size-2 shrink-0 rounded-full " + (env?.runtime ? "bg-emerald-500" : "bg-muted-foreground/40")} />
						<span className="truncate">{env?.name ?? ""}</span>
						{env?.pending_restart ? <Badge variant="warning" className="shrink-0">待重启</Badge> : null}
						{env && !env.runtime && mark && !mark.expected ? (
							<Badge variant="destructive" className="shrink-0" title={exitText(mark)}>
								<Activity className="size-3" />已退出
							</Badge>
						) : null}
					</SheetTitle>
					<SheetDescription className="flex items-center gap-2">
						{ro ? <Lock className="size-4" /> : null}
						{env ? (ro ? "API 组, " + RO_HINT + ", 本页只读" : "人类组, " + whereLabel(env)) : ""}
						{env ? (
							<Button size="xs" variant="ghost" className="ml-auto" onClick={props.onChain} data-open-chain="">
								<Route className="size-4" />链路
							</Button>
						) : null}
					</SheetDescription>
				</SheetHeader>
				{env ? <ActionBar env={env} ro={ro} onAction={props.onAction} onOpenEnv={props.onOpenEnv} onChanged={props.onChanged} expect={watch.expect} /> : null}
				{env ? (
					<Tabs key={env.id} defaultValue="overview" className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4 pb-6 pt-3">
						<TabsList className="self-start">
							{TABS.map((t) => (
								<TabsTrigger key={t.id} value={t.id} data-detail-tab={t.id}>
									{t.label}
								</TabsTrigger>
							))}
						</TabsList>
						<TabsContent value="overview"><OverviewTab env={env} segs={props.segs} onChain={props.onChain} checks={props.checks} /></TabsContent>
						<TabsContent value="ops">
							<OpsTab env={env} ro={ro} checks={props.checks} watch={watch} onChanged={props.onChanged} onOpenEnv={props.onOpenEnv} />
						</TabsContent>
						<TabsContent value="fp"><FingerprintTab env={env} /></TabsContent>
						<TabsContent value="cookies"><CookiesTab env={env} /></TabsContent>
						<TabsContent value="cache"><CacheTab env={env} ro={ro} /></TabsContent>
						<TabsContent value="pack"><PackTab env={env} ro={ro} /></TabsContent>
					</Tabs>
				) : null}
			</SheetContent>
		</Sheet>
	);
}
