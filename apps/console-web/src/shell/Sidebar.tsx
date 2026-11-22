/**
 * 侧栏: 品牌, 三组导航 (带在跑数, 最近删除数, 孤儿进程数角标), 底部通道标识与收起按钮.
 * 收起后只剩 56px 图标列, 悬停 title 给名称; 窄屏不渲染侧栏, 由顶栏菜单按钮打开同一份内容的抽屉.
 */
import type { JSX } from "react";
import { PanelLeftClose, PanelLeftOpen, ShieldCheck } from "lucide-react";
import { Button, SidebarNavItemButton, type SidebarNavBadge } from "@hx/ui";
import { NAV, type NavItem } from "./nav";
import type { ShellStats } from "./useShellStats";

function badgeOf(item: NavItem, stats: ShellStats): SidebarNavBadge | undefined {
	if (item.id === "browsers" && stats.running) return { text: String(stats.running), variant: "count" };
	if (item.id === "live" && stats.running) return { text: String(stats.running), variant: "count" };
	if (item.id === "trash" && stats.trash) return { text: String(stats.trash), variant: "count" };
	if (item.id === "orphans" && stats.orphans) return { text: String(stats.orphans), variant: "count" };
	return undefined;
}

export function Sidebar(props: {
	active: NavItem;
	stats: ShellStats;
	collapsed: boolean;
	onToggle?: () => void;
	onOpen: (item: NavItem) => void;
}): JSX.Element {
	const { collapsed } = props;
	return (
		<div className="flex h-full min-h-0 flex-col gap-4 px-2 py-3" data-sidebar="" data-collapsed={String(collapsed)}>
			<div className={"flex h-9 items-center gap-2 " + (collapsed ? "justify-center" : "px-2")}>
				<span className="brand-mark flex size-8 shrink-0 items-center justify-center rounded-lg">
					<img src="./favicon.svg" alt="" className="size-5" />
				</span>
				{collapsed ? null : (
					<span className="flex min-w-0 flex-col">
						<span className="t-card text-heading">HX-BCP</span>
						<span className="t-aux text-muted-foreground">调试链路控制台</span>
					</span>
				)}
			</div>
			<nav className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto overscroll-contain" aria-label="控制台导航">
				{NAV.map((g) => (
					<div key={g.id} className="flex flex-col gap-1">
						{collapsed ? <span className="mx-auto my-1 h-px w-6 bg-border" aria-hidden /> : <span className="px-2 pb-1 t-micro text-muted-foreground">{g.label}</span>}
						{g.items.map((item) => {
							const on = item.id === props.active.id;
							return (
								<SidebarNavItemButton
									key={item.id}
									item={{ id: item.id, label: collapsed ? undefined : item.label, title: item.label + "  " + item.keys.toUpperCase(), icon: item.icon, active: on, badge: collapsed ? undefined : badgeOf(item, props.stats) }}
									className={"nav-item h-8 " + (collapsed ? "justify-center px-0" : "")}
									data-active={String(on)}
									aria-current={on ? "page" : undefined}
									onClick={() => props.onOpen(item)}
								/>
							);
						})}
					</div>
				))}
			</nav>
			<div className={"flex items-center gap-2 border-t border-border-muted pt-3 " + (collapsed ? "flex-col" : "px-1")}>
				<span className="flex min-w-0 flex-1 items-center gap-2 t-aux text-muted-foreground" title="本页走人类通道, API 组只读">
					<ShieldCheck className="size-4 shrink-0 text-success" />
					{collapsed ? null : <span className="truncate">人类通道</span>}
				</span>
				{props.onToggle ? (
					<Button size="icon-sm" variant="ghost" onClick={props.onToggle} aria-label={collapsed ? "展开侧栏" : "收起侧栏"} title={(collapsed ? "展开侧栏" : "收起侧栏") + "  [ "}>
						{collapsed ? <PanelLeftOpen className="size-4" /> : <PanelLeftClose className="size-4" />}
					</Button>
				) : null}
			</div>
		</div>
	);
}
