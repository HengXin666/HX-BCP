/** 代理的新建与编辑表单. 顶部可粘贴整行代理串, 解析后填进下面各字段; 编辑时密码留空表示不改. */
import { useEffect, useState, type JSX } from "react";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@hx/ui";
import type { ProxyBody, ProxyItem } from "../../lib/api-res";
import { parseProxyLine } from "../../lib/proxy-parse";
import { Row } from "./ResParts";

export type ProxyTarget = { mode: "create" } | { mode: "edit"; item: ProxyItem } | null;

const TYPES = ["http", "https", "socks5"];
const BLANK: ProxyBody = { label: "", type: "http", host: "", port: 0, username: "", password: "" };

export function ProxyDialog(props: { target: ProxyTarget; busy: boolean; onClose: () => void; onSubmit: (body: ProxyBody) => void }): JSX.Element {
	const t = props.target;
	const [v, setV] = useState<ProxyBody>(BLANK);
	const [line, setLine] = useState("");
	const [lineErr, setLineErr] = useState("");
	useEffect(() => {
		setLine("");
		setLineErr("");
		if (t?.mode === "edit") setV({ ...BLANK, ...t.item, username: t.item.username ?? "", password: "" });
		else setV(BLANK);
	}, [t]);

	const paste = (text: string): void => {
		setLine(text);
		if (!text.trim()) return setLineErr("");
		const p = parseProxyLine(text);
		if (!p) return setLineErr("认不出这一行, 支持 type://user:pass@host:port, host:port, host:port:user:pass");
		setLineErr("");
		setV((cur) => ({ ...cur, ...p }));
	};
	const set = <K extends keyof ProxyBody>(k: K, val: ProxyBody[K]): void => setV((cur) => ({ ...cur, [k]: val }));
	const valid = !!v.host && v.port > 0 && v.port <= 65535;
	const edit = t?.mode === "edit";

	return (
		<Dialog open={!!t} onOpenChange={(o) => (o ? undefined : props.onClose())}>
			<DialogContent className="sm:max-w-lg" data-proxy-dialog="">
				<DialogHeader>
					<DialogTitle>{edit ? "编辑代理" : "新建代理"}</DialogTitle>
					<DialogDescription>保存到代理库, 新建或编辑浏览器时可以从库里选.</DialogDescription>
				</DialogHeader>
				<div className="flex flex-col gap-3">
					<Row label="粘贴整行 (可选)" hint={lineErr || undefined}>
						<Input value={line} placeholder="socks5://user:pass@1.2.3.4:1080" onChange={(e) => paste(e.target.value)} aria-invalid={!!lineErr} />
					</Row>
					<Row label="名称">
						<Input value={v.label} placeholder="可留空" onChange={(e) => set("label", e.target.value)} />
					</Row>
					<div className="grid grid-cols-[7rem_minmax(0,1fr)_6rem] gap-2">
						<Row label="类型">
							<Select value={v.type} onValueChange={(x) => set("type", x)}>
								<SelectTrigger><SelectValue /></SelectTrigger>
								<SelectContent>
									{TYPES.map((x) => <SelectItem key={x} value={x}>{x}</SelectItem>)}
								</SelectContent>
							</Select>
						</Row>
						<Row label="主机">
							<Input value={v.host} onChange={(e) => set("host", e.target.value.trim())} />
						</Row>
						<Row label="端口">
							<Input inputMode="numeric" value={v.port ? String(v.port) : ""} onChange={(e) => set("port", Number(e.target.value.replace(/\D/g, "")) || 0)} />
						</Row>
					</div>
					<div className="grid grid-cols-2 gap-2">
						<Row label="用户名">
							<Input value={v.username} autoComplete="off" onChange={(e) => set("username", e.target.value)} />
						</Row>
						<Row label="密码">
							<Input type="password" autoComplete="new-password" value={v.password} placeholder={edit ? "留空不改" : ""} onChange={(e) => set("password", e.target.value)} />
						</Row>
					</div>
				</div>
				<DialogFooter>
					<Button variant="outline" onClick={props.onClose}>取消</Button>
					<Button disabled={!valid || props.busy} onClick={() => props.onSubmit(v)}>{props.busy ? "保存中" : "保存"}</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
