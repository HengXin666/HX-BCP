import { chromium } from "playwright"

const baseURL = process.env.HX_UI_BASE_URL
const setupToken = process.env.HX_UI_SETUP_TOKEN
if (!baseURL || !setupToken) throw new Error("HX_UI_BASE_URL and HX_UI_SETUP_TOKEN are required")

const browser = await chromium.launch({ executablePath: process.env.HX_UI_CHROMIUM || "/usr/bin/chromium", headless: true })
const page = await browser.newPage({ viewport: { width: 1440, height: 960 }, deviceScaleFactor: 1 })
await page.goto(baseURL, { waitUntil: "networkidle" })
await page.getByLabel("一次性初始化 Token").fill(setupToken)
await page.getByLabel("用户名").fill("ui-review")
await page.getByLabel("密码").fill("ui-review-password-2026")
await page.getByRole("button", { name: "初始化并登录" }).click()
await page.getByRole("heading", { name: "总览" }).waitFor()
await page.goto(`${baseURL}/#/subscriptions`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "订阅与节点" }).waitFor()

await page.getByRole("button", { name: "新建订阅" }).first().click()
await page.getByRole("dialog").waitFor()
await page.getByLabel("订阅名称").fill("Clash 文件验证")
await page.getByRole("tab", { name: "粘贴内容" }).click()
await page.getByLabel("Clash / Mihomo YAML 或分享内容").fill("proxies:\n  - name: test\n    type: ss\n    server: 192.0.2.10\n    port: 443\n    cipher: aes-128-gcm\n    password: test-only\n")
await page.getByRole("button", { name: "创建订阅" }).click()
await page.getByText("Clash 文件验证", { exact: true }).waitFor()
const subscriptionRow = page.locator("article").filter({ hasText: "Clash 文件验证" }).first()
await subscriptionRow.getByRole("button", { name: "刷新" }).click()
await subscriptionRow.getByText("1 个节点").waitFor()
await page.getByText("Clash 文件验证", { exact: true }).click()
await page.getByRole("heading", { name: "查看与编辑订阅" }).waitFor()
await page.getByText("替换加密来源").waitFor()
await page.screenshot({ path: "../docs/screenshots/subscription-editor-desktop.png", fullPage: true })
await page.getByRole("button", { name: "取消" }).click()

await page.goto(`${baseURL}/#/routing`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "代理服务" }).waitFor()
// The create form is a dialog now, so the page is one full-width column at every
// width. The list may scroll inside its own box, but the page must never scroll
// sideways: the service row has a hard minimum width (name + badges + address +
// strategy + actions).
if (await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)) {
  throw new Error("proxy service page overflows horizontally")
}
if (await page.locator("main form").count() !== 0) {
  throw new Error("proxy service page must not keep the create form inline")
}
await page.getByRole("button", { name: "新建代理服务" }).first().click()
await page.getByRole("dialog").waitFor()
await page.getByLabel("服务名称").fill("本机订阅验证")
// The shared inbound is the default for new installs: every service reuses the
// one Mixed port, so the per-service port field is intentionally read-only and
// the form only asks for the login credential.
const sharedInboundEnabled = await page.evaluate(async () => (await (await fetch("/api/v1/settings")).json()).shared_inbound.mode === "shared")
const fixedPort = Number((await page.getByLabel("本地端口").textContent()).match(/\d+/)?.[0])
if (await page.getByLabel("本地端口").evaluate(element => element.tagName) !== "OUTPUT" || !fixedPort) {
  throw new Error("ingress port must be a fixed deployment value, not an editable input")
}
if (sharedInboundEnabled) {
  await page.getByRole("dialog").getByRole("textbox", { name: "用户名" }).fill("ui-review-user")
  await page.getByRole("dialog").getByRole("button", { name: "创建并启动" }).click()
  // The shared inbound keeps one aggregate listener per family alongside the
  // per-service rows, so the new service name can legitimately appear twice
  // (its own row and the aggregate's). Assert the row exists, not that it is
  // unique.
  await page.getByText("本机订阅验证 入口", { exact: true }).first().waitFor()
} else {
  await page.getByLabel("公网主机名 / IP（可选）").fill("proxy.example.com")
  await page.getByRole("dialog").getByRole("button", { name: "创建并启动" }).click()
  await page.getByText("本机订阅验证 入口", { exact: true }).first().waitFor()
}

const listener = await page.evaluate(async () => {
  const response = await fetch("/api/v1/listeners")
  const body = await response.json()
  return body.items.find((item) => item.name.includes("本机订阅验证"))
})
if (!listener?.share_path) throw new Error("created listener has no share path")

