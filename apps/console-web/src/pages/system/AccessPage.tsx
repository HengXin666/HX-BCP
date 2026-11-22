/**
 * 外部接入 #/system/access: Agent 通道怎么接, 能调什么, 人类通道怎么守. 数据只来自 GET /access.
 * 规则一句话: 带 x-api-key 的就是 Agent (必须带项目名, 只能动自己项目的环境), 人类只认控制台会话或本机令牌.
 */
import { useState, type JSX } from "react";
import { Copy, Eye, EyeOff, KeyRound, RotateCw, ShieldCheck } from "lucide-react";
import { Badge, Button, Card, CardContent, EmptyState, InputField, Switch, confirmDialog, toast } from "@hx/ui";
import { PageLoading } from "../PageLoading";
import { accessApi, accessInfo, snippets } from "../../lib/access";
import { usePoll } from "../../lib/hooks";

type Tab = "curl" | "python" | "mcp";
const TABS: { id: Tab; label: string }[] = [
	{ id: "curl", label: "curl" },
	{ id: "python", label: "Python" },
	{ id: "mcp", label: "MCP 客户端" },
];

async function copy(text: string, what: string): Promise<void> {
	try {
		await navigator.clipboard.writeText(text);
		toast.success("已复制" + what);
	} catch {
		toast.error("浏览器不允许写剪贴板, 请手动选中复制");
	}
}

function Block(props: { title: string; children: JSX.Element | JSX.Element[]; aside?: JSX.Element }): JSX.Element {
	return (
		<Card variant="solid">
			<CardContent className="flex flex-col gap-3 p-4">
				<div className="flex flex-wrap items-center gap-2">
					<span className="t-card">{props.title}</span>
					<span className="ml-auto flex items-center gap-2">{props.aside}</span>
				</div>
				{props.children}
			</CardContent>
		</Card>
	);
}

