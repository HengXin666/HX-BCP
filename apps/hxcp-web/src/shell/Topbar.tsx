/** 顶栏: 品牌, 引擎状态点 (运行实例数), 缓存规模与累计已省, 主题切换. */
import type { JSX } from "react";
import { Monitor, Moon, Sun } from "lucide-react";
import { Button, StateDot, Tooltip, TooltipContent, TooltipTrigger } from "@hx/ui";
import type { Stats } from "../lib/api";
import { bytes } from "../lib/format";
import { THEME_LABEL, useTheme, type ThemeMode } from "../lib/theme";

const NEXT: Record<ThemeMode, ThemeMode> = { dark: "light", light: "system", system: "dark" };
const ICON: Record<ThemeMode, typeof Sun> = { dark: Moon, light: Sun, system: Monitor };

export function Topbar(props: { stats: Stats | null; error: string }): JSX.Element {
	const { mode, setMode } = useTheme();
	const s = props.stats;
	const on = s?.engine_state === "running";
	const tone = props.error ? "error" : on ? "ok" : "idle";
	const state = props.error ? "API 不可达" : on ? "运行中 " + (s?.running ?? 1) + " 个实例" : "引擎 " + (s?.engine_state ?? "-");
	const Icon = ICON[mode];
	return (
		<header className="topbar sticky top-0 z-30 flex h-14 shrink-0 items-center gap-3 border-b border-border-muted px-3 md:px-6">
			<div className="flex items-baseline gap-2">
				<span className="t-section">hxcp</span>
				<span className="t-aux text-muted-foreground">缓存代理</span>
			</div>
			<div className="ml-auto flex items-center gap-2">
				<span className="inline-flex h-8 items-center gap-2 rounded-full border border-border px-3 t-aux text-muted-foreground" title={props.error || state}>
					<StateDot tone={tone} label={state} pulse={tone === "ok"} />
					<span className="tabular-nums">{state}</span>
				</span>
				{s ? (
					<span className="hidden h-8 items-center gap-2 rounded-full border border-border px-3 t-aux text-muted-foreground md:inline-flex">
						缓存 <b className="text-foreground tabular-nums">{s.store.entries}</b> 条 / {bytes(s.store.bytes)}
						<span className="text-border">|</span>
						累计已省 <b className="text-foreground tabular-nums">{bytes(s.store.bytes_served)}</b>
					</span>
				) : null}
				<Tooltip>
					<TooltipTrigger asChild>
						<Button size="icon-sm" variant="ghost" onClick={() => setMode(NEXT[mode])} aria-label="切换主题">
							<Icon className="size-4" />
						</Button>
					</TooltipTrigger>
					<TooltipContent>主题: {THEME_LABEL[mode]}</TooltipContent>
				</Tooltip>
			</div>
		</header>
	);
}
