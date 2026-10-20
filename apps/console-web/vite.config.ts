import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/** 控制台: 组件与样式全部来自 @hx/ui 的发布产物, 不配别名, 验证 tarball 能否当普通依赖用. */
export default defineConfig({
	plugins: [react(), tailwindcss()],
	server: {
		port: 8940,
		host: "127.0.0.1",
		proxy: {
			"/api/browser": { target: "http://127.0.0.1:8325", changeOrigin: true, rewrite: (p) => p.replace(/^\/api\/browser/, "") },
			"/api/cache": { target: "http://127.0.0.1:8898", changeOrigin: true, rewrite: (p) => p.replace(/^\/api\/cache/, "") },
		},
	},
});
