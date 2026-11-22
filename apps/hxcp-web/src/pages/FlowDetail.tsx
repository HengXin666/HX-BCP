/** 抓包详情: 关键字段, 复制为 curl / fetch, 头 / 请求体 / 原始 JSON 页签. 敏感头标红, 它们是分析时最想看的. */
import { useEffect, useState, type JSX } from "react";
import { Copy, Filter } from "lucide-react";
import { Alert, AlertDescription, Badge, Button, Card, CardContent, CardHeader, CardTitle, EmptyState, Tabs, TabsContent, TabsList, TabsTrigger, toast } from "@hx/ui";
import { api, type Flow } from "../lib/api";
import { bytes, copyText, splitUrl } from "../lib/format";
import { Snippet } from "./Snippet";
import { asCurl, asFetch, sizeOf, tlsLabel } from "./flowView";

const HOT = new Set(["cookie", "authorization", "set-cookie"]);

function Headers({ obj }: { obj?: Record<string, string> }): JSX.Element {
	const keys = Object.keys(obj ?? {});
	if (!keys.length) return <EmptyState compact text="无" />;
	return (
		<dl className="grid grid-cols-[minmax(120px,32%)_1fr] gap-x-3 gap-y-1 font-mono t-aux">
			{keys.map((k) => (
				<div key={k} className="contents">
					<dt className={"break-all " + (HOT.has(k.toLowerCase()) ? "text-destructive" : "text-link")}>{k}</dt>
					<dd className="m-0 break-all">{obj?.[k]}</dd>
				</div>
			))}
		</dl>
	);
}

async function copy(text: string, what: string): Promise<void> {
	if (await copyText(text)) toast.success("已复制 " + what);
	else toast.error("复制失败");
}

export function FlowDetail(props: { flow: Flow | null; onHost: (h: string) => void; onClient: (c: string) => void }): JSX.Element {
	const r = props.flow;
	const [body, setBody] = useState<{ text: string; eof: boolean; got: number } | null>(null);
	useEffect(() => setBody(null), [r]);
	if (!r)
		return (
			<Card className="min-h-[320px]">
				<EmptyState text="选一条请求" hint="看头, 请求体, 复制成 curl 或 fetch 重放" />
			</Card>
		);
	const [tls, tone] = tlsLabel(r);
	const ref = r.body_ref;
	const fetchBody = (offset: number): void => {
		if (!ref?.sha256) return;
		api
			.flowBody(ref.sha256, offset)
			.then((b) => setBody((old) => ({ text: (offset && old ? old.text : "") + b.text, eof: b.eof, got: offset + b.returned })))
			.catch((e: unknown) => toast.error("取回失败: " + String(e)));
	};
	const kv: [string, React.ReactNode][] = [
		["时间", new Date(r.at * 1000).toLocaleString()],
		["客户端", (r.client ?? "-") + ", " + (r.via ?? "cdp") + " 档"],
		["状态 / 类型", (r.status || "-") + ", " + (r.type ?? r.mime ?? "-")],
		["判定", r.verdict ?? r.kind],
		["TLS", <span key="t" className="inline-flex items-center gap-2"><Badge variant={tone}>{tls}</Badge>{r.alpn?.join(", ")} {r.http_version}</span>],
		["大小 / 耗时", sizeOf(r) + ", " + (r.ms != null ? r.ms + " ms" : "-")],
	];
	return (
		<Card className="min-w-0 self-start xl:sticky xl:top-[calc(var(--topbar-h)+16px)]">
			<CardHeader className="gap-2">
				<CardTitle className="break-all font-mono t-body">
					<span className="mr-2 text-muted-foreground">{r.method ?? "GET"}</span>
					{r.url}
				</CardTitle>
				<div className="flex flex-wrap gap-2">
					<Button size="xs" variant="outline" onClick={() => void copy(asCurl(r), "curl")}>
						<Copy className="size-3.5" />curl
					</Button>
					<Button size="xs" variant="outline" onClick={() => void copy(asFetch(r), "fetch")}>
						<Copy className="size-3.5" />fetch
					</Button>
					<Button size="xs" variant="outline" onClick={() => void copy(r.url, "URL")}>
						<Copy className="size-3.5" />URL
					</Button>
					<Button size="xs" variant="ghost" onClick={() => props.onHost(splitUrl(r.url)[0].split(":")[0])}>
						<Filter className="size-3.5" />只看这个 host
					</Button>
					{r.client ? (
						<Button size="xs" variant="ghost" onClick={() => props.onClient(r.client ?? "")}>
							<Filter className="size-3.5" />只看这个客户端
						</Button>
					) : null}
				</div>
			</CardHeader>
			<CardContent className="flex flex-col gap-3">
				<dl className="grid grid-cols-[96px_1fr] gap-x-3 gap-y-1 t-aux">
					{kv.map(([k, v]) => (
						<div key={k} className="contents">
							<dt className="text-muted-foreground">{k}</dt>
							<dd className="m-0 break-all font-mono">{v}</dd>
						</div>
					))}
				</dl>
				{r.kind === "tunnel" ? (
					<Alert variant="info">
						<AlertDescription>隧道连接: 浏览器自己与站点握手, 代理只转发字节, 看不到路径与内容. 想缓存或看明文, 去 主机与 TLS 把它切到解密.</AlertDescription>
					</Alert>
				) : null}
				<Tabs defaultValue="curl">
					<TabsList variant="underline">
						<TabsTrigger value="curl" variant="underline">curl</TabsTrigger>
						<TabsTrigger value="req" variant="underline">请求头</TabsTrigger>
						<TabsTrigger value="resp" variant="underline">响应头</TabsTrigger>
						<TabsTrigger value="body" variant="underline">请求体</TabsTrigger>
						<TabsTrigger value="raw" variant="underline">JSON</TabsTrigger>
					</TabsList>
					<TabsContent value="curl" className="pt-3">
						<Snippet code={asCurl(r)} />
					</TabsContent>
					<TabsContent value="req" className="pt-3">
						<Headers obj={r.req_headers} />
					</TabsContent>
					<TabsContent value="resp" className="pt-3">
						<Headers obj={r.resp_headers} />
					</TabsContent>
					<TabsContent value="body" className="flex flex-col gap-2 pt-3">
						{ref?.sha256 ? (
							<div className="flex items-center gap-2 t-aux text-muted-foreground">
								完整体 {bytes(ref.size)} 在取证池
								<Button size="xs" variant="outline" onClick={() => fetchBody(0)}>取回</Button>
								{body && !body.eof ? <Button size="xs" variant="ghost" onClick={() => fetchBody(body.got)}>续读</Button> : null}
							</div>
						) : null}
						{body || r.post_data ? (
							<Snippet code={body?.text ?? r.post_data ?? ""} />
						) : (
							<EmptyState compact text="这条请求没有请求体" />
						)}
					</TabsContent>
					<TabsContent value="raw" className="pt-3">
						<Snippet code={JSON.stringify(r, null, 2)} />
					</TabsContent>
				</Tabs>
			</CardContent>
		</Card>
	);
}
