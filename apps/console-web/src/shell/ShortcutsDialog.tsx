/** 快捷键速查 (按 ? 打开): 全局, 跳转 (g 序列, 来自导航表), 浏览器页. */
import type { JSX } from "react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, Kbd, KbdGroup } from "@hx/ui";
import { MOD_LABEL } from "../lib/hotkeys";
import { NAV_ITEMS } from "./nav";

type Row = [string, string[]];

const GLOBAL: Row[] = [
	["命令面板 / 全局搜索", [MOD_LABEL, "K"]],
	["快捷键速查", ["?"]],
	["收起或展开侧栏", ["["]],
];
const BROWSERS: Row[] = [
	["聚焦搜索框", ["/"]],
	["新建浏览器", ["N"]],
	["切换表格 / 网格视图", ["V"]],
	["清空选择或关闭抽屉", ["Esc"]],
];

function Keys({ keys }: { keys: string[] }): JSX.Element {
	return <KbdGroup>{keys.map((k) => <Kbd key={k}>{k}</Kbd>)}</KbdGroup>;
}

function Section(props: { title: string; rows: Row[] }): JSX.Element {
	return (
		<section className="flex flex-col gap-1">
			<h3 className="pb-1 t-micro text-muted-foreground">{props.title}</h3>
			{props.rows.map(([label, keys]) => (
				<div key={label} className="flex h-8 items-center justify-between gap-3 rounded-md px-2 t-body hover:bg-accent/40">
					<span>{label}</span>
					<Keys keys={keys} />
				</div>
			))}
		</section>
	);
}

export function ShortcutsDialog(props: { open: boolean; onOpenChange: (v: boolean) => void }): JSX.Element {
	const jump: Row[] = NAV_ITEMS.map((i) => [i.label, i.keys.toUpperCase().split(" ")]);
	return (
		<Dialog open={props.open} onOpenChange={props.onOpenChange}>
			<DialogContent className="sm:max-w-2xl" data-shortcuts="">
				<DialogHeader>
					<DialogTitle>快捷键</DialogTitle>
					<DialogDescription>焦点在输入框里时只响应带 {MOD_LABEL} 的组合键. 跳转是两段序列: 先按 G, 再按对应字母.</DialogDescription>
				</DialogHeader>
				<div className="grid gap-6 sm:grid-cols-2">
					<div className="flex flex-col gap-6">
						<Section title="全局" rows={GLOBAL} />
						<Section title="浏览器页" rows={BROWSERS} />
					</div>
					<Section title="跳转" rows={jump} />
				</div>
			</DialogContent>
		</Dialog>
	);
}
