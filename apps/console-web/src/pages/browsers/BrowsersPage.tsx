import { useCallback, useEffect, useMemo, useState, type JSX } from "react";
import { PanelLeft } from "lucide-react";
import { Button, CollapsibleRail, EmptyState, Sheet, SheetContent, SheetHeader, SheetTitle } from "@hx/ui";
import { listEnvs, listFolders, type Env } from "../../lib/api";
import { exitStateOf, upstreamKey } from "../../lib/chain";
import { applyQuery, readQuery, tagsOf, writeQuery, type EnvQuery, type ExitFilter, type SortKey } from "../../lib/env-query";
import { LoginContext, loginStates } from "../../lib/login";
import { useExitChecks, usePoll } from "../../lib/hooks";
import { KernelContext, kernelFilterOptions, kernelPaths, kernelView, listKernels } from "../../lib/kernels";
import { HUMAN_KEY, buildTree, crumbs, inScope, scopeOf } from "../../lib/tree";
import { BatchBar } from "./BatchBar";
import { EnvDialog } from "./EnvDialogs";
import { EnvList } from "./EnvList";
import { selectable, type EnvAction } from "./EnvTable";
import { EnvToolbar } from "./EnvToolbar";
import { FolderRail } from "./FolderRail";
import { PageTitle } from "../PageTitle";
import { FpDialog } from "./FpDialog";
import { ImportDialog, type ImportKind } from "./ImportDialog";
import { PageActions } from "./PageActions";
import { PageSkeleton } from "./PageSkeleton";
import { PendingContext, useEnvActions } from "./useEnvActions";

const RAIL_WIDTH = 240;
const EXIT_LABEL: Record<ExitFilter, string> = { all: "全部出口", bad: "出口不通", ok: "出口正常", unchecked: "出口未检测", direct: "未挂上游" };
const RAIL_BOX = "sticky top-3 hidden h-[calc(100dvh-1.5rem)] shrink-0 lg:block"; // 贴视口一屏高, 树在栏内自己滚
/**
 * 浏览器页: 左侧分组栏, 右侧工具条, 批量条, 表格, 分页. 条件与范围都在 hash query 里, 刷新不丢.
 * 人类组可写, API 组只读 (行不可选, 无操作菜单, 页头无写按钮).
 */
