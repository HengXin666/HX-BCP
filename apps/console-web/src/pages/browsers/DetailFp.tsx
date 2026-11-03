/** 详情抽屉的 指纹 与 Cookies 两页. 两者都要环境在跑 (经 CDP 实测), 不在跑时写后端给的原因与 "未上报". */
import { useCallback, useMemo, type JSX } from "react";
import { Download, RotateCw } from "lucide-react";
import { Badge, Button, DataTable, EmptyState, type DataTableColumn } from "@hx/ui";
import type { Env, EnvFingerprint } from "../../lib/api";
import { detailApi, downloadJson, type CookieItem, type FpObserved } from "../../lib/api-res";
import { usePoll } from "../../lib/hooks";
import { Section } from "./DetailParts";

type Level = "ok" | "warn" | "bad" | "info";
type Item = { name: string; expect: string; got: string; level: Level; note: string };

const LEVEL_TEXT: Record<Level, string> = { ok: "一致", warn: "不一致", bad: "暴露", info: "无预期" };
const LEVEL_TONE: Record<Level, "success" | "warning" | "destructive" | "secondary"> = { ok: "success", warn: "warning", bad: "destructive", info: "secondary" };
const VERDICT: Record<string, { text: string; tone: "success" | "warning" | "destructive" }> = {
	green: { text: "绿 通过", tone: "success" },
	yellow: { text: "黄 待复核", tone: "warning" },
	red: { text: "红 阻断", tone: "destructive" },
};

const str = (v: unknown): string => (v === undefined || v === null || v === "" ? "" : String(v));

/** 一项比较: 没有预期值时只列实测 (info); 不一致时按 severe 定 bad 或 warn. */
function cmp(name: string, expect: string, got: string, severe: boolean, note = ""): Item {
	if (!got) return { name, expect: expect || "-", got: "未上报", level: "info", note };
	if (!expect) return { name, expect: "跟随内核", got, level: "info", note };
	const same = expect.trim().toLowerCase() === got.trim().toLowerCase();
	return { name, expect, got, level: same ? "ok" : severe ? "bad" : "warn", note };
}

/** 预期 (环境指纹配置) 与实测 (浏览器内 JS) 逐项对比. 规则写在这里一处, 判色只看这张表. */
export function compareFp(fp: EnvFingerprint, o: FpObserved): Item[] {
	const webglExpect = fp.webgl_vendor || fp.webgl_renderer ? str(fp.webgl_vendor) + "|" + str(fp.webgl_renderer) : "";
	return [
		cmp("User-Agent", str(fp.user_agent), str(o.ua), true, "UA 与配置不同会被直接识别"),
		cmp("语言", str(fp.language), str(o.language), false),
		cmp("时区", str(fp.timezone), str(o.tz), false, "实测取 Date 文本里的时区名, 与 IANA 名写法可能不同"),
		cmp("屏幕", str(fp.resolution), str(o.screen), false),
		cmp("CPU 核数", fp.hardware_concurrency ? String(fp.hardware_concurrency) : "", str(o.cores), false),
		cmp("设备内存", fp.device_memory ? String(fp.device_memory) : "", str(o.memory), false),
		cmp("WebGL", webglExpect, str(o.webgl), false),
		cmp("平台", "", str(o.platform), false),
		o.webdriver === undefined
			? { name: "webdriver", expect: "false", got: "未上报", level: "info", note: "" }
			: { name: "webdriver", expect: "false", got: String(o.webdriver), level: o.webdriver ? "bad" : "ok", note: "true 即自动化特征外露" },
	];
}

export function verdictOf(items: Item[]): "green" | "yellow" | "red" {
	if (items.some((i) => i.level === "bad")) return "red";
	if (items.some((i) => i.level === "warn")) return "yellow";
	return "green";
}

