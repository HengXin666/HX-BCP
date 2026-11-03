import { useEffect, useState, type JSX, type ReactNode } from "react";
import {
	Button,
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
	InputField,
} from "@hx/ui";
import { sessionApi } from "../lib/session";

type Phase = "checking" | "locked" | "open";

/** 人类通道门: 8325 认可本页为人类时才渲染子页面, 否则弹出令牌解锁框. 令牌只随这次 POST 发出, 不保存. */
export function HumanGate({ children }: { children: ReactNode }): JSX.Element {
	const [phase, setPhase] = useState<Phase>("checking");
	const [token, setToken] = useState("");
	const [error, setError] = useState("");
	const [busy, setBusy] = useState(false);

	useEffect(() => {
		let alive = true;
		void sessionApi.state().then((r) => {
			if (!alive) return;
			if (!r.ok) setError(r.error);
			setPhase(r.ok && r.data.human ? "open" : "locked");
		});
		return () => {
			alive = false;
		};
	}, []);

	const unlock = async (): Promise<void> => {
		setBusy(true);
		const r = await sessionApi.open(token.trim());
		setBusy(false);
		setToken("");
		if (r.ok && r.data.human) {
			setError("");
			setPhase("open");
		} else setError(r.ok ? "未获得人类身份" : r.error);
	};

	if (phase === "open") return <>{children}</>;
	return (
		<Dialog open={phase === "locked"}>
			<DialogContent>
				<DialogHeader>
					<DialogTitle>解锁人类通道</DialogTitle>
					<DialogDescription>
						粘贴本机人类令牌 (cat ~/.bcp/human.token). 8325 校验后发 12 小时的 HttpOnly 会话, 页面脚本读不到, 令牌不保存.
					</DialogDescription>
				</DialogHeader>
				<InputField
					value={token}
					onChange={setToken}
					type="password"
					autoFocus
					aria-label="人类令牌"
					onKeyDown={(e) => (e.key === "Enter" && token.trim() ? void unlock() : undefined)}
				/>
				{error ? <p className="t-aux text-destructive">{error}</p> : null}
				<DialogFooter>
					<Button variant="primary" disabled={busy || token.trim() === ""} onClick={() => void unlock()}>
						解锁
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
