/**
 * 浏览器页 (控制台主页面): 左侧分组栏, 右侧 页头 / 状态速览 / 工具条 / 批量条 / 列表 / 吸底分页.
 * 条件, 范围与打开中的环境 (env=) 全在 hash query 里: 刷新不丢, 可以直接把某个环境的详情链接发给别人.
 * 两个抽屉 (详情 DetailSheet, 链路 ChainSheet) 由本页持有, 列表只负责通知; 命令面板经 lib/bus 发来的
 * 新建浏览器 / 新建文件夹 / 导入 / 打开环境 都在这里落地. 数据全走 lib 的带缓存读接口, 写操作由 lib 失效缓存.
 */
import { useCallback, useEffect, useMemo, useRef, useState, type JSX } from "react";
import { PanelLeft } from "lucide-react";
import { Badge, Button, CollapsibleRail, EmptyState, Sheet, SheetContent, SheetHeader, SheetTitle, toast } from "@hx/ui";
import { listEnvs, listFolders, type Env } from "../../lib/api";
import { listen, type Command } from "../../lib/bus";
import { chainOf, exitStateOf, upstreamKey } from "../../lib/chain";
import { applyQuery, isRunning, readQuery, tagsOf, writeQuery, type EnvQuery, type ExitFilter, type SortKey, type StatusFilter } from "../../lib/env-query";
import { LoginContext, loginStates } from "../../lib/login";
import { useExitChecks, usePoll } from "../../lib/hooks";
import { KernelContext, kernelFilterOptions, kernelPaths, kernelView, listKernels } from "../../lib/kernels";
import { HUMAN_KEY, buildTree, crumbs, inScope, scopeOf } from "../../lib/tree";
import { PageTitle } from "../PageTitle";
import { BatchBar } from "./BatchBar";
import { ChainSheet } from "./ChainSheet";
import { DetailSheet } from "./DetailSheet";
import { EnvDialog } from "./EnvDialogs";
import { EnvList } from "./EnvList";
import { selectable, type EnvAction } from "./EnvTable";
import { EnvToolbar } from "./EnvToolbar";
import { FolderRail } from "./FolderRail";
import { FpDialog } from "./FpDialog";
import { ImportDialog, type ImportKind } from "./ImportDialog";
import { PageActions } from "./PageActions";
import { PageSkeleton } from "./PageSkeleton";
import { PendingContext, useEnvActions } from "./useEnvActions";

const RAIL_WIDTH = 240;
const EXIT_LABEL: Record<ExitFilter, string> = { all: "全部出口", bad: "出口不通", ok: "出口正常", unchecked: "出口未检测", direct: "未挂上游" };
const RAIL_BOX = "sticky top-[calc(var(--topbar-h,3.5rem)+0.75rem)] hidden h-[calc(100dvh-var(--topbar-h,3.5rem)-1.5rem)] shrink-0 lg:block";

type Pulse = { key: string; label: string; count: number; tone: "success" | "warning" | "destructive" | "secondary"; patch: Partial<EnvQuery>; active: boolean };

/** 状态速览: 一排可点的计数, 点一下等于在工具条里选了对应筛选, 再点一下取消. 计数为 0 的告警项不显示. */
function PulseBar(props: { items: Pulse[]; onPick: (p: Partial<EnvQuery>, active: boolean) => void }): JSX.Element | null {
	const shown = props.items.filter((p) => p.count > 0 || p.tone === "secondary");
	if (!shown.length) return null;
	return (
		<div className="flex flex-wrap items-center gap-1.5" data-pulse-bar="">
			{shown.map((p) => (
				<button
					key={p.key}
					type="button"
					data-pulse={p.key}
					aria-pressed={p.active}
					onClick={() => props.onPick(p.patch, p.active)}
					className={"rounded-full transition-opacity focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring " + (p.active ? "" : "opacity-80 hover:opacity-100")}
				>
					<Badge variant={p.active ? p.tone : "outline"} className="gap-1 tabular-nums">
						{p.label}
						<span className="font-semibold">{p.count}</span>
					</Badge>
				</button>
			))}
		</div>
	);
}

