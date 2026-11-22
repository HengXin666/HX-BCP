/** 人类通道会话: 查询, 用本机令牌解锁, 退出. 令牌只在这次 POST 的请求体里, 不存储. */
import { PAGE_BASE, type Result } from "./api";
import { sendCompat } from "./http";
import { invalidate } from "./cache";

export type SessionState = { human: boolean; ttl_s: number; by?: string; loopback?: boolean };

/** 发一次会话请求, 返回信封 data; 走 http.ts 的统一超时与错误对象. data 为空按失败处理. */
async function call(method: string, body?: unknown): Promise<Result<SessionState>> {
	const r = await sendCompat<SessionState>(method, "/lane/session", body, method === "GET", { "x-bcp-base": PAGE_BASE });
	if (r.ok && !r.data) return { ok: false, error: "会话接口未返回数据" };
	return r;
}

export const sessionApi = {
	state: () => call("GET"),
	open: (token: string) => call("POST", { token }),
	/** hxb serve / bcp-lane unlock-url 打印的 10 分钟解锁码, 只来自地址栏 #/unlock?code=, 用完即从地址栏抹掉. */
	openCode: (code: string) => call("POST", { code }),
	/** 退出会话后整个读缓存作废, 避免下一位解锁者看到上一会话的数据. */
	close: async (): Promise<Result<SessionState>> => {
		const r = await call("DELETE");
		if (r.ok) invalidate("");
		return r;
	},
};
