/**
 * 顶栏 (吸顶, 半透明模糊底): 窄屏菜单按钮, 面包屑, 全局搜索入口 (打开命令面板), 服务连接指示, 主题切换, 快捷键帮助.
 * 连接指示读 GET /status: 通 = 绿点 + 版本 + 在跑数; 不通 = 红点 + 原因 (悬停看全文).
 */
import type { JSX } from "react";
import { Check, CircleHelp, Menu, Monitor, Moon, Search, Sun } from "lucide-react";
import {
	Breadcrumb,
	BreadcrumbItem,
	BreadcrumbList,
	BreadcrumbPage,
	BreadcrumbSeparator,
	Button,
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuTrigger,
	Kbd,
	KbdGroup,
	StateDot,
} from "@hx/ui";
import { MOD_LABEL } from "../lib/hotkeys";
import { THEME_LABEL, useTheme, type ThemeMode } from "../lib/theme";
import { groupOf, type NavItem } from "./nav";
import type { ShellStats } from "./useShellStats";

const THEME_ICON: Record<ThemeMode, typeof Sun> = { dark: Moon, light: Sun, system: Monitor };

function ConnPill({ stats }: { stats: ShellStats }): JSX.Element {
	const r = stats.svc;
	const tone = !r ? "idle" : r.ok ? "ok" : "error";
	const text = !r ? "连接中" : r.ok ? "v" + r.data.version + " · 在跑 " + r.data.running : "服务未连接";
	const title = !r ? "正在读取 8325 状态" : r.ok ? "hx-browser " + r.data.version + ", API :" + r.data.api_port + ", CDP 起始 :" + r.data.cdp_base_port : r.error;
	return (
		<span className="hidden h-8 items-center gap-2 rounded-full border border-border px-3 t-aux text-muted-foreground lg:inline-flex" title={title} data-conn={tone}>
			<StateDot tone={tone} label={text} pulse={tone === "ok"} />
			<span className="tabular-nums">{text}</span>
		</span>
	);
}

export function Topbar(props: { active: NavItem; stats: ShellStats; onMenu: () => void; onPalette: () => void; onHelp: () => void }): JSX.Element {
	const { mode, setMode } = useTheme();
	const ThemeIcon = THEME_ICON[mode];
	return (
		<header className="topbar sticky top-0 z-30 flex h-14 shrink-0 items-center gap-3 border-b border-border-muted px-3 md:px-6" data-topbar="">
			<Button size="icon-sm" variant="ghost" className="md:hidden" onClick={props.onMenu} aria-label="打开导航">
				<Menu className="size-4" />
			</Button>
			<Breadcrumb className="min-w-0">
				<BreadcrumbList>
					<BreadcrumbItem className="hidden sm:inline-flex">{groupOf(props.active).label}</BreadcrumbItem>
					<BreadcrumbSeparator className="hidden sm:inline-flex" />
					<BreadcrumbItem>
						<BreadcrumbPage className="truncate font-medium">{props.active.label}</BreadcrumbPage>
					</BreadcrumbItem>
				</BreadcrumbList>
			</Breadcrumb>
			<div className="ml-auto flex items-center gap-2">
				<button type="button" className="search-trigger hidden h-8 w-64 items-center gap-2 rounded-md border border-border px-3 t-aux text-muted-foreground sm:flex" onClick={props.onPalette} data-open-palette="">
					<Search className="size-4" />
					<span className="flex-1 text-left">搜索环境, 页面, 命令</span>
					<KbdGroup><Kbd>{MOD_LABEL}</Kbd><Kbd>K</Kbd></KbdGroup>
				</button>
				<Button size="icon-sm" variant="ghost" className="sm:hidden" onClick={props.onPalette} aria-label="搜索">
					<Search className="size-4" />
				</Button>
				<ConnPill stats={props.stats} />
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button size="icon-sm" variant="ghost" aria-label={"主题: " + THEME_LABEL[mode]} title={"主题: " + THEME_LABEL[mode]}>
							<ThemeIcon className="size-4" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end">
						<DropdownMenuLabel>主题</DropdownMenuLabel>
						{(Object.keys(THEME_LABEL) as ThemeMode[]).map((m) => {
							const Icon = THEME_ICON[m];
							return (
								<DropdownMenuItem key={m} onSelect={() => setMode(m)} data-theme-option={m}>
									<Icon className="size-4" />
									{THEME_LABEL[m]}
									{m === mode ? <Check className="ml-auto size-4" /> : null}
								</DropdownMenuItem>
							);
						})}
					</DropdownMenuContent>
				</DropdownMenu>
				<Button size="icon-sm" variant="ghost" onClick={props.onHelp} aria-label="快捷键" title="快捷键  ?">
					<CircleHelp className="size-4" />
				</Button>
			</div>
		</header>
	);
}
