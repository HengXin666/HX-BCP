/**
 * 登录态: GET /environments/login-states 一次扫全部可见环境, 返回 {env_id: {running, offline, platforms: {平台: {state, keys, identity}}}}.
 * 离线环境读 cookie 库的名称与过期时间判定, 运行中的环境额外经 CDP 取账号身份. 扫描较重, 列表按 60 秒轮询.
 */
import { createContext, useContext } from "react";
import { API, type Result } from "./api";
import { requestEnvelope, toResult } from "./http";
import { KEYS, keyed } from "./cache";

export type LoginState = "ok" | "expired" | "empty";
export type PlatformLogin = { state: LoginState | string; keys?: string[]; total?: number; identity?: string };
export type EnvLogin = { running?: boolean; offline?: boolean; error?: string; platforms: Record<string, PlatformLogin> };
export type LoginMap = Record<string, EnvLogin>;

/** 扫描较重: 超时放宽到 45 秒, 会话内 50 秒新鲜期; 启停环境后由写接口失效. */
export const loginStates = keyed<LoginMap>(KEYS.login, 50000, (signal) =>
	requestEnvelope<LoginMap>(API + "/environments/login-states", { signal, timeoutMs: 45000 })
		.then(toResult)
		.then((r): Result<LoginMap> => (r.ok ? { ok: true, data: r.data ?? {} } : r)),
);

/** null = 接口还没返回或不可达 (显示 "未上报"); 否则是全部环境的登录态. */
export const LoginContext = createContext<LoginMap | null>(null);
export const useLogin = (id: string): EnvLogin | null | undefined => {
	const m = useContext(LoginContext);
	return m === null ? null : m[id];
};

/** 已登录 (state=ok) 的平台名. */
export function loggedIn(e: EnvLogin | null | undefined): string[] {
	return e ? Object.entries(e.platforms ?? {}).filter(([, v]) => v.state === "ok").map(([k, v]) => k + (v.identity ? " (" + v.identity + ")" : "")) : [];
}
