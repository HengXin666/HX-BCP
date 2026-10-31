import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const POSITIVE = /^[1-9][0-9]*$/;
const pr = process.env.SCORECARD_PR ?? "";
const no = process.env.SCORECARD_NO ?? "";
if (!POSITIVE.test(pr) || !POSITIVE.test(no)) {
	throw new Error(`SCORECARD_PR 和 SCORECARD_NO 必须是从 1 起的整数, 拿到 pr=${pr || "空"} no=${no || "空"}`);
}

/** 评分卡页面: 每个 PR 的每次测评一个深路径 /ci/case/<pr>/<no>/, base 决定产物里所有资源的前缀. */
export default defineConfig({
	base: `/ci/case/${pr}/${no}/`,
	plugins: [react(), tailwindcss()],
	define: {
		__CASE_PR__: JSON.stringify(Number(pr)),
		__CASE_NO__: JSON.stringify(Number(no)),
	},
	server: { port: 8941, host: "127.0.0.1" },
});
