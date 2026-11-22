/** 抓包行的展示判据: 判定徽标, TLS 档, 体量, 复制为 curl / fetch. */
import type { Flow } from "../lib/api";
import { bytes } from "../lib/format";

export type Tone = "success" | "warning" | "destructive" | "secondary" | "outline" | "default";

export function verdictTone(r: Flow): Tone {
	const v = String(r.verdict ?? r.kind);
	if (r.kind === "hit") return "success";
	if (r.kind === "tunnel") return "outline";
	if (v.startsWith("fill")) return "default";
	if (/sensitive|no_store|set_cookie|vary|error/.test(v)) return "destructive";
	return "secondary";
}

const TLS: Record<string, [string, Tone]> = {
	passthrough: ["隧道", "outline"],
	decrypted: ["解密", "warning"],
	plain: ["明文", "secondary"],
	cdp: ["原生", "success"],
};

export function tlsLabel(r: Flow): [string, Tone] {
	const t = r.tls ?? (!r.via || r.via === "cdp" ? "cdp" : "");
	return TLS[t] ?? [t || "-", "secondary"];
}

export function sizeOf(r: Flow): string {
	const n = Number(r.size ?? 0) || Number(r.encoded ?? 0);
	if (n) return bytes(n);
	if (r.post_data) return "↑" + bytes(r.post_data.length);
	return "-";
}

const SKIP = (k: string): boolean => {
	const l = k.toLowerCase();
	return l === "host" || l === "content-length" || l.startsWith("proxy-") || l.startsWith(":");
};

const shq = (s: string): string => "'" + s.replace(/'/g, "'\\''") + "'";

export function asCurl(r: Flow): string {
	const lines = ["curl -X " + (r.method ?? "GET") + " " + shq(r.url)];
	for (const [k, v] of Object.entries(r.req_headers ?? {})) if (!SKIP(k)) lines.push("-H " + shq(k + ": " + v));
	if (r.post_data) lines.push("--data-raw " + shq(r.post_data));
	return lines.join(" \\\n  ");
}

export function asFetch(r: Flow): string {
	const headers: Record<string, string> = {};
	for (const [k, v] of Object.entries(r.req_headers ?? {})) if (!SKIP(k) && k.toLowerCase() !== "cookie") headers[k] = v;
	const init: Record<string, unknown> = { method: r.method ?? "GET", headers };
	if (r.post_data) init.body = r.post_data;
	return "await fetch(" + JSON.stringify(r.url) + ", " + JSON.stringify(init, null, 2) + ");";
}
