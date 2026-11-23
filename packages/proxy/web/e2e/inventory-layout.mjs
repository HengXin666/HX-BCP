import { chromium } from "playwright"

const baseURL = process.env.HX_UI_BASE_URL
const setupToken = process.env.HX_UI_SETUP_TOKEN
if (!baseURL || !setupToken) throw new Error("HX_UI_BASE_URL and HX_UI_SETUP_TOKEN are required")

const browser = await chromium.launch({ executablePath: process.env.HX_UI_CHROMIUM || "/usr/bin/chromium", headless: true })
const page = await browser.newPage({ viewport: { width: 1440, height: 960 }, deviceScaleFactor: 1 })
const subscriptionName = `Provider 兼容验证 ${Date.now()}`

async function assertNoPageOverflow(label) {
  const dimensions = await page.evaluate(() => ({ clientWidth: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth }))
  if (dimensions.scrollWidth > dimensions.clientWidth) throw new Error(`${label} overflows horizontally: ${JSON.stringify(dimensions)}`)
}

await page.goto(baseURL, { waitUntil: "networkidle" })
const setupField = page.getByLabel("一次性初始化 Token")
if (await setupField.isVisible()) {
  await setupField.fill(setupToken)
  await page.getByLabel("用户名").fill("inventory-layout")
  await page.getByLabel("密码").fill("inventory-layout-password-2026")
  await page.getByRole("button", { name: "初始化并登录" }).click()
} else {
  await page.getByLabel("用户名").fill("inventory-layout")
  await page.getByLabel("密码").fill("inventory-layout-password-2026")
  await page.getByRole("button", { name: "登录", exact: true }).click()
}
await page.getByRole("heading", { name: "总览" }).waitFor()

const sidebar = page.locator("[data-sidebar]")
const expandedSidebarWidth = (await sidebar.boundingBox())?.width ?? 0
await page.getByRole("button", { name: "折叠侧栏" }).click()
await page.waitForTimeout(260)
const collapsedSidebarWidth = (await sidebar.boundingBox())?.width ?? 0
if (expandedSidebarWidth < 200 || collapsedSidebarWidth > 72) throw new Error(`unexpected sidebar widths: ${expandedSidebarWidth} -> ${collapsedSidebarWidth}`)
const collapsedSubscriptions = sidebar.getByRole("button", { name: "订阅与节点", exact: true })
await collapsedSubscriptions.hover()
await collapsedSubscriptions.locator("[data-sidebar-tooltip]").waitFor({ state: "visible" })
await page.waitForTimeout(150)
await page.screenshot({ path: "../docs/screenshots/sidebar-collapsed-desktop.png", fullPage: true })
await page.getByRole("button", { name: "展开侧栏" }).click()
await page.waitForTimeout(260)

await page.goto(`${baseURL}/#/subscriptions`, { waitUntil: "networkidle" })
await page.getByRole("button", { name: "新建订阅" }).first().click()
await page.getByLabel("订阅名称").fill(subscriptionName)
await page.getByRole("tab", { name: "粘贴内容" }).click()
await page.getByLabel("Clash / Mihomo YAML 或分享内容").fill("payload:\n  - name: Tokyo Hysteria2\n    type: hysteria2\n    server: 192.0.2.20\n    port: 443\n    password: test-only\n  - name: Hong Kong AnyTLS\n    type: anytls\n    server: 192.0.2.21\n    port: 443\n    password: test-only\n")
await page.getByRole("button", { name: "创建订阅" }).click()
const subscription = page.locator("article").filter({ hasText: subscriptionName }).first()
await subscription.getByRole("button", { name: "刷新" }).click()
await subscription.getByText("2 个节点").waitFor()
const closeNotice = page.getByRole("button", { name: "关闭提示" })
if (await closeNotice.isVisible()) await closeNotice.click()
await page.waitForTimeout(220)
await assertNoPageOverflow("subscriptions desktop")
await page.screenshot({ path: "../docs/screenshots/subscriptions-desktop.png", fullPage: true })

await page.getByRole("tab", { name: "节点库存" }).click()
await page.getByText("Tokyo Hysteria2", { exact: true }).first().waitFor()
await assertNoPageOverflow("nodes desktop")
await page.screenshot({ path: "../docs/screenshots/nodes-by-subscription-desktop.png", fullPage: true })

await page.setViewportSize({ width: 1024, height: 900 })
await page.waitForTimeout(220)
const filterButtonsOverlap = await page.evaluate(() => {
  const buttons = [...document.querySelectorAll("[data-node-filter]")].map((element) => element.getBoundingClientRect())
  return buttons.some((left, index) => buttons.slice(index + 1).some((right) =>
    left.left < right.right && left.right > right.left && left.top < right.bottom && left.bottom > right.top,
  ))
})
if (filterButtonsOverlap) throw new Error("node filter buttons overlap at 1024px")
await assertNoPageOverflow("nodes medium desktop")
await page.screenshot({ path: "../docs/screenshots/nodes-filters-medium.png", fullPage: true })
await page.setViewportSize({ width: 1440, height: 960 })

await page.goto(`${baseURL}/#/about`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "关于 HX-Pxy" }).waitFor()
await page.getByText("sudo hx-pxy-install upgrade", { exact: true }).waitFor()
await page.waitForTimeout(220)
await assertNoPageOverflow("about desktop")
await page.screenshot({ path: "../docs/screenshots/about-desktop.png", fullPage: true })

await page.setViewportSize({ width: 390, height: 844 })
for (const route of ["subscriptions", "nodes", "about"]) {
  await page.goto(`${baseURL}/#/${route}`, { waitUntil: "networkidle" })
  if (route === "nodes") await page.getByRole("tab", { name: "节点库存" }).click()
  await page.waitForTimeout(220)
  await assertNoPageOverflow(`${route} mobile`)
  await page.screenshot({ path: `../docs/screenshots/${route}-mobile.png`, fullPage: true })
}

await browser.close()
