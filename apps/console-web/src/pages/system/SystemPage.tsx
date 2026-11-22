/** 系统页: 六个子页 (状态, 内核, 外部接入, 日志, 孤儿进程, 设置) 用 NavTabs 切换, 子页名在 hash 路由第二段, 如 #/system/logs. */
import type { JSX } from "react";
import { Activity, Cpu, Ghost, PlugZap, ScrollText, Settings2 } from "lucide-react";
import { NavTabs } from "@hx/ui";
import { PageTitle } from "../PageTitle";
import { AccessPage } from "./AccessPage";
import { KernelsPage } from "./KernelsPage";
import { LogsPage } from "./LogsPage";
import { OrphansPage } from "./OrphansPage";
import { SettingsPage } from "./SettingsPage";
import { StatusTab } from "./StatusTab";
import { formatHash } from "../../lib/router";

const TABS = [
	{ id: "status", label: "状态", icon: Activity },
	{ id: "kernels", label: "内核", icon: Cpu },
	{ id: "access", label: "外部接入", icon: PlugZap },
	{ id: "logs", label: "日志", icon: ScrollText },
	{ id: "orphans", label: "孤儿进程", icon: Ghost },
	{ id: "settings", label: "设置", icon: Settings2 },
] as const;

type Tab = (typeof TABS)[number]["id"];

export function SystemPage(props: { sub: string | undefined; onSub?: (id: string) => void }): JSX.Element {
	const tab: Tab = TABS.some((t) => t.id === props.sub) ? (props.sub as Tab) : "status";
	return (
		<div className="flex min-w-0 flex-col gap-4">
			<PageTitle title="系统" desc="服务状态, 内核管理, 外部接入, 操作日志, 孤儿进程与全局设置" />
			<NavTabs items={TABS.map((t) => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={props.onSub ?? ((id: string) => (window.location.hash = formatHash(["system", id])))} />
			<div key={tab} data-sys-tab={tab} className="animate-in fade-in-0 duration-200">
				{tab === "status" ? <StatusTab /> : tab === "kernels" ? <KernelsPage /> : tab === "access" ? <AccessPage /> : tab === "logs" ? <LogsPage /> : tab === "orphans" ? <OrphansPage /> : <SettingsPage />}
			</div>
		</div>
	);
}
