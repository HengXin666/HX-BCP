import { spawnSync } from "node:child_process"

/*
 * 这个脚本曾经是 Docker / 运维子页的滚动门禁(需要真实登录凭据)。
 *
 * 现在滚动门禁由 e2e/docker-scroll-guard.mjs 承担: 它不依赖登录与 2FA, 用路由打桩
 * 注入 40 个容器, 并直接断言「表格区域是唯一滚动容器 / 滚轮 / 拖动 / 末行可见」。
 * 这里保留入口, 只做转发, 免得同一件事有两套会漂移的断言。
 *
 *   HX_UI_BASE_URL=http://127.0.0.1:28341 node e2e/terminal-scroll.mjs
 */
const guard = new URL("./docker-scroll-guard.mjs", import.meta.url)
const result = spawnSync(process.execPath, [guard.pathname], { stdio: "inherit" })
process.exit(result.status ?? 1)
