import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const OUT_DIR = fileURLToPath(new URL("../../packages/cache-proxy/src/hxcp/web", import.meta.url));
const SRC = fileURLToPath(new URL("./src", import.meta.url));
const API = process.env.HXCP_API ?? "http://127.0.0.1:8898";
const API_PATHS = ["/stats", "/health", "/entries", "/lookup", "/body", "/flows", "/flow", "/purge", "/access", "/runtimes",
	"/ca.crt", "/proxy.pac", "/rules", "/hosts", "/clients", "/entry"];

/** 第三方依赖按库拆成独立分包, 业务代码换版时公共库缓存不失效. */
function vendorChunk(id: string): string | undefined {
	if (!id.includes("node_modules")) return undefined;
	if (/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/.test(id)) return "vendor-react";
	if (id.includes("@hx/ui") || id.includes("hx-ui")) return "vendor-hx-ui";
	if (id.includes("lucide-react")) return "vendor-icons";
	return "vendor";
}

/**
 * hxcp 控制台: 组件与样式来自 @hx/ui. 前端只发相对路径 (stats, flows ...), 直连 8898 与 HX-WebX 子路径都能用.
 * build 直接写进 hxcp.api 托管的 hxcp/web 并先清空. dev 把 API 路径代理到 8898 (HXCP_API 可改).
 */
export default defineConfig({
	base: "./",
	plugins: [react(), tailwindcss()],
	resolve: { alias: [{ find: /^@\//, replacement: SRC + "/" }] },
	build: {
		outDir: OUT_DIR,
		emptyOutDir: true,
		rollupOptions: { output: { manualChunks: vendorChunk } },
	},
	server: {
		port: 8941,
		host: "127.0.0.1",
		proxy: Object.fromEntries(API_PATHS.map((p) => [p, { target: API, changeOrigin: true }])),
	},
});