const results = await page.evaluate(async (sharePath) => {
  const request = async (format, userAgent) => {
    const response = await fetch(`${sharePath}?format=${format}`, { headers: { "User-Agent": userAgent } })
    return { status: response.status, type: response.headers.get("content-type"), format: response.headers.get("x-hx-subscription-format"), body: await response.text() }
  }
  return {
    clash: await request("clash", "Clash-Verge/v2.4.2"),
    v2rayn: await request("v2rayn", "v2rayN/7"),
    shadowrocket: await request("v2rayn", "Shadowrocket/2"),
    singbox: await request("sing-box", "sing-box/1.11"),
  }
}, listener.share_path)
if (results.clash.status !== 200 || results.clash.format !== "clash" || !results.clash.body.includes("proxies:")) throw new Error("Clash localhost subscription failed")
// Shared mode advertises the aggregate entry point; per-service mode advertises
// the configured public endpoint. Both must reach the subscription body.
const expectedEndpoint = sharedInboundEnabled ? "127.0.0.1:" : `proxy.example.com:${fixedPort}`
if (results.v2rayn.status !== 200 || results.v2rayn.format !== "v2rayn" || !Buffer.from(results.v2rayn.body, "base64").toString().includes(expectedEndpoint)) throw new Error(`v2rayN subscription did not advertise ${expectedEndpoint}`)
if (results.shadowrocket.status !== 200 || results.shadowrocket.format !== "v2rayn" || !Buffer.from(results.shadowrocket.body, "base64").toString().includes(expectedEndpoint)) throw new Error(`Shadowrocket subscription did not advertise ${expectedEndpoint}`)
if (results.singbox.status !== 200 || results.singbox.format !== "sing-box" || JSON.parse(results.singbox.body).outbounds.length === 0) throw new Error("sing-box localhost subscription failed")

await page.getByText("本机订阅验证 入口", { exact: true }).first().click()
await page.getByText("DIRECT（当前服务器出口）").waitFor()
await page.getByRole("tab", { name: "动态编辑" }).click()
const editPanel = page.getByRole("tabpanel", { name: "动态编辑" })
await editPanel.getByText("实时编辑服务").waitFor()
await editPanel.getByLabel("服务名称").fill("本机代理服务已编辑")
await editPanel.getByRole("button", { name: "保存并应用" }).click()
await page.getByText("本机代理服务已编辑 入口", { exact: true }).first().waitFor()

const serviceAction = page.getByRole("combobox").filter({ hasText: "服务操作" })
await serviceAction.click()
await page.getByRole("option", { name: "复制入口地址" }).click()
await page.getByText("入口地址已复制").waitFor()
await serviceAction.getByText("服务操作").waitFor()
const closeNotice = page.getByRole("button", { name: "关闭提示" })
if (await closeNotice.isVisible()) await closeNotice.click()

await page.getByRole("tab", { name: "流量统计" }).click()
await page.getByText("最近 24 小时").waitFor()
await page.screenshot({ path: "../docs/screenshots/proxy-service-traffic-desktop.png", fullPage: true })
await page.getByRole("tab", { name: "实时日志" }).click()
await page.getByText("实时连接").waitFor({ timeout: 10_000 })
await page.screenshot({ path: "../docs/screenshots/proxy-service-logs-desktop.png", fullPage: true })
await page.getByRole("tab", { name: "节点成员" }).click()
await page.screenshot({ path: "../docs/screenshots/proxy-services-expanded-desktop.png", fullPage: true })

await page.goto(`${baseURL}/#/nodes`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "订阅与节点" }).waitFor()
await page.getByRole("tab", { name: "节点库存" }).waitFor()
await page.getByRole("button", { name: /Clash 文件验证/ }).waitFor()
await page.getByText("test", { exact: true }).waitFor()
await page.screenshot({ path: "../docs/screenshots/nodes-by-subscription-desktop.png", fullPage: true })

await page.goto(`${baseURL}/#/`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "总览" }).waitFor()
await page.getByText("Mihomo 运行中", { exact: true }).waitFor()
await page.waitForTimeout(1_200)
await page.screenshot({ path: "../docs/screenshots/global-overview-desktop.png", fullPage: true })

await page.goto(`${baseURL}/#/rules`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "站点别名" }).waitFor()
await page.getByRole("button", { name: "新增别名" }).click()
await page.getByLabel("站点组 1 匹配值").fill("openai.com")
await page.getByLabel("站点别名名称").fill("AI 服务")
await page.getByRole("button", { name: "保存并应用" }).click()
await page.getByText("站点别名已保存并应用").waitFor()