/** 指纹页: GET /fingerprint/check, 30 秒重测一次, 可手动重测. */
export function FingerprintTab(props: { env: Env }): JSX.Element {
	const id = props.env.id;
	const check = usePoll(useCallback((s: AbortSignal) => detailApi.fpCheck(id, s), [id]), 30000);
	const r = check.result;
	const items = useMemo(() => (r?.ok ? compareFp(props.env.fingerprint ?? {}, r.data ?? {}) : []), [r, props.env.fingerprint]);
	const v = r?.ok ? VERDICT[verdictOf(items)] : null;
	const reload = <Button size="sm" variant="outline" onClick={check.reload}><RotateCw className="size-4" />重新检测</Button>;
	if (!r) return <Section title="指纹实测">{<p className="t-aux text-muted-foreground">检测中</p>}</Section>;
	if (!r.ok) {
		return (
			<Section title="指纹实测" right={reload}>
				<EmptyState text="未上报" hint={r.error + " (实测要在跑的浏览器里执行)"} />
			</Section>
		);
	}
	return (
		<Section title="指纹实测" right={<>{v ? <Badge variant={v.tone} data-fp-verdict="">{v.text}</Badge> : null}{reload}</>}>
			<ul className="flex flex-col divide-y divide-border/50 t-aux" data-fp-items={items.length}>
				{items.map((i) => (
					<li key={i.name} className="flex flex-col gap-1 py-2" title={i.note || undefined}>
						<div className="flex items-center gap-2">
							<span className="font-medium">{i.name}</span>
							<Badge variant={LEVEL_TONE[i.level]} className="ml-auto">{LEVEL_TEXT[i.level]}</Badge>
						</div>
						<div className="grid grid-cols-[3rem_minmax(0,1fr)] gap-x-2 font-mono t-aux text-muted-foreground">
							<span>预期</span>
							<span className="truncate" title={i.expect}>{i.expect}</span>
							<span>实测</span>
							<span className={"truncate " + (i.level === "bad" || i.level === "warn" ? "text-foreground" : "")} title={i.got}>{i.got}</span>
						</div>
					</li>
				))}
			</ul>
		</Section>
	);
}

/** CDP 的 expires 是秒, -1 或 session 为会话 cookie. */
function expiry(c: CookieItem): string {
	if (c.session || !c.expires || c.expires < 0) return "会话";
	return new Date(c.expires * 1000).toLocaleString();
}

const COOKIE_COLS: DataTableColumn<CookieItem>[] = [
	{ key: "domain", header: "域名", cellClassName: "max-w-[10rem] truncate font-mono t-aux", render: (c) => <span title={c.domain}>{c.domain}</span> },
	{ key: "name", header: "名称", cellClassName: "max-w-[8rem] truncate font-mono t-aux", render: (c) => <span title={c.name}>{c.name}</span> },
	{ key: "expires", header: "过期", cellClassName: "whitespace-nowrap t-aux text-muted-foreground", render: expiry },
];

/** Cookies 页: 只读列表 (域名 / 名称 / 过期), 不显示值; 导出按钮把整份 (含值) 存成 JSON. */
export function CookiesTab(props: { env: Env }): JSX.Element {
	const id = props.env.id;
	const res = usePoll(useCallback((s: AbortSignal) => detailApi.cookies(id, s), [id]), 15000);
	const r = res.result;
	const list = r?.ok ? r.data?.cookies ?? [] : [];
	const sorted = useMemo(() => [...list].sort((a, b) => a.domain.localeCompare(b.domain) || a.name.localeCompare(b.name)), [list]);
	const exportBtn = (
		<Button size="sm" variant="outline" disabled={!r?.ok || list.length === 0} onClick={() => downloadJson(props.env.name + "-cookies.json", list)}>
			<Download className="size-4" />导出
		</Button>
	);
	return (
		<Section title={"Cookies" + (r?.ok ? " (" + list.length + ")" : "")} right={exportBtn}>
			{!r ? (
				<p className="t-aux text-muted-foreground">读取中</p>
			) : !r.ok ? (
				<EmptyState text="未上报" hint={r.error} />
			) : (
				<DataTable columns={COOKIE_COLS} rows={sorted} rowKey={(c, i) => c.domain + "|" + c.name + "|" + (c.path ?? "") + "|" + i} emptyText="没有 cookie" maxHeight="26rem" bare />
			)}
		</Section>
	);
}
