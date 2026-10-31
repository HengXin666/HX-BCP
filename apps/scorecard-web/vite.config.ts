import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const POSITIVE = /^[1-9][0-9]*$/;
const pr = process.env.SCORECARD_PR ?? "";
const no = process.env.SCORECARD_NO ?? "";
// Pages 项目站点的根是 /<repo>/, 测评页只是它下面的 /ci/case/<pr>/<no>/ 。
// 根前缀写错时产物会去请求 /ci/case/... , 线上就是两个 404 加一片白屏。
const root = process.env.SCORECARD_ROOT ?? "/HX-BCP/";
if (!POSITIVE.test(pr) || !POSITIVE.test(no)) {
	throw new Error(`SCORECARD_PR 和 SCORECARD_NO 必须是从 1 起的整数, 拿到 pr=${pr || "空"} no=${no || "空"}`);
}
if (!root.startsWith("/") || !root.endsWith("/")) {
	throw new Error(`SCORECARD_ROOT 必须形如 /<repo>/, 拿到 ${root || "空"}`);
}

/** 评分卡页面: 每个 PR 的每次测评一个深路径, base 决定产物里所有资源的前缀. */
export default defineConfig({
	base: `${root}ci/case/${pr}/${no}/`,
	plugins: [react(), tailwindcss()],
	define: {
		__CASE_PR__: JSON.stringify(Number(pr)),
		__CASE_NO__: JSON.stringify(Number(no)),
	},
	server: { port: 8941, host: "127.0.0.1" },
});
