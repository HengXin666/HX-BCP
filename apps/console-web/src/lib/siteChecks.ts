/**
 * 检测站实测: 对一个在跑的环境跑 fingerprint-score 流水线里的同一套工具 (后端 site_checks.py).
 *   sites  公开检测站采集 + score.ts 评分, 0 到 100
 *   audit  fingerprint-audit 一致性规则, 红 黄 绿
 * GET /environments/{id}/site-checks 读两种的最近一次结果, POST .../site-checks/{kind} 后台开跑.
 */
import { API, getEnvelope, type Result } from "./api";
import { sendCompat } from "./http";

export type CheckKind = "sites" | "audit";
export type CheckRow = { siteId?: string; name: string; score?: number | null; verdict: string; headline: string; fix?: string };
export type CheckState = {
	kind: CheckKind;
	state: "idle" | "running" | "done" | "failed";
	started_at?: number;
	finished_at?: number;
	score?: number | null;
	verdict?: string;
	rows?: CheckRow[];
	error?: string;
};
export type SiteChecks = Record<CheckKind, CheckState>;

const path = (id: string): string => "/environments/" + encodeURIComponent(id) + "/site-checks";

export const siteChecksApi = {
	get: (id: string, s?: AbortSignal): Promise<Result<SiteChecks>> => getEnvelope<SiteChecks>(API + path(id), s),
	run: (id: string, kind: CheckKind): Promise<Result<CheckState>> => sendCompat<CheckState>("POST", path(id) + "/" + kind),
};

/** 行与整体结论的展示色, 两种检测的取值不同 (pass fail unmeasured 与 green yellow red) */
export function tone(v: string | undefined): "success" | "warning" | "destructive" | "secondary" {
	if (v === "pass" || v === "green") return "success";
	if (v === "yellow" || v === "incomplete") return "warning";
	if (v === "fail" || v === "block" || v === "red") return "destructive";
	return "secondary";
}

export const VERDICT_TEXT: Record<string, string> = {
	pass: "通过", fail: "未过", unmeasured: "没测到", block: "阻断", incomplete: "不完整",
	green: "绿", yellow: "黄", red: "红", unknown: "未知",
};
