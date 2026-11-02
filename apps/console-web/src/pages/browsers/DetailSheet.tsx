/**
 * 环境详情抽屉: 点表格行从右侧滑出, 约 480px 宽, 内容在抽屉内滚动. 非模态且不带遮罩, 列表仍可滚可点,
 * 点另一行直接换成那一行的详情. 五页: 概览, 指纹, Cookies, 缓存, 账号包. 缺的数据一律写 "未上报".
 * API 组 (lane=agent) 只读: 所有写按钮 disabled, 提示 "由 api 或 mcp 管理".
 */
import { useCallback, type JSX } from "react";
import { Lock, Route } from "lucide-react";
import { Badge, Button, Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, Tabs, TabsContent, TabsList, TabsTrigger } from "@hx/ui";
import { envStatus, type Env } from "../../lib/api";
import { STATE_TEXT, type Seg } from "../../lib/chain";
import { usePoll } from "../../lib/hooks";
import { loggedIn, useLogin, type EnvLogin } from "../../lib/login";
import { KV } from "./ChainParts";
import { CookiesTab, FingerprintTab } from "./DetailFp";
import { CacheTab, PackTab } from "./DetailOps";
import { RO_HINT, Section, readOnly, ts } from "./DetailParts";
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

/** 概览: 运行时 (GET /status, 5 秒轮询) + 链路四段摘要 (点按钮开链路抽屉) + 登录态 + 环境基础字段. */
function OverviewTab(props: { env: Env; segs: Seg[]; onChain: () => void }): JSX.Element {
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

const TABS = [
	{ id: "overview", label: "概览" },
	{ id: "fp", label: "指纹" },
	{ id: "cookies", label: "Cookies" },
	{ id: "cache", label: "缓存" },
	{ id: "pack", label: "账号包" },
] as const;

/** 右侧详情抽屉. env 为 null 时关闭; 只挂当前 Tab 的内容, 切走即停止该页轮询. */
export function DetailSheet(props: { env: Env | null; onClose: () => void; segs: Seg[]; onChain: () => void }): JSX.Element {
	const env = props.env;
	const ro = env ? readOnly(env) : true;
	return (
		<Sheet open={!!env} onOpenChange={(o) => (o ? undefined : props.onClose())} modal={false}>
			<SheetContent
				side="right"
				className="w-full gap-2 sm:w-[480px] sm:max-w-[480px]"
				overlayClassName="hidden"
				onInteractOutside={(e) => e.preventDefault()}
				onOpenAutoFocus={(e) => e.preventDefault()}
				data-detail-sheet=""
			>
				<SheetHeader className="pb-0">
					<SheetTitle className="truncate font-mono">{env?.name ?? ""}</SheetTitle>
					<SheetDescription className="flex items-center gap-2">
						{ro ? <Lock className="size-4" /> : null}
						{env ? (ro ? "API 组, " + RO_HINT + ", 本页只读" : "人类组, " + whereLabel(env)) : ""}
					</SheetDescription>
				</SheetHeader>
				{env ? (
					<Tabs key={env.id} defaultValue="overview" className="flex min-h-0 flex-1 flex-col gap-3 px-4 pb-6">
						<TabsList>
							{TABS.map((t) => (
								<TabsTrigger key={t.id} value={t.id} data-detail-tab={t.id}>
									{t.label}
								</TabsTrigger>
							))}
						</TabsList>
						<TabsContent value="overview"><OverviewTab env={env} segs={props.segs} onChain={props.onChain} /></TabsContent>
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
