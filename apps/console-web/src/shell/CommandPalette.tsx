/**
 * 命令面板 (Ctrl/Cmd+K): 跳转到任一页, 执行常用操作 (新建浏览器, 导入, 新建代理, 切换主题), 按名称, id, 标签, 代理搜环境.
 * 选中环境 = 跳到浏览器页并打开它的详情抽屉; 环境列表只在面板打开时拉取.
 * "界面" 组放纯界面操作: 收起侧栏, 快捷键速查, 表格密度, 刷新全部数据 (作废会话读缓存, 在挂的页面立即重拉).
 */
import type { JSX } from "react";
import { FileArchive, FolderOpen, FolderPlus, Keyboard, ListPlus, Moon, PanelLeft, Plus, RefreshCw, Rows3, Sun } from "lucide-react";
import {
	CommandDialog,
	CommandEmpty,
	CommandGroup,
	CommandInput,
	CommandItem,
	CommandList,
	CommandSeparator,
	CommandShortcut,
	StateDot,
} from "@hx/ui";
import { listEnvs } from "../lib/api";
import { emit, type Command } from "../lib/bus";
import { invalidate } from "../lib/cache";
import { isRunning } from "../lib/env-query";
import { usePoll } from "../lib/hooks";
import { DENSITY_LABEL, type Density } from "../lib/prefs";
import { useTheme } from "../lib/theme";
import { NAV, type NavItem } from "./nav";

const MAX_ENVS = 40;
const DENSITIES: Density[] = ["comfy", "default", "compact"];

function Envs(props: { run: (c: Command, item: NavItem | null) => void; browsers: NavItem }): JSX.Element | null {
	const res = usePoll(listEnvs, 15000);
	const envs = res.result?.ok ? res.result.data : [];
	if (!envs.length) return null;
	const sorted = [...envs].sort((a, b) => Number(isRunning(b)) - Number(isRunning(a)) || (b.last_open_at ?? 0) - (a.last_open_at ?? 0));
	return (
		<CommandGroup heading={"浏览器环境 (" + envs.length + ")"}>
			{sorted.slice(0, MAX_ENVS).map((e) => (
				<CommandItem
					key={e.id}
					value={[e.name, e.id, e.folder, e.project, e.proxy?.host ?? "", ...(e.tags ?? [])].join(" ")}
					onSelect={() => props.run({ page: "browsers", kind: "open-env", id: e.id }, props.browsers)}
				>
					<StateDot tone={e.in_use ? "busy" : isRunning(e) ? "ok" : "idle"} label={isRunning(e) ? "运行中" : "未运行"} />
					<span className="min-w-0 flex-1 truncate font-mono">{e.name}</span>
					<CommandShortcut>{e.lane === "agent" ? "API " + e.project : e.folder || "根目录"}</CommandShortcut>
				</CommandItem>
			))}
		</CommandGroup>
	);
}

export function CommandPalette(props: {
	open: boolean;
	onOpenChange: (v: boolean) => void;
	go: (item: NavItem) => void;
	collapsed: boolean;
	onToggleSidebar: () => void;
	onHelp: () => void;
	density: Density;
	onDensity: (d: Density) => void;
}): JSX.Element {
	const { resolved, setMode } = useTheme();
	const byId = (id: string): NavItem => NAV.flatMap((g) => g.items).find((i) => i.id === id) as NavItem;
	const close = (): void => props.onOpenChange(false);
	const run = (c: Command | null, item: NavItem | null): void => {
		close();
		if (item) props.go(item);
		if (c) emit(c);
	};
	const browsers = byId("browsers");
	const proxies = byId("proxies");
	return (
		<CommandDialog open={props.open} onOpenChange={props.onOpenChange} title="命令面板" description="跳转页面, 执行操作, 搜索浏览器环境" className="sm:max-w-xl">
			<CommandInput placeholder="输入页面名, 操作, 或环境名称 / id / 标签" />
			<CommandList className="max-h-[420px]">
				<CommandEmpty>没有匹配项</CommandEmpty>
				<CommandGroup heading="操作">
					<CommandItem value="新建浏览器 new browser" onSelect={() => run({ page: "browsers", kind: "new-browser" }, browsers)}>
						<Plus />新建浏览器<CommandShortcut>N</CommandShortcut>
					</CommandItem>
					<CommandItem value="新建文件夹 folder" onSelect={() => run({ page: "browsers", kind: "new-folder" }, browsers)}>
						<FolderPlus />新建文件夹
					</CommandItem>
					<CommandItem value="导入 profile 目录 import" onSelect={() => run({ page: "browsers", kind: "import", what: "profile" }, browsers)}>
						<FolderOpen />导入 profile 目录
					</CommandItem>
					<CommandItem value="导入账号包 pack import" onSelect={() => run({ page: "browsers", kind: "import", what: "pack" }, browsers)}>
						<FileArchive />导入账号包
					</CommandItem>
					<CommandItem value="新建代理 proxy" onSelect={() => run({ page: "proxies", kind: "new-proxy" }, proxies)}>
						<Plus />新建代理
					</CommandItem>
					<CommandItem value="批量导入代理 bulk proxy" onSelect={() => run({ page: "proxies", kind: "bulk-proxy" }, proxies)}>
						<ListPlus />批量导入代理
					</CommandItem>
					<CommandItem value="切换主题 深色 浅色 theme" onSelect={() => (setMode(resolved === "dark" ? "light" : "dark"), close())}>
						{resolved === "dark" ? <Sun /> : <Moon />}切换到{resolved === "dark" ? "浅色" : "深色"}主题
					</CommandItem>
				</CommandGroup>
				<CommandSeparator />
				<CommandGroup heading="界面">
					<CommandItem value="收起 展开 侧栏 sidebar" onSelect={() => (props.onToggleSidebar(), close())}>
						<PanelLeft />{props.collapsed ? "展开侧栏" : "收起侧栏"}<CommandShortcut>[</CommandShortcut>
					</CommandItem>
					<CommandItem value="快捷键 速查 shortcuts help" onSelect={() => (close(), props.onHelp())}>
						<Keyboard />快捷键速查<CommandShortcut>?</CommandShortcut>
					</CommandItem>
					{DENSITIES.filter((d) => d !== props.density).map((d) => (
						<CommandItem key={d} value={"表格密度 行高 density " + d + " " + DENSITY_LABEL[d]} onSelect={() => (props.onDensity(d), close())}>
							<Rows3 />表格密度: {DENSITY_LABEL[d]}
						</CommandItem>
					))}
					<CommandItem value="刷新 全部数据 reload refresh" onSelect={() => (invalidate(""), close())}>
						<RefreshCw />刷新全部数据
					</CommandItem>
				</CommandGroup>
				<CommandSeparator />
				{NAV.map((g) => (
					<CommandGroup key={g.id} heading={"跳转 / " + g.label}>
						{g.items.map((i) => {
							const Icon = i.icon;
							return (
								<CommandItem key={i.id} value={i.label + " " + i.desc + " " + i.id} onSelect={() => run(null, i)}>
									<Icon />
									<span className="flex-1">{i.label}</span>
									<span className="hidden t-aux text-muted-foreground sm:inline">{i.desc}</span>
									<CommandShortcut>{i.keys.toUpperCase()}</CommandShortcut>
								</CommandItem>
							);
						})}
					</CommandGroup>
				))}
				<CommandSeparator />
				{props.open ? <Envs run={run} browsers={browsers} /> : null}
			</CommandList>
		</CommandDialog>
	);
}