export function BrowsersPage(props: { params: URLSearchParams; onParams: (q: Record<string, string>) => void }): JSX.Element {
	const envs = usePoll(listEnvs);
	const folders = usePoll(listFolders);
	const kernelList = usePoll(listKernels, 30000);
	const paths = usePoll(kernelPaths, 30000);
	const logins = usePoll(loginStates, 60000);
	const checks = useExitChecks();
	const [picked, setPicked] = useState<Set<string>>(new Set());
	const [railCollapsed, setRailCollapsed] = useState(false);
	const [railSheet, setRailSheet] = useState(false);
	const [importKind, setImportKind] = useState<ImportKind | null>(null);
	const [chainId, setChainId] = useState("");

	const selectedNode = props.params.get("scope") || "all";
	const detailId = props.params.get("env") || "";
	const query = readQuery(props.params);
	const envList: Env[] = envs.result?.ok ? envs.result.data : [];
	const folderMap = folders.result?.ok ? folders.result.data : {};
	const humanFolders = folderMap[HUMAN_KEY] ?? [];
	const tree = useMemo(() => buildTree(envList, folderMap), [envList, folderMap]);
	const scope = scopeOf(selectedNode);
	const inside = useMemo(() => envList.filter((e) => inScope(e, scope)), [envList, selectedNode]);
	const kernels = kernelList.result?.ok ? kernelList.result.data : [];
	const kpaths = paths.result?.ok ? paths.result.data : null;
	const view = useCallback((e: Env) => kernelView(e, kernels, kpaths), [kernelList.result, paths.result]);
	const byId = useMemo(() => new Map(envList.map((e) => [e.id, e])), [envList]);

	// 出口检测: 全部环境用到的每个上游各测一次 (lib 里并发 4, 结果全站共享), 出口筛选, 链路列与详情都读它
	const { results, run } = checks;
	const ups = useMemo(() => [...new Map(envList.filter((e) => upstreamKey(e.proxy)).map((e) => [upstreamKey(e.proxy), e.proxy ?? null])).entries()], [envList]);
	const upKeys = ups.map(([k]) => k).join(",");
	useEffect(() => {
		const todo = ups.filter(([k]) => !results[k]);
		if (todo.length) void run(todo.map(([key, proxy]) => ({ key, proxy })));
		// 只在上游集合变化时触发
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [upKeys, run]);
	const exitOf = useCallback((e: Env) => exitStateOf(e, results), [results]);
	const segsOf = useCallback((e: Env) => chainOf(e, results[upstreamKey(e.proxy)]), [results]);

	const exitOptions = useMemo(() => {
		const n: Record<string, number> = {};
		for (const e of inside) n[exitOf(e)] = (n[exitOf(e)] ?? 0) + 1;
		return (Object.keys(EXIT_LABEL) as ExitFilter[]).map((k) => ({ value: k, label: EXIT_LABEL[k] + (k === "all" ? "" : " (" + (n[k] ?? 0) + ")") }));
	}, [inside, exitOf]);
	const page = useMemo(() => applyQuery(inside, query, view, exitOf), [inside, props.params, view, exitOf]);
	const kernelOptions = useMemo(() => kernelFilterOptions(kernels, inside, view), [inside, view]);
	const tags = useMemo(() => tagsOf(inside), [inside]);
	const human = scope.kind !== "agent"; // 全部与人类组可写 (行级仍按 selectable 判定)
	const folder = scope.kind === "human" && scope.folder ? scope.folder : null;

	const pulses = useMemo((): Pulse[] => {
		const status = (s: StatusFilter, label: string, tone: Pulse["tone"], count: number): Pulse => ({ key: s, label, tone, count, patch: { status: s }, active: query.status === s });
		const running = inside.filter(isRunning).length;
		return [
			status("running", "运行中", "success", running),
			status("restart", "待重启", "warning", inside.filter((e) => e.pending_restart).length),
			status("busy", "占用中", "secondary", inside.filter((e) => e.in_use).length),
			{ key: "exit-bad", label: "出口不通", tone: "destructive", count: inside.filter((e) => exitOf(e) === "bad").length, patch: { exit: "bad" }, active: query.exit === "bad" },
		];
	}, [inside, exitOf, query.status, query.exit]);

	// 换范围清空选择; 每次刷新去掉已不存在或已不可写的 id
	useEffect(() => setPicked(new Set()), [selectedNode]);
	useEffect(() => {
		setPicked((cur) => new Set([...cur].filter((id) => byId.has(id) && selectable(byId.get(id) as Env))));
	}, [byId]);

	/** 写 hash: 筛选条件 + 范围 + 打开中的环境, 三者互不覆盖. */
	const write = (patch: Partial<EnvQuery>, node = selectedNode, env = detailId): void =>
		props.onParams({ ...writeQuery({ ...query, ...patch }), ...(node !== "all" ? { scope: node } : {}), ...(env ? { env } : {}) });
	const setParams = (patch: Partial<EnvQuery>): void => write(patch);
	const openDetail = (id: string): void => (setChainId(""), write({}, selectedNode, id));
	const closeDetail = (): void => write({}, selectedNode, "");
	const openChain = (id: string): void => (write({}, selectedNode, ""), setChainId(id));
	// 点 "全部" 同时清掉状态, 内核, 标签, 出口筛选
	const pickNode = (id: string): void => (setRailSheet(false), write({ page: 1, ...(id === "all" ? { status: "all", kernel: "", tag: "", exit: "all" } : {}) }, id));
	const onSort = (col: SortKey): void => setParams({ sort: col, dir: query.sort === col && query.dir === "desc" ? "asc" : "desc", page: 1 });
	const onSelect = (ids: string[], checked: boolean): void =>
		setPicked((cur) => {
			const next = new Set(cur);
			for (const id of ids) (checked ? next.add(id) : next.delete(id));
			return next;
		});
	const onPulse = (patch: Partial<EnvQuery>, active: boolean): void =>
		setParams({ page: 1, ...(active ? { ...("status" in patch ? { status: "all" as const } : {}), ...("exit" in patch ? { exit: "all" as const } : {}) } : patch) });

	const reload = (): void => (envs.reload(), folders.reload());
	const act = useEnvActions({ byId, picked, setPicked, kernels, reload, pickNode });
	const { busy, dialog, setDialog } = act;
	const onAction = (a: EnvAction, e: Env): void => (a === "detail" ? openDetail(e.id) : void act.onAction(a, e));

	// 打开中的环境被删除或移走: 列表已加载且找不到时关掉抽屉, 并提示一次
	const loaded = !!envs.result?.ok;
	useEffect(() => {
		if (loaded && detailId && !byId.has(detailId)) {
			toast.error("环境 " + detailId + " 不存在或已删除");
			closeDetail();
		}
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [loaded, detailId, byId]);

	// 命令面板 / 其他页经 lib/bus 发来的命令. 用 ref 拿最新闭包, 订阅只建一次, 排队中的命令在订阅时补发
	const onCommand = useRef<(c: Command) => void>(() => undefined);
	onCommand.current = (c: Command): void => {
		if (c.page !== "browsers") return;
		if (c.kind === "open-env") return openDetail(c.id);
		if (c.kind === "new-browser") return act.setFp({ mode: "create", folder: folder ?? "" });
		if (c.kind === "new-folder") return setDialog({ kind: "new-folder" });
		if (c.kind === "import") return setImportKind(c.what);
	};
	useEffect(() => listen("browsers", (c) => onCommand.current(c)), []);

	if (envs.loading && !envs.result) return <PageSkeleton />;
	if (!envs.result?.ok) return <EmptyState text="未连接" hint={"浏览器服务不可达: " + (envs.result ? envs.result.error : "")} />;

	const detailEnv = detailId ? byId.get(detailId) ?? null : null;
	const chainEnv = chainId ? byId.get(chainId) ?? null : null;
	const rail = <FolderRail nodes={tree} all={envList} inside={inside} selectedId={selectedNode} onSelect={pickNode} query={query} onFilter={(p) => setParams({ ...p, page: 1 })} kernels={kernels} view={view} />;
	const writeButtons = human ? (
		<PageActions
			folder={folder}
			onCreate={() => act.setFp({ mode: "create", folder: folder ?? "" })}
			onNewFolder={() => setDialog({ kind: "new-folder" })}
			onRenameFolder={(f) => setDialog({ kind: "rename-folder", folder: f })}
			onRemoveFolder={(f) => void act.removeFolder(f)}
			onImport={setImportKind}
		/>
	) : null;
	const desc = human ? inside.length + " 个浏览器 · 人类组可写, 勾选多行可批量操作" : inside.length + " 个浏览器 · API 组只能由 api 或 mcp 创建与操作, 这里只读";

	return (
		<LoginContext.Provider value={logins.result?.ok ? logins.result.data : null}>
		<KernelContext.Provider value={view}>
		<PendingContext.Provider value={act.pending}>
		<div className="flex min-w-0 items-start gap-4" data-page="browsers">
			<div className={RAIL_BOX}>
				<CollapsibleRail className="h-full" collapsed={railCollapsed} onToggle={() => setRailCollapsed((c) => !c)} width={RAIL_WIDTH} toggleLabel="分组栏">
					{(wide) => (wide ? <div className="h-full min-h-0">{rail}</div> : null)}
				</CollapsibleRail>
			</div>
			<div className="flex min-w-0 flex-1 flex-col gap-3">
				<PageTitle
					title={crumbs(scope).join(" / ")}
					desc={desc}
					right={writeButtons}
					extra={<Button size="sm" variant="outline" className="lg:hidden" onClick={() => setRailSheet(true)}><PanelLeft className="size-4" />分组</Button>}
				/>
				<PulseBar items={pulses} onPick={onPulse} />
				<EnvToolbar query={query} tags={tags} kernelOptions={kernelOptions} exitOptions={exitOptions} total={page.total} onChange={(p) => setParams(p)} />
				<BatchBar count={picked.size} busy={busy} onAction={(a) => void act.onBatch(a)} onClear={() => setPicked(new Set())} />
				<EnvList
					page={page}
					query={query}
					selected={picked}
					activeId={detailId || chainId}
					onSelect={onSelect}
					onSort={onSort}
					onAction={onAction}
					onPage={(n) => setParams({ page: n })}
					chainOf={segsOf}
					onDetail={(e) => openDetail(e.id)}
					onChain={(e) => openChain(e.id)}
				/>
			</div>
			<Sheet open={railSheet} onOpenChange={setRailSheet}>
				<SheetContent side="left" className="flex w-[85vw] max-w-sm flex-col">
					<SheetHeader>
						<SheetTitle>分组</SheetTitle>
					</SheetHeader>
					<div className="min-h-0 flex-1 px-4 pb-4">{rail}</div>
				</SheetContent>
			</Sheet>
			<DetailSheet
				env={detailEnv}
				onClose={closeDetail}
				segs={detailEnv ? segsOf(detailEnv) : []}
				onChain={() => detailEnv && openChain(detailEnv.id)}
				onAction={onAction}
				onOpenEnv={openDetail}
				onChanged={reload}
				checks={checks}
			/>
			<ChainSheet env={chainEnv} onClose={() => setChainId("")} checks={checks} />
			<EnvDialog state={dialog} folders={humanFolders} kernels={kernels} busy={busy} onClose={() => setDialog(null)} onSubmit={(s, v) => void act.onSubmit(s, v)} />
			<ImportDialog kind={importKind} onClose={() => setImportKind(null)} onDone={reload} />
			<FpDialog target={act.fp} folders={humanFolders} kernels={kernels} busy={busy} onClose={() => act.setFp(null)} onSubmit={(t, b) => void act.onFpSubmit(t, b)} />
		</div>
		</PendingContext.Provider>
		</KernelContext.Provider>
		</LoginContext.Provider>
	);
}
