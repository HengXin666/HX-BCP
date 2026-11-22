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
import { parseHash } from "../lib/router";
import { sessionApi } from "../lib/session";

/** 地址栏里的一次性解锁码 (#/unlock?code=...); 读到就从历史里抹掉, 不留在浏览记录. */
function takeUnlockCode(): string {
	const r = parseHash(window.location.hash, []);
	if (r.path[0] !== "unlock") return "";
	window.history.replaceState(null, "", "#/overview");
	window.dispatchEvent(new HashChangeEvent("hashchange"));
	return r.query.get("code") ?? "";
}

type Phase = "checking" | "locked" | "open";

/** 人类通道门: 8325 认可本页为人类时才渲染子页面, 否则弹出令牌解锁框. 令牌只随这次 POST 发出, 不保存. */
export function HumanGate({ children }: { children: ReactNode }): JSX.Element {
	const [phase, setPhase] = useState<Phase>("checking");
	const [token, setToken] = useState("");
	const [error, setError] = useState("");
	const [busy, setBusy] = useState(false);

	useEffect(() => {
		let alive = true;
		const code = takeUnlockCode();
		void (async () => {
			if (code) {
				const u = await sessionApi.openCode(code);
				if (!alive) return;
				if (u.ok && u.data.human) return setPhase("open");
				setError("解锁链接无效或已过期 (10 分钟), 运行 bcp-lane unlock-url 取新的, 或粘贴令牌");
			}
			const r = await sessionApi.state();
			if (!alive) return;
			if (!r.ok) setError(r.error);
			setPhase(r.ok && r.data.human ? "open" : "locked");
		})();
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
						本机终端运行 bcp-lane unlock-url 打开它打印的链接 (10 分钟有效), 或粘贴本机人类令牌 (cat ~/.bcp/human.token).
						8325 校验后发 12 小时的 HttpOnly 会话, 页面脚本读不到, 令牌不保存. Agent 的 API 密钥在这里无效.
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
