import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/** @hx/ui 以源码形式取自 GitHub, 这里把包入口指向它的 src. */
function uiSrc(): string {
	return resolve(dirname(fileURLToPath(import.meta.resolve("@hx/ui/package.json"))), "src");
}

export default defineConfig({
	plugins: [react(), tailwindcss()],
	resolve: {
		alias: [
			{ find: "@hx/ui/styles.css", replacement: resolve(uiSrc(), "styles.css") },
			{ find: "@hx/ui/tokens.css", replacement: resolve(uiSrc(), "tokens.css") },
			{ find: /^@hx\/ui$/, replacement: resolve(uiSrc(), "index.ts") },
		],
	},
	server: {
		port: 8940,
		host: "127.0.0.1",
		proxy: {
			"/api/browser": { target: "http://127.0.0.1:8325", changeOrigin: true, rewrite: (p) => p.replace(/^\/api\/browser/, "") },
			"/api/cache": { target: "http://127.0.0.1:8898", changeOrigin: true, rewrite: (p) => p.replace(/^\/api\/cache/, "") },
		},
	},
});
