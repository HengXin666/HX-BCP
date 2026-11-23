// 验证代理服务列表按**来源类别**分型, 并且分类不靠名字。
//
// 为什么必须浏览器级验证: 这一层是纯前端的派生 —— 后端只给出 node_id / source_spec /
// subscriptions.source_class 三个事实, "这一行该显示成什么" 是页面算出来的。后端测试
// 无法覆盖它, 而它的失败模式又恰恰是"页面看起来完全正常, 但说的是另一件事"。
//
// 判据 (机械可判):
//   1) 池 / 订阅 / 钉节点三类服务各自有对应的形态徽章 —— 它们不再渲染成同一行
//   2) 节点列表被**分页取全**: 生产库有 1300+ 节点而单页上限是 1000, 断言页面确实发出了
//      offset=1000 的第二页请求。旧代码只发一次 limit=1000, 于是所有由它派生的计数
//      (行上的 N/M 可用、健康统计、每订阅节点数) 都是在**残缺集合**上算的 —— 实测池服务
//      显示 `0/493 可用`, 而它当时有 1305 个成员。
//   3) 钉节点的服务展开后**只有 1 个成员**: 它此前按 proxy_group_id 解析, 显示的是共享锚组
//      的成员 (实测 ioa-vm-mixed 显示了良心云的 10 个节点), 与它自己的出口无关
import { chromium } from "playwright"

const baseURL = process.env.HX_UI_BASE_URL
const session = process.env.HX_UI_SESSION_COOKIE
if (!baseURL || !session) throw new Error("HX_UI_BASE_URL and HX_UI_SESSION_COOKIE are required")

const browser = await chromium.launch()
const context = await browser.newContext({ viewport: { width: 1600, height: 1200 }, deviceScaleFactor: 1 })
await context.addCookies([{ name: "hx_session", value: session, domain: new URL(baseURL).hostname, path: "/" }])
const page = await context.newPage()
const failures = []
// 记录节点列表的每一次请求, 用来证明分页 (而不是断言一个会随探测漂移的计数)。
const nodePages = []
page.on("request", (request) => {
  const url = request.url()
  if (!url.includes("/api/v1/nodes?")) return
  try { nodePages.push(Number(new URL(url).searchParams.get("offset") ?? "0")) } catch { /* ignore */ }
})
page.on("pageerror", (error) => failures.push("pageerror: " + error.message))

await page.goto(`${baseURL}/#/routing`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "代理服务" }).waitFor()
await page.waitForTimeout(2500)

async function rowTexts() {
  return page.evaluate(() => [...document.querySelectorAll("main .divide-y > div")]
    .map((element) => element.innerText.split("\n").map((line) => line.trim()).filter(Boolean)))
}

/** 展开一行并读它的成员表行数。 */
async function membersOf(name) {
  const row = page.locator("main .divide-y > div").filter({ hasText: name }).first()
  await row.click()
  await page.waitForTimeout(2500)
  const members = await page.evaluate(() => {
    const panel = document.querySelector('[role="tabpanel"]')
    return panel ? [...panel.querySelectorAll("div.grid.border-b")].map((element) => element.innerText.split("\n")[0]) : []
  })
  await row.click()
  await page.waitForTimeout(500)
  return members
}

const rows = await rowTexts()
const byName = (name) => rows.find((row) => row.some((line) => line === name))

// 1) 分类徽章: 三类来源必须各自成派。
for (const { name, badge, why } of [
  { name: "proxyscrape-pool-mixed", badge: "代理池", why: "source_class=pool 的订阅" },
  { name: "ioa-vm-mixed", badge: "指定节点", why: "listener.node_id 钉死单节点" },
  { name: "良心云 入口", badge: "订阅节点", why: "source_class=subscription 的订阅" },
]) {
  const row = byName(name)
  if (!row) { failures.push(`找不到服务行 ${name}`); continue }
  if (!row.includes(badge)) failures.push(`${name} (${why}) 没有「${badge}」徽章; 实际: ${JSON.stringify(row)}`)
}

// 2) 分页取全: 节点总数超过单页上限时, 必须看到第二页请求。
if (nodePages.length < 2) {
  failures.push(`节点列表只发了 ${nodePages.length} 次请求 (offsets: ${JSON.stringify(nodePages)}); 库存大于单页时必须继续翻页, 否则所有计数都在残缺集合上`)
} else if (Math.max(...nodePages) < 1000) {
  failures.push(`节点列表未请求第二页 (offsets: ${JSON.stringify(nodePages)})`)
}

// 3) 钉节点的服务只显示一个成员 (决定性)。
const pinnedMembers = await membersOf("ioa-vm-mixed")
if (pinnedMembers.length !== 1) {
  failures.push(`钉节点的服务显示了 ${pinnedMembers.length} 个成员, 期望 1: ${JSON.stringify(pinnedMembers.slice(0, 4))}`)
}

await page.screenshot({ path: process.env.OUT || "/tmp/hx-pxy-service-shapes.png", fullPage: false })
await browser.close()

if (failures.length) {
  console.error(JSON.stringify({ failures, nodePages }, null, 1))
  process.exit(1)
}
console.log("routing-service-shapes: ok — 池/订阅/指定节点分行分型, 节点列表分页取全, 钉节点的服务只有 1 个成员")
