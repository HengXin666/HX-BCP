/** 侧栏导航表: 三组 (工作台, 资源, 系统), 每项一条 hash 路径与一个 g 序列快捷键. 命令面板, 面包屑, 快捷键都读这一张表. */
import { Activity, Boxes, Cpu, Gauge, Ghost, LayoutTemplate, MonitorPlay, Monitor, Network, PlugZap, Puzzle, ScrollText, Settings2, Trash2, type LucideIcon } from "lucide-react";

export type NavItem = { id: string; label: string; path: string[]; icon: LucideIcon; keys: string; desc: string };
export type NavGroup = { id: string; label: string; items: NavItem[] };

export const NAV: NavGroup[] = [
	{
		id: "work",
		label: "工作台",
		items: [
			{ id: "overview", label: "总览", path: ["overview"], icon: Gauge, keys: "g o", desc: "关键指标, 最近使用, 待处理" },
			{ id: "browsers", label: "浏览器", path: ["browsers"], icon: Monitor, keys: "g b", desc: "全部环境, 分组, 批量操作" },
			{ id: "live", label: "实时预览", path: ["live"], icon: MonitorPlay, keys: "g l", desc: "在跑浏览器的画面墙与远程操作" },
			{ id: "trash", label: "最近删除", path: ["trash"], icon: Trash2, keys: "g t", desc: "恢复或彻底删除环境" },
		],
	},
	{
		id: "res",
		label: "资源",
		items: [
			{ id: "proxies", label: "代理", path: ["resources", "proxies"], icon: Network, keys: "g p", desc: "代理库, 批量导入与检测" },
			{ id: "extensions", label: "扩展", path: ["resources", "extensions"], icon: Puzzle, keys: "g e", desc: "扩展目录与绑定" },
			{ id: "templates", label: "模板", path: ["resources", "templates"], icon: LayoutTemplate, keys: "g m", desc: "指纹模板, 一键套用或批量新建" },
		],
	},
	{
		id: "sys",
		label: "系统",
		items: [
			{ id: "status", label: "服务状态", path: ["system", "status"], icon: Activity, keys: "g s", desc: "全局链路, 趋势, 内核" },
			{ id: "kernels", label: "内核", path: ["system", "kernels"], icon: Cpu, keys: "g k", desc: "内核路径, 默认内核, 能力与检测" },
			{ id: "access", label: "外部接入", path: ["system", "access"], icon: PlugZap, keys: "g a", desc: "Agent 密钥, 项目名, 示例, MCP" },
			{ id: "logs", label: "操作日志", path: ["system", "logs"], icon: ScrollText, keys: "g g", desc: "后端记录的操作" },
			{ id: "orphans", label: "孤儿进程", path: ["system", "orphans"], icon: Ghost, keys: "g x", desc: "失联进程清理" },
			{ id: "settings", label: "设置", path: ["system", "settings"], icon: Settings2, keys: "g ,", desc: "路径, 端口, 默认行为" },
		],
	},
];

export const NAV_ITEMS: NavItem[] = NAV.flatMap((g) => g.items);
export const RESOURCES_ICON = Boxes;

/** 当前路由命中的导航项: 先比两段, 再比一段; 都不中回总览. */
export function activeItem(path: string[]): NavItem {
	const two = NAV_ITEMS.find((i) => i.path.length === 2 && i.path[0] === path[0] && i.path[1] === path[1]);
	if (two) return two;
	const one = NAV_ITEMS.find((i) => i.path[0] === path[0]);
	return one ?? NAV_ITEMS[0];
}

/** 导航项所在分组. */
export function groupOf(item: NavItem): NavGroup {
	return NAV.find((g) => g.items.includes(item)) ?? NAV[0];
}
