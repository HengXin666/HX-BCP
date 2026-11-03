/** 人类通道会话: 查询, 用本机令牌解锁, 退出. 令牌只在这次 POST 的请求体里, 不存储. */
import { API, HUMAN_HEADERS, PAGE_BASE, errorText, type Envelope, type Result } from "./api";

export type SessionState = { human: boolean; ttl_s: number };

/** 发一次会话请求, 返回信封 data. */
async function call(method: string, body?: unknown): Promise<Result<SessionState>> {
	try {
		const res = await fetch(API + "/lane/session", {
			method,
			headers: { ...HUMAN_HEADERS, "Content-Type": "application/json", "x-bcp-base": PAGE_BASE },
			body: body === undefined ? undefined : JSON.stringify(body),
		});
		if (!res.ok) return { ok: false, error: await errorText(res) };
		const env = (await res.json()) as Envelope<SessionState>;
		if (env.code !== 0 || !env.data) return { ok: false, error: env.msg ?? "code " + String(env.code) };
		return { ok: true, data: env.data };
	} catch (err) {
		return { ok: false, error: String((err as Error)?.message ?? err) };
	}
}

export const sessionApi = {
	state: () => call("GET"),
	open: (token: string) => call("POST", { token }),
	close: () => call("DELETE"),
};
