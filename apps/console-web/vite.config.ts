import { readFileSync } from "node:fs";
import type { IncomingMessage } from "node:http";
import { homedir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const PORT = 8940;
const TOKEN_FILE = process.env.BCP_HUMAN_TOKEN_FILE ?? join(homedir(), ".bcp", "human.token");
const OUT_DIR = fileURLToPath(new URL("../../packages/browser/src/hx_browser/web", import.meta.url));
const SELF_ORIGINS = new Set(["http://127.0.0.1:" + PORT, "http://localhost:" + PORT]);

/** 每次转发都现读本机人类令牌, 读不到给空串, 后端回 401 human_token_required. 令牌不进打包产物. */
function humanToken(): string {
	try {
		return readFileSync(TOKEN_FILE, "utf8").trim();
	} catch {
		return "";
	}
}

/**
 * 只给本控制台页面发出的请求带令牌. 本机任意进程都能打 8940, 无 Origin 不是可信信号, 不再放行.
 * 浏览器同源 GET 按 fetch 规范不带 Origin, 改认 Sec-Fetch-Site: same-origin, 网页脚本无法改写这个头.
 * 其余请求只标 human 通道不给令牌, 后端回 401 human_token_required. 只影响开发代理, 打包产物里没有令牌.
 */
function fromConsole(req: IncomingMessage): boolean {
	const origin = req.headers.origin;
	if (origin) return SELF_ORIGINS.has(origin);
	return req.headers["sec-fetch-site"] === "same-origin";
}

function tagHuman(proxyReq: { setHeader(name: string, value: string): void }, req: IncomingMessage): void {
	proxyReq.setHeader("x-bcp-lane", "human");
	proxyReq.setHeader("x-bcp-human-token", fromConsole(req) ? humanToken() : "");
}

/**
 * 控制台: 组件与样式全部来自 @hx/ui 的发布产物. 前端只发相对路径 api/v1, 直连 8325 时就是 /api/v1.
 * base "./" 让产物里的资源引用全是相对路径, 直连 / 与 HX-WebX 的 /proxy/hx-browser/ 两种根都能用.
 * build 直接写进 8325 托管的 hx_browser/web 并先清空, 老 bundle 不留. dev 代理 /api/v1 到 8325.
 */
export default defineConfig({
	base: "./",
	plugins: [react(), tailwindcss()],
	build: { outDir: OUT_DIR, emptyOutDir: true },
	server: {
		port: PORT,
		host: "127.0.0.1",
		proxy: {
			"/api/v1": {
				target: "http://127.0.0.1:8325",
				changeOrigin: true,
				ws: true,
				configure: (proxy) => {
					proxy.on("proxyReq", (proxyReq, req) => tagHuman(proxyReq, req));
					proxy.on("proxyReqWs", (proxyReq, req) => tagHuman(proxyReq, req));
				},
			},
		},
	},
});