export function BrowsersPage(props: { params: URLSearchParams; onParams: (q: Record<string, string>) => void }): JSX.Element {
	const envs = usePoll(listEnvs);
	const folders = usePoll(listFolders);
	const kernelList = usePoll(listKernels, 30000);
	const paths = usePoll(kernelPaths, 30000);
	const [picked, setPicked] = useState<Set<string>>(new Set());
	const [railCollapsed, setRailCollapsed] = useState(false);
	const [railSheet, setRailSheet] = useState(false);
	const [importKind, setImportKind] = useState<ImportKind | null>(null);
	const logins = usePoll(loginStates, 60000);
	const checks = useExitChecks();

	const selectedNode = props.params.get("scope") || "all";
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
	// 出口检测: 全部环境用到的每个上游各测一次 (并发 4, 结果整个控制台共享), 出口筛选与链路列都读它
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
	const exitOptions = useMemo(() => {
		const n: Record<string, number> = {};
		for (const e of inside) n[exitOf(e)] = (n[exitOf(e)] ?? 0) + 1;
		return (Object.keys(EXIT_LABEL) as ExitFilter[]).map((k) => ({ value: k, label: EXIT_LABEL[k] + (k === "all" ? "" : " (" + (n[k] ?? 0) + ")") }));
	}, [inside, exitOf]);
	const page = useMemo(() => applyQuery(inside, query, view, exitOf), [inside, props.params, view, exitOf]);
	const kernelOptions = useMemo(() => kernelFilterOptions(kernels, inside, view), [inside, view]);
	const tags = useMemo(() => tagsOf(inside), [inside]);
	const byId = useMemo(() => new Map(envList.map((e) => [e.id, e])), [envList]);
	const human = scope.kind !== "agent"; // 全部与人类组都可写 (行级仍按 selectable 判定)
	const folder = scope.kind === "human" && scope.folder ? scope.folder : null;

	// 换范围清空选择; 每次刷新去掉已不存在或已不可写的 id
	useEffect(() => setPicked(new Set()), [selectedNode]);
	useEffect(() => {
		setPicked((cur) => new Set([...cur].filter((id) => byId.has(id) && selectable(byId.get(id) as Env))));
	}, [byId]);

	const setParams = (patch: Partial<EnvQuery>, node = selectedNode): void =>
		props.onParams({ ...writeQuery({ ...query, ...patch }), ...(node !== "all" ? { scope: node } : {}) });
	// 点 "全部" 同时清掉状态, 内核, 标签筛选
	const pickNode = (id: string): void => (setRailSheet(false), setParams({ page: 1, ...(id === "all" ? { status: "all", kernel: "", tag: "", exit: "all" } : {}) }, id));
	const onSort = (col: SortKey): void =>
		setParams({ sort: col, dir: query.sort === col && query.dir === "desc" ? "asc" : "desc", page: 1 });
	const onSelect = (ids: string[], checked: boolean): void =>
		setPicked((cur) => {
			const next = new Set(cur);
			for (const id of ids) (checked ? next.add(id) : next.delete(id));
			return next;
		});

	const reload = (): void => (envs.reload(), folders.reload());
	const act = useEnvActions({ byId, picked, setPicked, kernels, reload, pickNode });
	const { busy, dialog, setDialog } = act;

	if (envs.loading && !envs.result) return <PageSkeleton />;
	if (!envs.result?.ok) return <EmptyState text="未连接" hint={"浏览器服务不可达: " + (envs.result ? envs.result.error : "")} />;

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
	const desc = human ? "人类组可写, 选中多行可批量操作" : "API 组只能由 api 或 mcp 创建与操作, 这里只读";

	return (
		<LoginContext.Provider value={logins.result?.ok ? logins.result.data : null}>
		<KernelContext.Provider value={view}>
		<PendingContext.Provider value={act.pending}>
		<div className="flex min-w-0 items-start gap-3">
			<div className={RAIL_BOX}>
				<CollapsibleRail className="h-full" collapsed={railCollapsed} onToggle={() => setRailCollapsed((c) => !c)} width={RAIL_WIDTH} toggleLabel="分组栏">
					{(wide) => (wide ? <div className="h-[calc(100dvh-5.5rem)] min-h-0">{rail}</div> : null)}
				</CollapsibleRail>
			</div>
			<div className="flex min-w-0 flex-1 flex-col gap-3">
				<PageTitle
					title={crumbs(scope).join(" / ")}
					desc={desc}
					right={writeButtons}
					extra={<Button size="sm" variant="outline" className="lg:hidden" onClick={() => setRailSheet(true)}><PanelLeft className="size-4" />分组</Button>}
				/>
				<EnvToolbar query={query} tags={tags} kernelOptions={kernelOptions} exitOptions={exitOptions} total={page.total} onChange={(p) => setParams(p)} />
				<BatchBar count={picked.size} busy={busy} onAction={(a) => void act.onBatch(a)} onClear={() => setPicked(new Set())} />
				<EnvList page={page} query={query} selected={picked} onSelect={onSelect} onSort={onSort} onAction={(a, e) => void act.onAction(a, e)} onPage={(n) => setParams({ page: n })} checks={checks} />
			</div>
			<Sheet open={railSheet} onOpenChange={setRailSheet}>
				<SheetContent side="left" className="flex w-[85vw] max-w-sm flex-col">
					<SheetHeader>
						<SheetTitle>分组</SheetTitle>
					</SheetHeader>
					<div className="min-h-0 flex-1 px-4 pb-4">{rail}</div>
				</SheetContent>
			</Sheet>
			<EnvDialog state={dialog} folders={humanFolders} kernels={kernels} busy={busy} onClose={() => setDialog(null)} onSubmit={(s, v) => void act.onSubmit(s, v)} />
			<ImportDialog kind={importKind} onClose={() => setImportKind(null)} onDone={reload} />
			<FpDialog target={act.fp} folders={humanFolders} kernels={kernels} busy={busy} onClose={() => act.setFp(null)} onSubmit={(t, b) => void act.onFpSubmit(t, b)} />
		</div>
		</PendingContext.Provider>
		</KernelContext.Provider>
		</LoginContext.Provider>
	);
}
