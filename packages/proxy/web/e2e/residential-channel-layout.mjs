import { chromium } from "playwright"

const baseURL = process.env.HX_UI_BASE_URL
const setupToken = process.env.HX_UI_SETUP_TOKEN
if (!baseURL || !setupToken) throw new Error("HX_UI_BASE_URL and HX_UI_SETUP_TOKEN are required")

const browser = await chromium.launch({
  executablePath: process.env.HX_UI_CHROMIUM || "/usr/bin/chromium",
  headless: true,
})
const context = await browser.newContext({
  viewport: { width: 1440, height: 960 },
  deviceScaleFactor: 1,
  permissions: ["clipboard-read", "clipboard-write"],
})
const page = await context.newPage()

async function assertNoPageOverflow(label) {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }))
  if (dimensions.scrollWidth > dimensions.clientWidth) {
    throw new Error(`${label} overflows horizontally: ${JSON.stringify(dimensions)}`)
  }
}

await page.goto(baseURL, { waitUntil: "networkidle" })
await page.getByLabel("一次性初始化 Token").fill(setupToken)
await page.getByLabel("用户名").fill("residential-channel-ui")
await page.getByLabel("密码").fill("residential-channel-ui-password-2026")
await page.getByRole("button", { name: "初始化并登录" }).click()
await page.getByRole("heading", { name: "总览" }).waitFor()

await page.goto(`${baseURL}/#/residential`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "住宅代理" }).waitFor()
if (await page.getByText("统一客户端订阅", { exact: true }).count()) {
  throw new Error("residential page still exposes the removed unified subscription")
}

await page.getByRole("tab", { name: /^供应商/ }).click()
await page.getByRole("button", { name: "新建供应商" }).click()
const providerDialog = page.getByRole("dialog")
await providerDialog.getByRole("heading", { name: "新建供应商" }).waitFor()
await providerDialog.getByLabel("名称").fill("住宅 UI 验证供应商")
await providerDialog.getByLabel("网关地址").fill("proxy.example.test")
await providerDialog.getByLabel("账号").fill("ui-user")
await providerDialog.getByLabel("密码").fill("ui-password")
await providerDialog.getByRole("button", { name: "保存", exact: true }).click()
await page.getByText("住宅 UI 验证供应商", { exact: true }).waitFor()

await page.getByRole("tab", { name: /^渠道/ }).click()
await page.getByRole("button", { name: "新建渠道" }).click()
const channelDialog = page.getByRole("dialog")
await channelDialog.getByRole("heading", { name: "新建渠道" }).waitFor()
await channelDialog.getByLabel("名称").fill("住宅 VMess UI 验证")
await channelDialog.getByLabel("客户端协议").click()
await page.getByRole("option", { name: "VMess over WebSocket" }).click()
await channelDialog.getByText("VMESS over WebSocket · TLS", { exact: true }).waitFor()
await channelDialog.getByLabel("节点数量").fill("2")
await channelDialog.getByLabel("Cloudflare / 雷池域名").fill("proxy.example.test")
await assertNoPageOverflow("residential channel dialog desktop")
await page.screenshot({ path: "../docs/screenshots/residential-channel-protocol-desktop.png", fullPage: true })
await channelDialog.getByRole("button", { name: "创建", exact: true }).click()
await page.getByText("住宅 VMess UI 验证", { exact: true }).waitFor()
await page.getByText("VMESS · WebSocket · TLS", { exact: true }).waitFor()
await assertNoPageOverflow("residential channel desktop")

await page.goto(`${baseURL}/#/routing`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "代理服务" }).waitFor()
await page.getByText("住宅 VMess UI 验证", { exact: true }).waitFor()
const serviceAction = page.getByRole("combobox").filter({ hasText: "服务操作" }).first()
await serviceAction.click()
await page.getByRole("option", { name: "复制 Clash / Mihomo 订阅" }).waitFor()
await page.getByRole("option", { name: "复制自动化控制 URL" }).waitFor()
await page.getByRole("option", { name: "复制 Clash / Mihomo 订阅" }).click()
await page.getByText("Clash / Mihomo 订阅已复制").waitFor()
let clipboard = await page.evaluate(() => navigator.clipboard.readText())
if (!clipboard.startsWith("https://proxy.example.test/sub/") || !clipboard.endsWith("?format=clash")) {
  throw new Error(`unexpected residential subscription URL: ${clipboard}`)
}
const closeNotice = page.getByRole("button", { name: "关闭提示" })
if (await closeNotice.isVisible()) await closeNotice.click()

await serviceAction.click()
await page.getByRole("option", { name: "复制自动化控制 URL" }).click()
await page.getByText("自动化控制 URL已复制").waitFor()
clipboard = await page.evaluate(() => navigator.clipboard.readText())
if (!clipboard.startsWith("https://proxy.example.test/ctl/")) {
  throw new Error(`unexpected residential control URL: ${clipboard}`)
}
if (await closeNotice.isVisible()) await closeNotice.click()
await serviceAction.click()
await page.getByRole("option", { name: "复制 Clash / Mihomo 订阅" }).waitFor()
await page.getByRole("option", { name: "复制自动化控制 URL" }).waitFor()
await assertNoPageOverflow("residential proxy service desktop")
await page.screenshot({ path: "../docs/screenshots/residential-proxy-service-actions-desktop.png", fullPage: true })
await page.keyboard.press("Escape")

await page.setViewportSize({ width: 390, height: 844 })
await page.goto(`${baseURL}/#/routing`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "代理服务" }).waitFor()
await page.getByText("住宅 VMess UI 验证", { exact: true }).waitFor()
await page.getByRole("combobox").filter({ hasText: "服务操作" }).first().click()
await page.getByRole("option", { name: "复制自动化控制 URL" }).waitFor()
await assertNoPageOverflow("residential proxy service mobile")
await page.screenshot({ path: "../docs/screenshots/residential-proxy-service-actions-mobile.png", fullPage: true })

await browser.close()
