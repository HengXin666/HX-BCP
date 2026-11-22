import type { JSX } from "react";
import { Activity, Database, Gauge, Globe, PlugZap } from "lucide-react";
import { NavTabs, type NavTabItem } from "@hx/ui";
import { useRoute } from "./lib/route";
import { AccessPage } from "./pages/AccessPage";
import { CachePage } from "./pages/CachePage";
import { FlowsPage } from "./pages/FlowsPage";
import { HostsPage } from "./pages/HostsPage";
import { OverviewPage } from "./pages/OverviewPage";
import { Topbar } from "./shell/Topbar";
import { useStats } from "./shell/useStats";

const NAV: readonly NavTabItem[] = [
	{ id: "overview", label: "总览", icon: Gauge },
	{ id: "flows", label: "抓包", icon: Activity },
	{ id: "cache", label: "缓存", icon: Database },
	{ id: "hosts", label: "主机与 TLS", icon: Globe },
	{ id: "access", label: "接入", icon: PlugZap },
];

/**
 * 外壳: 吸顶顶栏 (品牌, 运行状态, 主题) + 页签导航 + 当前页. 路由走 hash (#/flows?host=...), 默认总览.
 * 页面全部只读 hxcp.api 的相对路径, 写操作只有 TLS 规则, 删条目, 清过期.
 */
export function App(): JSX.Element {
	const { route, go } = useRoute();
	const stats = useStats();
	const page = NAV.some((n) => n.id === route.page) ? route.page : "overview";
	const body = (() => {
		if (page === "flows") return <FlowsPage query={route.query} onQuery={(q) => go("flows", q, true)} />;
		if (page === "cache") return <CachePage />;
		if (page === "hosts") return <HostsPage />;
		if (page === "access") return <AccessPage />;
		return <OverviewPage stats={stats.data} go={go} />;
	})();
	return (
		<div className="flex min-h-dvh w-full flex-col">
			<Topbar stats={stats.data} error={stats.error} />
			<nav className="border-b border-border-muted px-3 md:px-6">
				<NavTabs items={NAV} value={page} onChange={(id) => go(id)} />
			</nav>
			<main className="min-w-0 flex-1 px-3 py-4 md:px-6 md:py-6">
				<div key={page} className="page-enter mx-auto w-full max-w-[1680px]">
					{body}
				</div>
			</main>
		</div>
	);
}
