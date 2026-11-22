import { useState, type JSX } from "react";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@hx/ui";
import { useHotkeys } from "./lib/hotkeys";
import { DENSITY_KEY, usePref, type Density } from "./lib/prefs";
import { useRoute } from "./lib/router";
import { BrowsersPage } from "./pages/browsers/BrowsersPage";
import { TrashPage } from "./pages/browsers/TrashPage";
import { HumanGate } from "./pages/HumanGate";
import { LivePage } from "./pages/live/LivePage";
import { OverviewPage } from "./pages/overview/OverviewPage";
import { ResourcesPage } from "./pages/resources/ResourcesPage";
import { SystemPage } from "./pages/system/SystemPage";
import { CommandPalette } from "./shell/CommandPalette";
import { NAV_ITEMS, activeItem, type NavItem } from "./shell/nav";
import { ShortcutsDialog } from "./shell/ShortcutsDialog";
import { Sidebar } from "./shell/Sidebar";
import { Topbar } from "./shell/Topbar";
import { useShellStats } from "./shell/useShellStats";

/**
 * 控制台外壳: 左侧可收起侧栏 (三组导航), 右侧吸顶顶栏 (面包屑, 搜索, 连接, 主题) 与当前页.
 * 整页滚动, 顶栏高度写在 --topbar-h, 表头与分组栏按它吸顶. 路由走 hash (lib/router.ts), 默认进总览.
 * 全部页面在人类通道门之后; 命令面板与快捷键在门外也可用 (只做跳转).
 * 表格密度 (舒适, 默认, 紧凑) 是界面偏好, 落在外壳的 data-density 上, 由 styles.css 的密度规则接管所有 DataTable 行高.
 */
export function App(): JSX.Element {
	const { route, go } = useRoute(["overview"]);
	const [collapsed, setCollapsed] = usePref<boolean>("sidebar.collapsed", false);
	const [density, setDensity] = usePref<Density>(DENSITY_KEY, "default");
	const [mobileNav, setMobileNav] = useState(false);
	const [palette, setPalette] = useState(false);
	const [help, setHelp] = useState(false);
	const stats = useShellStats();
	const active = activeItem(route.path);
	const open = (item: NavItem): void => (setMobileNav(false), go([...item.path]));

	const jumps: Record<string, () => void> = {};
	for (const item of NAV_ITEMS) jumps[item.keys] = () => open(item);
	useHotkeys({
		...jumps,
		"mod+k": () => setPalette((v) => !v),
		"?": () => setHelp(true),
		"[": () => setCollapsed(!collapsed),
	});

	const page = (() => {
		const head = route.path[0];
		if (head === "browsers") return <BrowsersPage params={route.query} onParams={(q) => go(["browsers"], q, true)} />;
		if (head === "live") return <LivePage />;
		if (head === "trash") return <TrashPage />;
		if (head === "resources") return <ResourcesPage sub={route.path[1]} />;
		if (head === "system") return <SystemPage sub={route.path[1]} />;
		return <OverviewPage go={(path, q) => go(path, q)} />;
	})();

	return (
		<div className="app-shell flex min-h-dvh w-full" data-collapsed={String(collapsed)} data-density={density}>
			<aside className="app-sidebar sticky top-0 hidden h-dvh shrink-0 border-r border-border-muted md:block" style={{ width: collapsed ? "3.5rem" : "15rem" }}>
				<Sidebar active={active} stats={stats} collapsed={collapsed} onToggle={() => setCollapsed(!collapsed)} onOpen={open} />
			</aside>
			<div className="flex min-w-0 flex-1 flex-col">
				<Topbar active={active} stats={stats} onMenu={() => setMobileNav(true)} onPalette={() => setPalette(true)} onHelp={() => setHelp(true)} />
				<main className="min-w-0 flex-1 px-3 py-4 md:px-6 md:py-6">
					<HumanGate>
						{/* 换页 200ms 淡入并上移 4px, key 让每次换页重新挂载 */}
						<div key={active.id} data-route={active.id} className="page-enter mx-auto w-full max-w-[1680px]">
							{page}
						</div>
					</HumanGate>
				</main>
			</div>
			<Sheet open={mobileNav} onOpenChange={setMobileNav}>
				<SheetContent side="left" className="w-64 p-0" showCloseButton={false}>
					<SheetHeader className="sr-only">
						<SheetTitle>导航</SheetTitle>
					</SheetHeader>
					<Sidebar active={active} stats={stats} collapsed={false} onOpen={open} />
				</SheetContent>
			</Sheet>
			<CommandPalette
				open={palette}
				onOpenChange={setPalette}
				go={open}
				collapsed={collapsed}
				onToggleSidebar={() => setCollapsed(!collapsed)}
				onHelp={() => setHelp(true)}
				density={density}
				onDensity={setDensity}
			/>
			<ShortcutsDialog open={help} onOpenChange={setHelp} />
		</div>
	);
}
