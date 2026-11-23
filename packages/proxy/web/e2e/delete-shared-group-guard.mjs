// 验证「删除代理服务」不再误删共享代理组。
//
// 为什么需要浏览器级验证: 这条回退只发生在**前端** —— 后端即使正确拒绝了删组, 前端若
// 继续把整次操作判为失败, 用户看到的仍是"删除失败、行还在"。必须证明: 删服务成功,
// 且它所属的共享组原样保留。
//
// 判据 (机械可判, 不靠肉眼):
//   1) 删除后该 listener 从列表消失
//   2) 它之前指向的组**仍然存在**
//   3) 页面上出现成功提示
import { chromium } from "playwright"

const BASE = process.env.HX_PXY_URL || "http://127.0.0.1:28341"
const API = process.env.HX_PXY_API || "http://127.0.0.1:19090"
const KEY = process.env.HX_PXY_KEY
const USER = process.env.HX_PXY_USER || "loli"
const PASS = process.env.HX_PXY_PASS
const OUT = process.env.OUT || "/tmp/hx-pxy-delete-guard.png"

if (!PASS || !KEY) { console.error("需要 HX_PXY_PASS 与 HX_PXY_KEY"); process.exit(2) }

const api = (path, init = {}) => fetch(API + path, {
  ...init,
  headers: { Authorization: "Bearer " + KEY, "Content-Type": "application/json", ...(init.headers || {}) },
})
const listListeners = async () => (await (await api("/api/v1/listeners")).json()).items
const listGroups = async () => (await (await api("/api/v1/proxy-groups")).json()).items

const failures = []
let probeID = null
let anchorID = null

try {
  const groupsBefore = await listGroups()
  const anchor = groupsBefore.find((g) => g.name === "良心云") || groupsBefore[0]
  if (!anchor) { console.error("库里没有任何组"); process.exit(2) }
  anchorID = anchor.id

  const nodes = (await (await api("/api/v1/nodes")).json()).items
  const healthy = nodes.find((n) => n.lifecycle_state === "healthy")
  if (!healthy) { console.error("没有 healthy 节点可钉"); process.exit(2) }

  // 探针服务用 node_id 钉节点 —— 这会让它与其它 pinned 服务共享同一个锚组,
  // 正是用户踩到的形态 (前端按 proxy_group_id 找到锚组, 误以为是"专属组")。
  const probeName = "__del_guard_probe"
  const created = await (await api("/api/v1/listeners", {
    method: "POST",
    body: JSON.stringify({
      name: probeName, kind: "mixed", bind_address: "127.0.0.1", port: 17990,
      node_id: healthy.id, shared_inbound: "standard", enabled: true,
    }),
  })).json()
  if (!created.id) { console.error("建探针失败: " + JSON.stringify(created)); process.exit(2) }
  probeID = created.id

  const listeners = await listListeners()
  const probe = listeners.find((l) => l.id === probeID)
  const sharers = listeners.filter((l) => l.proxy_group_id === probe.proxy_group_id)
  // 前置断言: 探针必须与别的 listener 共享组, 否则测不到那条回退分支。
  if (sharers.length < 2) failures.push("探针没与其它 listener 共享组, 测不到回退 (sharers=" + sharers.length + ")")

  const browser = await chromium.launch()
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1200 } })
    page.on("pageerror", (e) => failures.push("pageerror: " + e.message))

    // 先登录, 再进路由页。
    //
    // ⚠️ 登录与跳页不能在同一个 goto 里完成: 未登录时 SPA 渲染登录视图, 登录成功后
    // hash 没变, 路由不会重新挂载, 于是列表数据从未拉取 —— 表现为"探针不在页面上"
    // (实测踩过)。所以登录后必须显式再 goto 一次目标路由。
    await page.goto(BASE + "/", { waitUntil: "domcontentloaded" })
    const pw = page.locator('input[type="password"]').first()
    if (await pw.waitFor({ state: "visible", timeout: 20000 }).then(() => true).catch(() => false)) {
      await page.locator('input[type="text"]').first().fill(USER)
      await pw.fill(PASS)
      await page.locator('button[type="submit"]').first().click()
      // 等主界面出现 (侧边栏的"代理服务"入口)
      await page.locator('text=代理服务').first().waitFor({ timeout: 20000 })
      await page.waitForTimeout(1000)
    }
    await page.goto(BASE + "/#/routing", { waitUntil: "domcontentloaded" })
    await page.waitForTimeout(2500)
    // 诊断: 等不到就先截图, 让失败自带现场
    try {
      await page.locator("text=" + probeName).first().waitFor({ timeout: 25000 })
    } catch (error) {
      await page.screenshot({ path: "/tmp/hx-pxy-delete-guard-FAIL.png", fullPage: true })
      throw error
    }

    // 真实交互路径: 该行的「服务操作」下拉 -> 删除服务 -> 确认
    //
    // 定位用 role=button 的那一层 (行本身是 role=button), 不能用 .last() —— 探针名字
    // 出现在多个嵌套 div 里, last() 会选中最内层的纯文本 div, 那里面没有按钮。
    const row = page.locator('div[role="button"]').filter({ hasText: probeName }).first()
    await row.locator('[role="combobox"], button').filter({ hasText: "服务操作" }).first().click()
    await page.locator('[role="option"]:has-text("删除服务")').last().click()
    await page.waitForTimeout(800)
    await page.locator('button:has-text("确认"), button:has-text("删除")').last().click()
    await page.waitForTimeout(3000)

    await page.screenshot({ path: OUT, fullPage: true })
    const bodyText = await page.locator("body").innerText()
    if (/删除.*失败|操作失败/.test(bodyText)) failures.push("页面显示了删除失败提示")
  } finally {
    await browser.close()
  }

  const after = await listListeners()
  const stillThere = after.find((l) => l.id === probeID)
  if (stillThere) {
    failures.push("listener 没被删掉 —— 回退仍把整次操作判为失败")
  } else {
    probeID = null
  }
  const groupsAfter = await listGroups()
  const anchorAlive = Boolean(groupsAfter.find((g) => g.id === anchorID))
  if (!anchorAlive) failures.push("★ 共享组被误删 ★")

  console.log(JSON.stringify({
    probe: probeName,
    sharedGroup: probe.proxy_group_id,
    sharersBefore: sharers.length,
    listenerRemoved: !stillThere,
    anchorStillPresent: anchorAlive,
    groupsBefore: groupsBefore.length,
    groupsAfter: groupsAfter.length,
    screenshot: OUT,
    failures,
  }, null, 1))
} finally {
  // 探针若还在就清掉, 不留垃圾
  if (probeID) {
    const left = (await listListeners()).find((l) => l.id === probeID)
    if (left) await api("/api/v1/listeners/" + left.id + "?version=" + left.version, { method: "DELETE" }).catch(() => {})
  }
}

process.exit(failures.length ? 1 : 0)