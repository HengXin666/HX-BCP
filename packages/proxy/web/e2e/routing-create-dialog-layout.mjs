import { chromium } from "playwright"

const baseURL = process.env.HX_UI_BASE_URL
const session = process.env.HX_UI_SESSION_COOKIE
if (!baseURL || !session) throw new Error("HX_UI_BASE_URL and HX_UI_SESSION_COOKIE are required")

const browser = await chromium.launch()
const context = await browser.newContext({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1 })
await context.addCookies([{ name: "hx_session", value: session, domain: new URL(baseURL).hostname, path: "/" }])
const page = await context.newPage()

async function metrics() {
  return page.evaluate(() => {
    const list = document.querySelector("main section.flex")
    const rows = [...document.querySelectorAll("main .divide-y > div")]
    return {
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      listWidth: list ? Math.round(list.getBoundingClientRect().width) : 0,
      rowHeights: rows.map((row) => Math.round(row.getBoundingClientRect().height)),
      expanded: document.querySelectorAll('button[aria-label="收起节点"]').length,
      inlineForms: document.querySelectorAll("main form").length,
    }
  })
}

await page.goto(`${baseURL}/#/routing`, { waitUntil: "networkidle" })
await page.getByRole("heading", { name: "代理服务" }).waitFor()
await page.waitForTimeout(1500)

const closed = await metrics()
// The create form lives in a dialog now: the page itself owns no form, and the
// list therefore spans the full content width instead of sharing it.
if (closed.inlineForms !== 0) throw new Error(`routing page keeps ${closed.inlineForms} inline form(s); the create form must be a dialog`)
if (closed.expanded !== 0) throw new Error(`${closed.expanded} service row(s) start expanded; rows must start collapsed`)
if (closed.scrollWidth > closed.clientWidth) throw new Error(`routing page overflows horizontally: ${JSON.stringify(closed)}`)
if (closed.listWidth < 900) throw new Error(`service list is too narrow for its rows: ${closed.listWidth}px`)
for (const [index, height] of closed.rowHeights.entries()) {
  if (height > 80) throw new Error(`service row ${index} is ${height}px tall; a collapsed row must stay one line`)
}

await page.getByRole("button", { name: "新建代理服务" }).click()
const dialog = page.getByRole("dialog")
await dialog.waitFor()
await dialog.getByRole("heading", { name: "新建代理服务" }).waitFor()
if (!(await dialog.getByRole("button", { name: "创建并启动" }).isVisible())) throw new Error("create dialog is missing its submit action")

await page.keyboard.press("Escape")
await page.waitForTimeout(400)
if (await page.getByRole("dialog").count() !== 0) throw new Error("Escape did not close the create dialog")

// A narrow viewport must still fit: the row is the only horizontally demanding
// element, and two of them stacked is where truncation has to hold.
await page.setViewportSize({ width: 1024, height: 800 })
await page.reload({ waitUntil: "networkidle" })
await page.getByRole("heading", { name: "代理服务" }).waitFor()
await page.waitForTimeout(1200)
const narrow = await metrics()
if (narrow.scrollWidth > narrow.clientWidth) throw new Error(`routing page overflows at 1024: ${JSON.stringify(narrow)}`)

await browser.close()
console.log("routing-create-dialog-layout: ok — 创建走弹窗, 列表独占整行, 行高单行, 无水平溢出;", JSON.stringify({ listWidth: closed.listWidth, rows: closed.rowHeights.length }))
