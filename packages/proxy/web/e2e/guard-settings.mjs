// 抓「设置 -> 性能与运行」页的截图, 验证「禁止 API 新增服务组」开关真的渲染出来了。
//
// 为什么需要它: 后端闸门有单元测试与 curl 实测, 但用户要的是一个**前端选项**。
// 光有 API 字段不满足需求 —— 必须证明这个勾选框在真实页面里可见、且能反映当前值。
import { chromium } from "playwright"

const BASE = process.env.HX_PXY_URL || "http://127.0.0.1:28341"
const USER = process.env.HX_PXY_USER || "loli"
const PASS = process.env.HX_PXY_PASS
const OUT = process.env.OUT || "/tmp/hx-pxy-guard-settings.png"

if (!PASS) {
  console.error("需要 HX_PXY_PASS")
  process.exit(2)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 1200 } })
const problems = []

page.on("console", (message) => {
  if (message.type() === "error") problems.push("console: " + message.text())
})
page.on("pageerror", (error) => problems.push("pageerror: " + error.message))

await page.goto(BASE + "/#/settings", { waitUntil: "domcontentloaded" })

// 登录表单是 React 受控组件, 必须用 fill 触发 input 事件。
//
// ⚠️ isVisible() **不等待** —— 页面处于 domcontentloaded 时 React 还没挂载, 它会立刻返回
// false, 于是整个登录块被跳过、截图停在登录页 (实测踩过)。必须先 waitFor 挂载。
const passwordField = page.locator('input[type="password"]').first()
const hasLoginForm = await passwordField
  .waitFor({ state: "visible", timeout: 20000 })
  .then(() => true)
  .catch(() => false)
if (hasLoginForm) {
  await page.locator('input[autocomplete="username"], input[type="text"]').first().fill(USER)
  await passwordField.fill(PASS)
  await page.locator('button[type="submit"]').first().click()
  // 登录成功后 App 会切到主界面; 等设置页的标签栏出现, 不靠固定 sleep。
  await page.locator('[role="tab"], button:has-text("外观")').first().waitFor({ timeout: 15000 })
  await page.waitForTimeout(800)
}

await page.goto(BASE + "/#/settings", { waitUntil: "domcontentloaded" })
await page.waitForTimeout(1500)

// 标签文案是「运行」(不是「性能与运行」—— 那是描述文字, 实测按它选不中)。
const runtimeTab = page.getByRole("tab", { name: "运行", exact: true })
await runtimeTab.click({ timeout: 15000 })
await page.waitForTimeout(1200)

const label = page.locator("text=禁止 API 新增服务组").first()
const visible = await label.isVisible().catch(() => false)
if (!visible) {
  problems.push("找不到「禁止 API 新增服务组」开关")
}

// 读出开关当前状态 —— 服务端此刻是 true, 前端必须反映出来。
let checked = null
if (visible) {
  const row = label.locator("xpath=ancestor::div[contains(@class,'flex')][1]")
  const sw = row.locator('button[role="checkbox"], input[type="checkbox"]').first()
  if (await sw.count()) {
    const state = await sw.getAttribute("data-state").catch(() => null)
    checked = state ? state === "checked" : await sw.isChecked().catch(() => null)
  } else {
    problems.push("开关存在但找不到 checkbox 控件")
  }
}

await label.scrollIntoViewIfNeeded().catch(() => {})
await page.waitForTimeout(500)
await page.screenshot({ path: OUT, fullPage: true })

console.log(JSON.stringify({
  url: page.url(),
  switchVisible: visible,
  switchChecked: checked,
  screenshot: OUT,
  problems,
}, null, 1))

await browser.close()
process.exit(problems.length ? 1 : 0)
