/**
 * 粘贴整行代理串转表单字段. 认四种写法:
 *   type://user:pass@host:port   type://host:port   user:pass@host:port   host:port   host:port:user:pass
 * type 缺省 http; socks / socks5h 归一为 socks5. 认不出返回 null, 由表单提示.
 */
import type { ProxyBody } from "./api-res";

const TYPES: Record<string, string> = { http: "http", https: "https", socks: "socks5", socks5: "socks5", socks5h: "socks5" };

export function parseProxyLine(raw: string): Omit<ProxyBody, "label"> | null {
	let s = raw.trim();
	if (!s) return null;
	let type = "http";
	const scheme = /^([a-z0-9]+):\/\//i.exec(s);
	if (scheme) {
		const t = TYPES[scheme[1].toLowerCase()];
		if (!t) return null;
		type = t;
		s = s.slice(scheme[0].length);
	}
	s = s.replace(/\/+$/, "");
	let username = "";
	let password = "";
	const at = s.lastIndexOf("@");
	if (at >= 0) {
		const cred = s.slice(0, at);
		s = s.slice(at + 1);
		const i = cred.indexOf(":");
		username = decodeURIComponent(i >= 0 ? cred.slice(0, i) : cred);
		password = decodeURIComponent(i >= 0 ? cred.slice(i + 1) : "");
	}
	const parts = s.split(":");
	if (parts.length === 4 && !username) {
		username = parts[2];
		password = parts[3];
		parts.length = 2;
	}
	if (parts.length !== 2) return null;
	const port = Number(parts[1]);
	if (!parts[0] || !Number.isInteger(port) || port <= 0 || port > 65535) return null;
	return { type, host: parts[0], port, username, password };
}

/** 列表里的地址: type://host:port, 不带凭据. */
export function proxyAddr(p: { type?: string; host?: string; port?: number }): string {
	return (p.type || "http") + "://" + (p.host || "?") + (p.port ? ":" + p.port : "");
}
