/**
 * 环境的运维接口: 复制, 重启, 窗口前置, 最近删除 (列表, 恢复, 彻底删除), 体检与一键修复, 注入 cookie.
 * 全部是 8325 已有路由, 控制台此前没接. 写操作仍过通道判定, API 组会被后端拒绝.
 */
import { API, getEnvelope, type Env, type Result } from "./api";
import { call } from "./api-res";

const enc = encodeURIComponent;
const envPath = (id: string, tail: string): string => "/environments/" + enc(id) + tail;

/** 体检单项: status 取 healthy, configured, warning, error, unknown, manual. fix 是可自动修复时要改的偏好. */
export type DoctorCheck = { id: string; status: string; value: string; detail: string; fixable: boolean; fix: Record<string, unknown> };
export type DoctorReport = { env_id: string; region: Record<string, unknown>; checks: DoctorCheck[] };

export const opsApi = {
	duplicate: (id: string) => call<Env>("POST", envPath(id, "/duplicate")),
	restart: (id: string) => call<unknown>("POST", envPath(id, "/restart")),
	focus: (id: string) => call<{ ok?: boolean; visible_to_user?: boolean }>("POST", envPath(id, "/window/focus")),
	trash: (signal?: AbortSignal): Promise<Result<Env[]>> =>
		getEnvelope<{ list: Env[] }>(API + "/trash", signal).then((r) => (r.ok ? { ok: true, data: r.data?.list ?? [] } : r)),
	restore: (id: string) => call<{ id: string }>("POST", envPath(id, "/restore")),
	purge: (id: string) => call<unknown>("POST", envPath(id, "/purge")),
	doctor: (id: string, signal?: AbortSignal) => getEnvelope<DoctorReport>(API + envPath(id, "/doctor?network=true"), signal),
	fix: (id: string, items: string[]) => call<Record<string, unknown>>("POST", envPath(id, "/doctor/fix"), { items }),
	injectCookies: (id: string, cookies: unknown) => call<{ injected: number }>("POST", envPath(id, "/cookies"), { cookies }),
};

/** 相对时间: 刚刚, N 分钟前, N 小时前, N 天前; 超过 30 天写日期. 0 或缺省写横线. */
export function ago(ms: number | undefined | null): string {
	if (!ms) return "-";
	const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
	if (s < 45) return "刚刚";
	if (s < 3600) return Math.round(s / 60) + " 分钟前";
	if (s < 86400) return Math.round(s / 3600) + " 小时前";
	if (s < 86400 * 30) return Math.round(s / 86400) + " 天前";
	return new Date(ms).toLocaleDateString();
}