await page.goto(`${baseURL}/#/settings`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "全局配置" }).waitFor()
await page.getByRole("tab", { name: "节点检测" }).click()
await page.getByLabel("启用 ChatGPT").click()
await page.getByRole("button", { name: "测试站点" }).click()
await page.getByText(/站点测试完成/).waitFor({ timeout: 20_000 })
await page.waitForTimeout(250)
await page.screenshot({ path: "../docs/screenshots/settings-light-desktop.png", fullPage: true })
await page.getByRole("tab", { name: "主题" }).click()
await page.getByRole("button", { name: /黑夜/ }).click()
await page.waitForFunction(() => document.documentElement.classList.contains("dark"))
await page.getByLabel("主题颜色选择器").fill("#2563eb")
await page.waitForFunction(() => localStorage.getItem("hx-theme-color") === "#2563eb" && document.documentElement.style.getPropertyValue("--brand") === "#2563eb")
await page.waitForTimeout(250)
await page.screenshot({ path: "../docs/screenshots/settings-dark-desktop.png", fullPage: true })

await page.goto(`${baseURL}/#/rules`, { waitUntil: "networkidle" })
const aliasNameInput = page.getByLabel("站点别名名称")
await aliasNameInput.waitFor()
if (await aliasNameInput.inputValue() !== "AI 服务") throw new Error("site alias was not persisted")
await page.screenshot({ path: "../docs/screenshots/site-aliases-dark-desktop.png", fullPage: true })

await page.goto(`${baseURL}/#/routing`, { waitUntil: "networkidle" })
await page.getByText("本机代理服务已编辑 入口", { exact: true }).first().click()
await page.getByRole("tab", { name: "路由策略" }).click()
await page.getByLabel("AI 服务 路由动作").click()
await page.getByRole("option", { name: "直连 DIRECT" }).click()
await page.getByRole("button", { name: "保存策略" }).click()
await page.getByText("本机代理服务已编辑 的路由策略已应用").waitFor()
const routeConfig = await page.evaluate(async () => (await fetch("/api/v1/routing-rules")).json())
const aiRoute = routeConfig.rule_sets.find((set) => set.name === "AI 服务")?.routes?.[0]
if (aiRoute?.action?.type !== "direct") throw new Error("per-group route action was not persisted")
await page.screenshot({ path: "../docs/screenshots/proxy-group-routing-dark-desktop.png", fullPage: true })

await page.setViewportSize({ width: 390, height: 844 })
await page.goto(`${baseURL}/#/settings`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "全局配置" }).waitFor()
await page.getByText("本机代理服务已编辑 的路由策略已应用").waitFor({ state: "hidden", timeout: 10_000 })
await page.waitForTimeout(250)
await page.screenshot({ path: "../docs/screenshots/settings-dark-mobile.png", fullPage: true })
await page.goto(`${baseURL}/#/routing`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "代理服务" }).waitFor()
await page.getByText("本机代理服务已编辑 入口", { exact: true }).first().click()
await page.getByText("DIRECT（当前服务器出口）").waitFor()
await page.screenshot({ path: "../docs/screenshots/proxy-services-expanded-mobile.png", fullPage: true })
await page.goto(`${baseURL}/#/`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "总览" }).waitFor()
await page.getByText("Mihomo 运行中", { exact: true }).waitFor()
await page.waitForTimeout(1_200)
await page.screenshot({ path: "../docs/screenshots/global-overview-mobile.png", fullPage: true })

await page.setViewportSize({ width: 1440, height: 960 })
await page.goto(`${baseURL}/#/settings`, { waitUntil: "networkidle" })
await page.getByRole("tab", { name: "账号安全" }).click()
await page.getByLabel("新管理员账号").fill("ui-review-renamed")
await page.getByLabel("修改账号的当前密码").fill("ui-review-password-2026")
await page.getByRole("button", { name: "修改账号" }).click()
await page.getByText("管理员登录", { exact: true }).waitFor()
await page.getByLabel("用户名").fill("ui-review-renamed")
await page.getByLabel("密码").fill("ui-review-password-2026")
await page.getByRole("button", { name: "登录" }).click()
await page.getByRole("heading", { name: "全局配置" }).waitFor()
await page.getByRole("tab", { name: "账号安全" }).click()
await page.getByLabel("当前密码", { exact: true }).fill("ui-review-password-2026")
await page.getByLabel("新密码", { exact: true }).fill("ui-review-password-updated-2026")
await page.getByLabel("确认新密码").fill("ui-review-password-updated-2026")
await page.getByRole("button", { name: "修改密码" }).click()
await page.getByText("管理员登录", { exact: true }).waitFor()
await page.getByLabel("用户名").fill("ui-review-renamed")
await page.getByLabel("密码").fill("ui-review-password-updated-2026")
await page.getByRole("button", { name: "登录" }).click()
await page.getByRole("heading", { name: "全局配置" }).waitFor()
await browser.close()
