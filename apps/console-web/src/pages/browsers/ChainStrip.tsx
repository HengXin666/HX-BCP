/** 行内链路条: 浏览器 - 缓存 - 出口 - 目标 四个节点与三根连线, 一眼看出挂了哪几段, 哪段坏了. 点开看详情. */
import { Fragment, type JSX } from "react";
import { TooltipHint } from "@hx/ui";
import type { Seg } from "../../lib/chain";
import { STATE_TEXT } from "../../lib/chain";
import { SegNode, Wire } from "./ChainParts";

/** 悬停提示: 每段一行 "段名 状态: 当前值". */
function Hint(props: { segs: Seg[] }): JSX.Element {
	return (
		<div className="flex max-w-[22rem] flex-col gap-1 t-aux">
			{props.segs.map((s) => (
				<span key={s.key} className="truncate">
					<b className="font-medium">{s.name}</b> {STATE_TEXT[s.state]}: {s.value}
				</span>
			))}
			<span className="pt-1 text-muted-foreground">点击查看每段的实测值</span>
		</div>
	);
}

export function ChainStrip(props: { segs: Seg[]; onOpen: () => void; label: string }): JSX.Element {
	const { segs } = props;
	const text = segs.map((s) => s.name + " " + STATE_TEXT[s.state]).join(", ");
	return (
		<TooltipHint label={<Hint segs={segs} />} side="left" delayDuration={200}>
			<button
				type="button"
				onClick={props.onOpen}
				className="flex items-center rounded-md p-1 transition-colors duration-150 hover:bg-accent/60 focus-visible:outline-2 focus-visible:outline-ring"
				aria-label={"查看链路 " + props.label + ": " + text}
				data-chain-strip=""
			>
				{segs.map((s, i) => (
					<Fragment key={s.key}>
						{i > 0 ? <Wire from={segs[i - 1].state} to={s.state} short /> : null}
						<SegNode k={s.key} state={s.state} size="xs" />
					</Fragment>
				))}
			</button>
		</TooltipHint>
	);
}
