import { chromium } from "playwright"

/*
 * Docker 列表滚动门禁。
 *
 * 用户报告 "docker 页面无法滚动列表"。这一页挂在终端页里, 而终端页的 main 在
 * lg 断点是 lg:h-full lg:overflow-hidden; 一旦滚动容器不是**表格区域自己**,
 * 列表就会在某个视口下被裁掉或滚不动。
 *
 * 本脚本不依赖真实登录与真实 2FA: 它按路由打桩 /api/v1/** 并注入 40 个容器,
 * 对真实 Vite dev server 跑, 只断言 "在浏览器里才看得见" 的行为:
 *
 *   1. 表格区域是**唯一**的纵向滚动容器, 且就是 <table> 的父元素;
 *   2. 滚轮滚动有效, 拖动滚动条有效, 都能滚到最后一个容器行;
 *   3. 滚到底时最后一行完整可见(不被裁), 页面说明仍在表格下方;
 *   4. 整页(lg 及以上)不出现文档级横向溢出或纵向溢出。
 *
 *   HX_UI_BASE_URL=http://127.0.0.1:28341 node e2e/docker-scroll-guard.mjs
 *
 * 所有 API 都打桩; 不连接真实管理面, 也不需要机器凭据。
 */
const baseURL = process.env.HX_UI_BASE_URL
if (!baseURL) throw new Error("HX_UI_BASE_URL is required")
const containerCount = Number(process.env.HX_DOCKER_CONTAINERS || "40")

const containers = []
for (let index = 0; index < containerCount; index += 1) {
  containers.push({
    id: "c" + index,
    name: "hx-service-" + index,
    image: "hx/proxygroup:" + index,
    state: index % 4 === 0 ? "exited" : "running",
    status: index % 4 === 0 ? "Exited (0) 2 hours ago" : "Up 3 hours",
    ports: "0.0.0.0:2834" + index + "->8000/tcp, [::]:2834" + index + "->8000/tcp",
    created: "2026-09-18 12:00:00 +0800 CST",
    cpu_perc: "1.0%",
    mem_usage: "12.5MiB / 1GiB",
    mem_perc: "1.2%",
    net_io: "1.2kB / 3.4kB",
    block_io: "0B / 4.1kB",
  })
}

const terminalStatus = {
  enabled: true,
  active_sessions: 0,
  max_sessions: 2,
  idle_timeout_seconds: 0,
  max_lifetime_seconds: 0,
  privileged: false,
  two_factor_configured: true,
  two_factor_enabled: true,
  two_factor_verified: true,
  two_factor_verification_ttl_seconds: 900,
}

async function stubRoutes(page) {
  // Catch-all first: Playwright matches the most recently registered route.
  await page.route("**/api/v1/**", (route) => {
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [] }) })
  })
  await page.route("**/api/v1/docker/containers", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ containers }) }))
  await page.route("**/api/v1/terminal/status", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(terminalStatus) }))
  await page.route("**/api/v1/auth/status", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ configured: true, authenticated: true, username: "scroll-guard", csrf_token: "scroll-guard" }),
    }))
}

// Runs inside the page: measure the scroll container, the last row and the
// document. Written as one evaluation so every reading describes one moment.
function measure() {
  const table = document.querySelector("table")
  if (!table) return { tableFound: false }
  const scroller = table.parentElement
  const scrollStyle = getComputedStyle(scroller)
  const lastRow = table.querySelector("tbody tr:last-child")
  const footer = [...document.querySelectorAll("p")].find((node) => (node.textContent || "").includes("只读视图"))
  const scrollableVertically = [...document.querySelectorAll("*")].filter((element) => {
    const style = getComputedStyle(element)
    return (style.overflowY === "auto" || style.overflowY === "scroll") && element.scrollHeight > element.clientHeight + 4
  })
  const scrollerBox = scroller.getBoundingClientRect()
  const lastRowBox = lastRow ? lastRow.getBoundingClientRect() : null
  return {
    tableFound: true,
    scrollerTag: scroller.tagName,
    scrollerOverflowY: scrollStyle.overflowY,
    scrollerOverflowX: scrollStyle.overflowX,
    scrollerScrollTop: scroller.scrollTop,
    scrollerScrollHeight: scroller.scrollHeight,
    scrollerClientHeight: scroller.clientHeight,
    scrollerClientWidth: scroller.clientWidth,
    scrollerScrollWidth: scroller.scrollWidth,
    scrollerMaxScrollTop: scroller.scrollHeight - scroller.clientHeight,
    verticalScrollerCount: scrollableVertically.length,
    otherVerticalScrollers: scrollableVertically
      .filter((element) => element !== scroller)
      .map((element) => (element.className || "").toString().slice(0, 80)),
    tableWidth: Math.round(table.getBoundingClientRect().width),
    scrollerWidth: Math.round(scrollerBox.width),
    lastRowText: lastRow ? (lastRow.textContent || "").trim().slice(0, 40) : null,
    lastRowFullyVisible: lastRowBox
      ? lastRowBox.top >= scrollerBox.top - 1 && lastRowBox.bottom <= scrollerBox.bottom + 1
      : false,
    footerVisible: footer ? footer.getBoundingClientRect().height > 0 : false,
    footerBelowTable: footer ? footer.getBoundingClientRect().top >= scrollerBox.bottom - 1 : false,
    documentOverflowX: document.documentElement.scrollWidth > window.innerWidth + 1,
    documentOverflowY: document.documentElement.scrollHeight > window.innerHeight + 1,
    docOverflowPx: Math.max(0, document.documentElement.scrollHeight - window.innerHeight),
    scrollbarWidth: Math.round(scroller.offsetWidth - scroller.clientWidth),
  }
}

