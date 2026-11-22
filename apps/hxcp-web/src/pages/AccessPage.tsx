/**
 * 接入: 代理入口, 每个客户端的端口与上游, 各种客户端的现成配置, CA 与信任步骤, Agent 的 HTTP 与 MCP.
 * 片段全部来自 GET /access, 与 MCP proxy_access 同一份.
 */
import type { JSX } from "react";
import { Copy } from "lucide-react";
import {
	Badge,
	Button,
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
	DataTable,
	EmptyState,
	PageHeader,
	StatCard,
	StatGrid,
	Stepper,
	toast,
} from "@hx/ui";
import { api, type Listener } from "../lib/api";
import { copyText } from "../lib/format";
import { usePoll } from "../lib/hooks";
import { Snippet } from "./Snippet";
import { Block } from "./Block";

const SNIPPETS: [string, string, string][] = [
	["chromium", "Chrome / Edge / Brave", "bash"],
	["firefox", "Firefox (about:config)", "text"],
	["playwright", "Playwright", "python"],
	["puppeteer", "Puppeteer", "js"],
	["curl", "curl", "bash"],
	["env", "环境变量", "bash"],
	["system", "系统代理 (Safari 等)", "bash"],
];

const CA_STEPS = [
	{ title: "下载本地 CA", description: "只有切到解密的 host 才用得到它" },
	{ title: "Chrome / Edge", description: "设置, 隐私和安全, 管理证书, 授权机构, 导入" },
	{ title: "Firefox", description: "设置, 证书, 查看证书, 证书颁发机构, 导入并勾选信任网站" },
	{ title: "macOS / Safari", description: "导入钥匙串, 在钥匙串访问里设为始终信任" },
	{ title: "Linux 系统级", description: "放进 /usr/local/share/ca-certificates 后 update-ca-certificates" },
];

function urlOf(l: Listener): string {
	return (l.mode === "socks5" ? "socks5://" : "http://") + l.host + ":" + l.port;
}

export function AccessPage(): JSX.Element {
	const acc = usePoll(api.access, 10000);
	const a = acc.data;
	if (!a) return <EmptyState kind={acc.error ? "error" : "loading"} text={acc.error || "读取接入信息"} />;
	const here = new URL(".", window.location.href).href;
	const copy = (t: string): void => void copyText(t).then((ok) => (ok ? toast.success("已复制") : toast.error("复制失败")));
	const nDec = a.rules.decrypt_all ? "全部 (调试)" : a.rules.decrypt.length + " 个 host";

	return (
		<div className="flex min-w-0 flex-col gap-6">
			<PageHeader title="接入" desc="任何能配代理的浏览器, 程序, Agent 指向下面的入口即可, 所有入口共用一份缓存." />
			<StatGrid columns={3}>
				<StatCard
					label="代理入口"
					value={<span className="font-mono">{a.running ? a.proxy_url : "未启动"}</span>}
					hint={a.running ? "HTTP 代理, 任意浏览器与程序可用" : "先在宿主上启动 hxcp proxy"}
					tone={a.running ? "good" : "bad"}
					extra={a.running ? <Button size="xs" variant="ghost" onClick={() => copy(a.proxy_url)}><Copy className="size-3.5" /></Button> : undefined}
				/>
				<StatCard label="HTTPS 解密" value={a.decrypting ? nDec : "无"} hint={a.decrypting ? "这些 host 的上游握手会变" : "全部隧道, TLS 指纹与直连相同"} tone={a.decrypting ? "accent" : "good"} />
				<StatCard label="本地 CA" value={a.ca.exists ? "已生成" : "未生成"} hint={a.ca.path} tone={a.ca.exists ? "default" : "muted"} />
			</StatGrid>

			{!a.running ? (
				<Block title="启动代理" desc="每个浏览器一个入口可加 --client 名字=端口[,上游代理], 抓包按客户端区分">
					<Snippet code={a.snippets.start ?? ""} />
				</Block>
			) : null}

			<Block title="入口列表" desc="SOCKS5 入口不能叠上游出口, 需要上游时用 HTTP 入口">
				<DataTable<Listener>
					rows={a.listeners}
					rowKey={(l) => l.name + ":" + l.port}
					empty={<EmptyState compact text="没有运行中的入口" />}
					columns={[
						{ key: "name", header: "客户端", render: (l) => <span className="font-mono">{l.name}</span> },
						{ key: "mode", header: "协议", width: "96px", render: (l) => <Badge variant="secondary">{l.mode}</Badge> },
						{ key: "url", header: "地址", render: (l) => <span className="font-mono">{urlOf(l)}</span> },
						{ key: "upstream", header: "上游出口", render: (l) => <span className="t-aux">{(l.upstream || "直连") + (l.upstream_auth ? " (带认证)" : "")}</span> },
						{
							key: "copy",
							header: "",
							width: "56px",
							render: (l) => (
								<Button size="icon-xs" variant="ghost" onClick={() => copy(urlOf(l))} aria-label="复制地址">
									<Copy className="size-3.5" />
								</Button>
							),
						},
					]}
				/>
			</Block>

			<Block title="接入方式">
				<div className="grid gap-3 lg:grid-cols-2">
					{SNIPPETS.filter(([k]) => a.snippets[k]).map(([k, title, lang]) => (
						<Snippet key={k} title={title} code={a.snippets[k]} />
					))}
					<Snippet title="PAC 自动配置" code={new URL("proxy.pac", here).href} />
				</div>
			</Block>

			<div className="grid gap-3 lg:grid-cols-2">
				<Card>
					<CardHeader>
						<CardTitle>证书 (只在解密时需要)</CardTitle>
						<CardDescription>
							{a.ca.exists ? (
								<a className="text-link underline-offset-4 hover:underline" href="ca.crt" download="hxcp-ca.pem">
									下载 hxcp-ca.pem
								</a>
							) : (
								"代理启动后自动生成"
							)}
						</CardDescription>
					</CardHeader>
					<CardContent>
						<Stepper orientation="vertical" current={-1} steps={CA_STEPS.map((s, i) => ({ id: String(i), title: s.title, description: s.description, status: "pending" as const }))} />
					</CardContent>
				</Card>
				<Card>
					<CardHeader>
						<CardTitle>给 Agent</CardTitle>
						<CardDescription>Agent 用自己的入口端口, 统计与人分开; 读写都走同一套 HTTP 或 MCP.</CardDescription>
					</CardHeader>
					<CardContent className="flex flex-col gap-3">
						<Snippet title="HTTP API" code={[here + "access", here + "flows?q=&client=", here + "flows.har", here + "hosts", "POST " + here + "rules/host {host, mode}"].join("\n")} />
						{a.snippets.mcp ? <Snippet title="MCP (stdio)" code={a.snippets.mcp} /> : null}
					</CardContent>
				</Card>
			</div>

			<Block title="三种档位怎么选">
				<div className="grid gap-3 lg:grid-cols-3">
					{(["tunnel", "decrypt", "cdp"] as const).map((k) => (
						<Card key={k} variant="muted">
							<CardContent className="pt-4 t-aux text-muted-foreground">{a.notes[k]}</CardContent>
						</Card>
					))}
				</div>
			</Block>
		</div>
	);
}