export function AccessPage(): JSX.Element {
	const res = usePoll(accessInfo, 60000);
	const [reveal, setReveal] = useState(false);
	const [tab, setTab] = useState<Tab>("curl");
	const [project, setProject] = useState("");
	if (res.result && !res.result.ok) return <EmptyState text="未连接" hint={"GET /api/v1/access 失败: " + res.result.error} />;
	if (!res.result) return <PageLoading />;
	const a = res.result.data;
	const code = snippets(a, project.trim(), reveal);
	const masked = a.api_key ? a.api_key.slice(0, 4) + "\u2022".repeat(Math.max(4, a.api_key.length - 8)) + a.api_key.slice(-4) : "未设置";

	const rotate = async (): Promise<void> => {
		const yes = await confirmDialog({ title: "更换 API 密钥?", message: "旧密钥立即失效, 所有 Agent 与 MCP 配置都要换成新的.", confirmLabel: "更换", cancelLabel: "取消", variant: "danger" });
		if (!yes) return;
		const r = await accessApi.rotate();
		if (r.ok) (toast.success("密钥已更换"), res.reload());
		else toast.error("更换失败: " + r.error);
	};
	const loopback = async (on: boolean): Promise<void> => {
		if (on) {
			const yes = await confirmDialog({ title: "本机回环直接算人类?", message: "打开后本机任何进程 (包括本机跑的 Agent 与脚本) 只要声明人类通道就能操作你的浏览器, 不再需要令牌.", hint: "只建议在单用户机器, 且本机没有跑任何 Agent 时打开.", confirmLabel: "仍然打开", cancelLabel: "保持关闭", variant: "danger" });
			if (!yes) return;
		}
		const r = await accessApi.setLoopback(on);
		if (r.ok) (toast.success(on ? "本机回环已算人类" : "本机回环不再算人类"), res.reload());
		else toast.error(r.error);
	};

	return (
		<div className="flex min-w-0 flex-col gap-4" data-sys-page="access">
			<Block title="Agent 通道" aside={<Badge variant="success"><ShieldCheck className="size-3" />必须带密钥 + 项目名</Badge>}>
				<div className="grid gap-2 sm:grid-cols-[8rem_minmax(0,1fr)] sm:items-center">
					<span className="t-aux text-muted-foreground">接口地址</span>
					<span className="flex min-w-0 items-center gap-2"><code className="truncate font-mono t-aux">{a.base + "/api/v1"}</code><Button size="sm" variant="ghost" onClick={() => void copy(a.base + "/api/v1", "地址")}><Copy className="size-4" /></Button></span>
					<span className="t-aux text-muted-foreground">x-api-key</span>
					<span className="flex min-w-0 flex-wrap items-center gap-2">
						<code className="truncate font-mono t-aux" data-access-key="">{reveal ? a.api_key : masked}</code>
						<Button size="sm" variant="ghost" onClick={() => setReveal(!reveal)}>{reveal ? <EyeOff className="size-4" /> : <Eye className="size-4" />}</Button>
						<Button size="sm" variant="ghost" onClick={() => void copy(a.api_key, "密钥")}><Copy className="size-4" /></Button>
						<Button size="sm" variant="outline" onClick={() => void rotate()}><KeyRound className="size-4" />更换</Button>
					</span>
					<span className="t-aux text-muted-foreground">x-bcp-project</span>
					<span className="t-aux">{"每个请求必填, 规则 " + a.project_rule + "; 环境按项目分组, 互相看不见"}</span>
					<span className="t-aux text-muted-foreground">MCP</span>
					<span className="flex min-w-0 items-center gap-2"><code className="truncate font-mono t-aux">{"POST " + a.mcp.url}</code><span className="t-aux text-muted-foreground">{a.mcp.tools.length + " 个工具"}</span></span>
				</div>
			</Block>

			<Block title="接入示例" aside={<InputField className="w-40" value={project} onChange={setProject} placeholder="项目名, 如 crawler-a" aria-label="示例项目名" />}>
				<div className="flex gap-1" role="tablist">
					{TABS.map((t) => <Button key={t.id} size="sm" variant={tab === t.id ? "primary" : "ghost"} role="tab" aria-selected={tab === t.id} onClick={() => setTab(t.id)}>{t.label}</Button>)}
					<Button size="sm" variant="outline" className="ml-auto" onClick={() => void copy(snippets(a, project.trim(), true)[tab], "示例 (含真实密钥)")}><Copy className="size-4" />复制</Button>
				</div>
				<pre className="max-h-72 overflow-auto rounded-md border border-border-muted bg-muted/40 p-3 font-mono t-aux" data-access-snippet={tab}>{code[tab]}</pre>
			</Block>

			<Block title="通道规则">
				<ul className="flex list-disc flex-col gap-1 pl-5 t-aux">
					{a.rules.map((r) => <li key={r}>{r}</li>)}
				</ul>
				<details className="t-aux">
					<summary className="cursor-pointer text-muted-foreground">{"Agent 可调的接口 (" + a.agent_paths.length + ")"}</summary>
					<div className="mt-2 grid gap-1 font-mono sm:grid-cols-2">{a.agent_paths.map((p) => <code key={p} className="truncate">{p}</code>)}</div>
				</details>
			</Block>

			<Block title="人类通道" aside={<Button size="sm" variant="ghost" onClick={res.reload}><RotateCw className="size-4" /></Button>}>
				<p className="t-aux text-muted-foreground">控制台用本机令牌或 hxb serve 打印的 10 分钟解锁链接换 12 小时会话; 带 x-api-key 的请求即使声明 human 也会被拒 (403).</p>
				<label className="flex items-center gap-2 t-body">
					<Switch checked={a.human_loopback} onCheckedChange={(v) => void loopback(v)} />
					本机回环直接算人类 (不推荐, 默认关)
				</label>
			</Block>
		</div>
	);
}
