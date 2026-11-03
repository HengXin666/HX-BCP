/**
 * 导入对话框, 两种:
 * profile = POST /environments/import-profile (服务所在机器上的 Firefox profile 目录, 复制, 移动或原地引用);
 * pack = POST /account-packs/import (本机选一个账号包 JSON, 导出时设过口令就填口令).
 * 两者都新建一个人类组环境. 失败在对话框里写后端原文, 并弹错误提示.
 */
import { useEffect, useState, type JSX } from "react";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Input, SelectField, toast } from "@hx/ui";
import { importApi } from "../../lib/api-res";
import { Row } from "../resources/ResParts";

export type ImportKind = "profile" | "pack";

const MODES = [
	{ value: "copy", label: "复制 (原目录不动)" },
	{ value: "move", label: "移动 (原目录移走)" },
	{ value: "reference", label: "原地引用 (不复制)" },
];

export function ImportDialog(props: { kind: ImportKind | null; onClose: () => void; onDone: () => void }): JSX.Element {
	const kind = props.kind;
	const [dir, setDir] = useState("");
	const [mode, setMode] = useState("copy");
	const [name, setName] = useState("");
	const [pack, setPack] = useState<Record<string, unknown> | null>(null);
	const [file, setFile] = useState("");
	const [pass, setPass] = useState("");
	const [err, setErr] = useState("");
	const [busy, setBusy] = useState(false);
	useEffect(() => (setDir(""), setMode("copy"), setName(""), setPack(null), setFile(""), setPass(""), setErr("")), [kind]);

	const pick = async (f: File | undefined): Promise<void> => {
		setPack(null);
		setFile(f?.name ?? "");
		if (!f) return;
		try {
			const v = JSON.parse(await f.text()) as unknown;
			if (!v || typeof v !== "object" || Array.isArray(v)) throw new Error("不是 JSON 对象");
			setPack(v as Record<string, unknown>);
			setErr("");
		} catch (e) {
			setErr("读不了这个文件: " + String((e as Error).message));
		}
	};
	const submit = async (): Promise<void> => {
		setBusy(true);
		const r = kind === "profile" ? await importApi.profile({ profile_dir: dir.trim(), mode, name: name.trim() }) : await importApi.pack({ pack: pack ?? {}, password: pass, name: name.trim() });
		setBusy(false);
		if (!r.ok) {
			setErr(r.error);
			toast.error("导入失败: " + r.error);
			return;
		}
		toast.success("已导入为 " + (r.data?.name || r.data?.id || "新环境"));
		props.onDone();
		props.onClose();
	};
	const ready = kind === "profile" ? !!dir.trim() : !!pack;

	return (
		<Dialog open={!!kind} onOpenChange={(o) => (o ? undefined : props.onClose())}>
			<DialogContent className="sm:max-w-lg" data-import-dialog={kind ?? ""}>
				<DialogHeader>
					<DialogTitle>{kind === "profile" ? "导入 profile 目录" : "导入账号包"}</DialogTitle>
					<DialogDescription>
						{kind === "profile" ? "填浏览器服务所在机器上的 Firefox profile 路径, 登录态随目录保留." : "选导出的账号包 JSON, 包内含 cookie 与指纹, 导入后新建一个环境."}
					</DialogDescription>
				</DialogHeader>
				<div className="flex flex-col gap-3">
					{kind === "profile" ? (
						<>
							<Row label="profile 目录"><Input className="font-mono" value={dir} placeholder="/home/hx/.mozilla/firefox/abcd.default" onChange={(e) => setDir(e.target.value)} /></Row>
							<Row label="方式"><SelectField value={mode} onChange={setMode} options={MODES} /></Row>
						</>
					) : (
						<>
							<Row label="账号包文件" hint={file ? file + (pack ? ", 已读取" : "") : undefined}>
								<Input type="file" accept=".json,application/json" onChange={(e) => void pick(e.target.files?.[0])} />
							</Row>
							<Row label="口令 (可选)"><Input type="password" autoComplete="off" value={pass} onChange={(e) => setPass(e.target.value)} /></Row>
						</>
					)}
					<Row label="新环境名称"><Input value={name} placeholder="可留空" onChange={(e) => setName(e.target.value)} /></Row>
					{err ? <p className="t-aux text-destructive" role="alert">{err}</p> : null}
				</div>
				<DialogFooter>
					<Button variant="outline" onClick={props.onClose}>取消</Button>
					<Button disabled={!ready || busy} onClick={() => void submit()}>{busy ? "导入中" : "导入"}</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
