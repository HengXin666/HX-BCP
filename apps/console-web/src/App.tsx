import type { JSX } from "react";
import { Activity, Boxes, Monitor } from "lucide-react";
import { AppFrame, Badge, Card, CardContent, NavTabs, SidebarNavItemButton } from "@hx/ui";
import { BrowsersPage } from "./pages/browsers/BrowsersPage";
import { HumanGate } from "./pages/HumanGate";
import { ResourcesPage } from "./pages/resources/ResourcesPage";
import { SystemPage } from "./pages/system/SystemPage";
import { useRoute } from "./lib/router";

/** 一级页面. 每级上线时只列已完成的页, 不留空导航. */
const PAGES = [
	{ id: "browsers", label: "浏览器", path: ["browsers"], icon: Monitor },
	{ id: "resources", label: "资源", path: ["resources", "proxies"], icon: Boxes },
	{ id: "system", label: "系统", path: ["system", "status"], icon: Activity },
] as const;

/**
 * AppFrame 默认 h-screen + overflow-hidden + w-screen, 是给 "外壳不滚, 内层各自滚" 的应用用的.
 * 本控制台改为整页滚动 (表头 sticky 贴视口, 分组栏 sticky 自带滚动), 所以解封:
 * 高度随内容, 不裁剪; w-screen 含滚动条宽度, 出现竖向滚动条后会横向溢出, 改 w-full.
 */
const FRAME = "h-auto min-h-dvh w-full overflow-visible";

type PageId = (typeof PAGES)[number]["id"];

/**
 * 控制台外壳: 宽屏左侧导航, 窄屏顶部页签, 右侧当前页. 路由走 hash (见 lib/router.ts),
 * 全部内容在人类通道门之后; 本机回环连接直接放行, 不弹令牌框.
 */
export function App(): JSX.Element {
	const { route, go } = useRoute(["browsers"]);
	const current: PageId = PAGES.some((p) => p.id === route.path[0]) ? (route.path[0] as PageId) : "browsers";
	const open = (id: string): void => go([...(PAGES.find((p) => p.id === id)?.path ?? ["browsers"])]);

	return (
		<AppFrame className={FRAME} contentClassName="min-w-0 p-0">
			<div className="flex min-w-0 flex-1 flex-col gap-3 p-3 md:flex-row md:items-start">
				<Card variant="solid" className="shrink-0 md:sticky md:top-3 md:w-44">
					<CardContent className="flex flex-col gap-3 p-4">
						<div className="flex items-center gap-2">
							<img src="./favicon.svg" alt="" className="size-5" />
							<span className="t-card">HX-BCP</span>
							<Badge variant="secondary" className="ml-auto">人类</Badge>
						</div>
						<nav className="hidden flex-col gap-1 md:flex" aria-label="控制台页面">
							{PAGES.map((p) => (
								<SidebarNavItemButton key={p.id} item={{ id: p.id, label: p.label, icon: p.icon, active: p.id === current }} onClick={() => open(p.id)} />
							))}
						</nav>
						<NavTabs className="md:hidden" items={PAGES.map((p) => ({ id: p.id, label: p.label, icon: p.icon }))} value={current} onChange={open} />
					</CardContent>
				</Card>
				<Card variant="solid" className="min-w-0 flex-1">
					<CardContent className="min-w-0 p-4">
						<HumanGate>
							{/* 换页时内容区 200ms 淡入 (tw-animate 的 enter 关键帧, 只动 opacity); key 让每次换页重新挂载触发 */}
							<div key={current} data-route={current} className="animate-in fade-in-0 duration-200">
								{current === "browsers" ? (
									<BrowsersPage params={route.query} onParams={(q) => go(["browsers"], q, true)} />
								) : current === "resources" ? (
									<ResourcesPage sub={route.path[1]} onSub={(id) => go(["resources", id])} />
								) : (
									<SystemPage sub={route.path[1]} onSub={(id) => go(["system", id])} />
								)}
							</div>
						</HumanGate>
					</CardContent>
				</Card>
			</div>
		</AppFrame>
	);
}
