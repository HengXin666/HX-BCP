/** 资源页: 三个子页 (代理, 扩展, 模板) 用 NavTabs 切换, 子页名在 hash 路由第二段, 如 #/resources/proxies. */
import type { JSX } from "react";
import { LayoutTemplate, Network, Puzzle } from "lucide-react";
import { NavTabs } from "@hx/ui";
import { PageTitle } from "../PageTitle";
import { ExtensionsPage } from "./ExtensionsPage";
import { ProxiesPage } from "./ProxiesPage";
import { TemplatesPage } from "./TemplatesPage";

export const RES_TABS = [
	{ id: "proxies", label: "代理", icon: Network },
	{ id: "extensions", label: "扩展", icon: Puzzle },
	{ id: "templates", label: "模板", icon: LayoutTemplate },
] as const;

export type ResTab = (typeof RES_TABS)[number]["id"];

export function resTabOf(sub: string | undefined): ResTab {
	return RES_TABS.some((t) => t.id === sub) ? (sub as ResTab) : "proxies";
}

export function ResourcesPage(props: { sub: string | undefined; onSub: (id: string) => void }): JSX.Element {
	const tab = resTabOf(props.sub);
	return (
		<div className="flex flex-col gap-4">
			<PageTitle title="资源" desc="代理, 扩展与指纹模板, 新建浏览器时可直接选用" />
			<NavTabs items={RES_TABS.map((t) => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={props.onSub} />
			<div key={tab} data-res-tab={tab} className="animate-in fade-in-0 duration-200">
				{tab === "proxies" ? <ProxiesPage /> : tab === "extensions" ? <ExtensionsPage /> : <TemplatesPage />}
			</div>
		</div>
	);
}