// boundedShell: 该视口落在 App 壳的 lg 分支(lg:h-screen + lg:overflow-hidden),
// 整页必须完全落在视口内, 有文档级滚动条就是高度约定又漏了。lg 以下壳本身就是
// 文档流(内容高度), 页面比视口高几十像素是壳的既有行为 —— 那里只断言列表仍由
// 表格区域滚动, 文档溢出只做一个宽松上限(见 problems 判定)。
const viewports = [
  { width: 1280, height: 720, strict: true, boundedShell: true },
  { width: 900, height: 700, strict: true, boundedShell: false },
  { width: 1440, height: 900, strict: false, boundedShell: true },
  { width: 390, height: 844, strict: false, boundedShell: false },
]

const browser = await chromium.launch({ executablePath: process.env.HX_UI_CHROMIUM || "/usr/bin/chromium", headless: true })
const failures = []
const report = []

for (const viewport of viewports) {
  const page = await browser.newPage({ viewport: { width: viewport.width, height: viewport.height }, deviceScaleFactor: 1 })
  const consoleErrors = []
  page.on("pageerror", (error) => consoleErrors.push(String(error.message)))
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text().slice(0, 160)) })

  await stubRoutes(page)
  await page.goto(baseURL + "/#/terminal", { waitUntil: "networkidle" })
  await page.waitForTimeout(1500)
  await page.getByRole("tab", { name: "Docker" }).click()
  await page.waitForTimeout(1500)

  const table = page.locator("table")
  if (await table.count() === 0) {
    failures.push(viewport.width + "x" + viewport.height + ": 容器表格未渲染")
    report.push({ viewport, skipped: true })
    await page.close()
    continue
  }

  const before = await page.evaluate(measure)

  // 1) 滚轮: 落在表格区域正中, 向下滚。
  const box = await table.boundingBox()
  await page.mouse.move(box.x + Math.min(box.width / 2, 300), box.y + Math.min(box.height / 2, 80))
  await page.mouse.wheel(0, 600)
  await page.waitForTimeout(400)
  const afterWheel = await page.evaluate(measure)

  // 2) 拖动滚动条: 从当前滑块位置往下拖。若表格区域不是滚动容器, 这里不会动。
  //    坐标必须用滚动容器自己的 border box —— <table> 的 boundingBox 是内容尺寸,
  //    比容器窄一个滚动条, 用它会点在滚动条外面(实测 2px 差值就足以拖不动)。
  //    先把位置回到中点: 滚轮可能已经到底, 滑块顶到底部就再也拖不动了(假阴性)。
  if (afterWheel.scrollbarWidth > 0) {
    await page.evaluate(() => {
      const scroller = document.querySelector("table").parentElement
      scroller.scrollTop = Math.floor((scroller.scrollHeight - scroller.clientHeight) / 2)
    })
    await page.waitForTimeout(200)
  }
  const beforeDrag = await page.evaluate(measure)
  if (afterWheel.scrollbarWidth > 0) {
    const track = await page.evaluate(() => {
      const table = document.querySelector("table")
      const scroller = table.parentElement
      const rect = scroller.getBoundingClientRect()
      return { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
    })
    const trackX = track.x + track.width - afterWheel.scrollbarWidth / 2
    const ratio = afterWheel.scrollerMaxScrollTop > 0 ? afterWheel.scrollerScrollTop / afterWheel.scrollerMaxScrollTop : 0
    const thumbHeight = Math.max(24, track.height * (afterWheel.scrollerClientHeight / afterWheel.scrollerScrollHeight))
    const thumbY = track.y + ratio * (track.height - thumbHeight) + thumbHeight / 2
    await page.mouse.move(trackX, thumbY)
    await page.mouse.down()
    await page.mouse.move(trackX, track.y + track.height - 4, { steps: 8 })
    await page.mouse.up()
  }
  await page.waitForTimeout(300)
  const afterDrag = await page.evaluate(measure)

  // 3) 直接设到底, 验证最后一行完整可见。
  await page.evaluate(() => {
    const table = document.querySelector("table")
    const scroller = table.parentElement
    scroller.scrollTop = scroller.scrollHeight
  })
  await page.waitForTimeout(300)
  const atBottom = await page.evaluate(measure)

  const label = viewport.width + "x" + viewport.height
  const problems = []
  if (before.verticalScrollerCount !== 1) {
    problems.push("纵向滚动容器数量 = " + before.verticalScrollerCount + " (want 1); 其他: " + JSON.stringify(before.otherVerticalScrollers))
  }
  if (before.scrollerTag !== "DIV") problems.push("滚动容器不是 <table> 的父 div")
  if (before.scrollerOverflowY !== "auto" && before.scrollerOverflowY !== "scroll") {
    problems.push("表格区域 overflow-y = " + before.scrollerOverflowY)
  }
  if (afterWheel.scrollerScrollTop <= before.scrollerScrollTop) {
    problems.push("滚轮没有滚动表格区域 (" + before.scrollerScrollTop + " -> " + afterWheel.scrollerScrollTop + ")")
  }
  if (afterWheel.scrollbarWidth === 0) {
    problems.push("表格区域没有可见的纵向滚动条, 无法验证拖动")
  } else if (afterDrag.scrollerScrollTop <= beforeDrag.scrollerScrollTop) {
    problems.push("拖动滚动条没有继续滚动 (" + beforeDrag.scrollerScrollTop + " -> " + afterDrag.scrollerScrollTop + ")")
  }
  if (Math.abs(atBottom.scrollerScrollTop - atBottom.scrollerMaxScrollTop) > 2) {
    problems.push("无法滚到内容末尾 (scrollTop " + atBottom.scrollerScrollTop + " / max " + atBottom.scrollerMaxScrollTop + ")")
  }
  if (!atBottom.lastRowFullyVisible) problems.push("最后一个容器行没有完整可见")
  if (!atBottom.footerBelowTable) problems.push("底部说明被挤到表格上方或不可见")
  if (before.documentOverflowX) problems.push("整页横向溢出")
  if (viewport.strict) {
    // 窄视口下表格自身可以横向滚动, 但滚动必须发生在表格区域内。
    if (before.scrollerScrollWidth > before.scrollerClientWidth && before.scrollerOverflowX !== "auto" && before.scrollerOverflowX !== "scroll") {
      problems.push("表格比容器宽却没有横向滚动能力")
    }
  }
  if (viewport.boundedShell && before.documentOverflowY) {
    problems.push("整页纵向溢出 (文档级滚动条出现)")
  }
  // lg 以下(900x700 / 390x844)终端页的滚动区域没有确定高度, 文档本身会滚几十像素:
  // 这是 App 壳在窄视口的既有行为, 不是这一页的回归。此时只要求它不被撑爆得离谱,
  // 并且列表仍然由表格区域滚动(verticalScrollerCount === 1, 上面已断言)。
  if (!viewport.boundedShell && before.docOverflowPx > 200) {
    problems.push("整页纵向溢出 " + before.docOverflowPx + "px, 超过容忍上限 200px")
  }
  if (consoleErrors.length > 0) problems.push("控制台错误: " + consoleErrors.slice(0, 2).join(" / "))

  report.push({ viewport: label, before, afterWheel, beforeDrag, afterDrag, atBottom, problems })
  for (const problem of problems) failures.push(label + ": " + problem)

  if (viewport.strict) {
    await page.screenshot({
      path: "../docs/screenshots/terminal-docker-scroll-" + viewport.width + "x" + viewport.height + ".png",
      fullPage: false,
    })
  }
  await page.close()
}

await browser.close()

for (const entry of report) {
  console.log(JSON.stringify(entry))
}

if (failures.length > 0) {
  console.log("FAIL Docker 列表滚动门禁:")
  for (const failure of failures) console.log("  - " + failure)
  process.exit(1)
}
console.log("ok   Docker 列表滚动门禁 (滚动容器唯一/滚轮/拖动/末行可见)")
