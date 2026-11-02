/** 详情抽屉的 缓存 与 账号包 两页. 写操作 (挂载, 卸载, 清理, 导出) 在只读环境上全部 disabled. */
import { useCallback, useState, type JSX } from "react";
import { Download, Eraser, Play, Square } from "lucide-react";
import { Badge, Checkbox, Input, confirmDialog, toast } from "@hx/ui";
import { cacheStats, hxcpStatus, type Env } from "../../lib/api";
import { detailApi, downloadJson } from "../../lib/api-res";
import { fmtBytes, fmtPct, hitRate } from "../../lib/chain";
import { usePoll } from "../../lib/hooks";
import { KV } from "./ChainParts";
import { Section, WriteButton } from "./DetailParts";

const CLEAR_TYPES = [
	{ id: "cache", label: "HTTP 缓存" },
	{ id: "localstorage", label: "localStorage" },
	{ id: "indexeddb", label: "IndexedDB" },
	{ id: "cookies", label: "Cookies" },
	{ id: "history", label: "历史" },
] as const;

const HXCP_TONE: Record<string, "success" | "secondary" | "destructive"> = { running: "success", absent: "secondary", dead: "destructive" };
const HXCP_TEXT: Record<string, string> = { running: "已挂载", absent: "未挂载", dead: "进程已死" };

/** 缓存页: 本环境的 hxcp 挂载实况与启停, 浏览器内缓存清理, 以及 8898 全局计数 (本环境计数后端尚未上报). */
export function CacheTab(props: { env: Env; ro: boolean }): JSX.Element {
	const id = props.env.id;
	const h = usePoll(useCallback((s: AbortSignal) => hxcpStatus(id, s), [id]));
	const c = usePoll(cacheStats, 10000);
	const [busy, setBusy] = useState(false);
	const [types, setTypes] = useState<Set<string>>(new Set(["cache"]));
	const st = h.result?.ok ? h.result.data : null;
	const cs = c.result?.ok ? c.result.data : null;

	const run = async (label: string, fn: () => Promise<{ ok: boolean; error?: string }>): Promise<void> => {
		setBusy(true);
		const r = await fn();
		setBusy(false);
		if (r.ok) toast.success(label + " 完成");
		else toast.error(label + " 失败: " + (r.error ?? ""));
		h.reload();
	};
	const clear = async (): Promise<void> => {
		const list = [...types];
		const yes = await confirmDialog({
			title: "清理 " + props.env.name + " 的浏览器数据?",
			message: "将清理: " + list.join(", "),
			hint: "在跑的浏览器里执行, 不可撤销.",
			confirmLabel: "清理",
			variant: "danger",
		});
		if (yes) await run("清理缓存", () => detailApi.clearCache(id, list));
	};
	const toggle = (t: string, on: boolean): void =>
		setTypes((cur) => {
			const n = new Set(cur);
			if (on) n.add(t);
			else n.delete(t);
			return n;
		});

	return (
		<div className="flex flex-col gap-3">
			<Section
				title="缓存代理 (hxcp)"
				right={st ? <Badge variant={HXCP_TONE[st.status] ?? "secondary"}>{HXCP_TEXT[st.status] ?? st.status}</Badge> : null}
			>
				<KV
					rows={[
						["挂载实况", h.result && !h.result.ok ? "不可达: " + h.result.error : st ? HXCP_TEXT[st.status] ?? st.status : null],
						["进程", st?.pid ? "pid " + st.pid : null],
						["端点", st?.endpoint ?? null],
						["随启动挂载", props.env.hxcp_enabled ? "开" : "关"],
						["本环境命中率", null],
						["本环境省流", null],
					]}
				/>
				<div className="flex flex-wrap gap-2">
					<WriteButton ro={props.ro} busy={busy || st?.status === "running"} onClick={() => void run("挂载缓存", () => detailApi.hxcpStart(id))}>
						<Play className="size-4" />挂载
					</WriteButton>
					<WriteButton ro={props.ro} busy={busy || st?.status !== "running"} onClick={() => void run("卸载缓存", () => detailApi.hxcpStop(id))}>
						<Square className="size-4" />卸载
					</WriteButton>
				</div>
			</Section>
			<Section title="8898 全局计数">
				<KV
					rows={[
						["引擎", cs ? cs.engine_state ?? null : c.result && !c.result.ok ? "不可达: " + c.result.error : null],
						["命中率", cs?.engine ? fmtPct(hitRate(cs)) + " (" + (cs.engine.hits ?? 0) + " 中 / " + (cs.engine.misses ?? 0) + " 未中)" : null],
						["省流", cs?.engine ? fmtBytes(cs.engine.bytes_served) + " 由缓存返回" : null],
						["走网络", cs?.engine ? fmtBytes(cs.engine.bytes_network) : null],
						["缓存库", cs?.store ? (cs.store.entries ?? 0) + " 条, " + fmtBytes(cs.store.bytes) : null],
					]}
				/>
				<p className="t-aux text-muted-foreground">本环境的命中与省流写在它自己的缓存目录, 8325 尚未上报; 这里是全局缓存服务的数字.</p>
			</Section>
			<Section title="清理浏览器数据">
				<div className="flex flex-wrap gap-x-4 gap-y-2 t-aux">
					{CLEAR_TYPES.map((t) => (
						<label key={t.id} className="flex items-center gap-2">
							<Checkbox checked={types.has(t.id)} disabled={props.ro} onCheckedChange={(v) => toggle(t.id, v === true)} />
							{t.label}
						</label>
					))}
				</div>
				<div>
					<WriteButton ro={props.ro} busy={busy || types.size === 0} variant="destructive" onClick={() => void clear()}>
						<Eraser className="size-4" />清理所选
					</WriteButton>
				</div>
			</Section>
		</div>
	);
}

/** 账号包页: 导出在跑浏览器的登录态 (cookie, 存储, 指纹) 为 JSON 文件, 可选口令加密. */
export function PackTab(props: { env: Env; ro: boolean }): JSX.Element {
	const [password, setPassword] = useState("");
	const [busy, setBusy] = useState(false);
	const [last, setLast] = useState<{ at: number; bytes: number } | null>(null);
	const running = !!props.env.runtime;
	const exportPack = async (): Promise<void> => {
		setBusy(true);
		const r = await detailApi.exportPack(props.env.id, password);
		setBusy(false);
		if (!r.ok) {
			toast.error("导出失败: " + r.error);
			return;
		}
		const text = JSON.stringify(r.data);
		downloadJson(props.env.name + ".hxpack.json", r.data);
		setLast({ at: Date.now(), bytes: text.length });
		toast.success("账号包已下载");
	};
	return (
		<Section title="账号包">
			<KV
				rows={[
					["环境状态", running ? "运行中, 可导出" : "未运行, 导出要先启动"],
					["本次导出", last ? new Date(last.at).toLocaleString() + ", " + fmtBytes(last.bytes) : null],
				]}
			/>
			<label className="flex flex-col gap-1 t-aux text-muted-foreground">
				口令 (可选, 留空不加密)
				<Input type="password" autoComplete="new-password" value={password} disabled={props.ro} onChange={(e) => setPassword(e.target.value)} />
			</label>
			<div>
				<WriteButton ro={props.ro} busy={busy} variant="default" onClick={() => void exportPack()}>
					<Download className="size-4" />{busy ? "导出中" : "导出并下载"}
				</WriteButton>
			</div>
		</Section>
	);
}
