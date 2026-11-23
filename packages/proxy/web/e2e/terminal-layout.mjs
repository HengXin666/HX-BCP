import { chromium } from "playwright"
import crypto from "node:crypto"

// Regression test for the terminal bottom-clipping bug.
//
// FitAddon derives rows from the mount point's computed height and subtracts only the
// .xterm child's own padding — which xterm.css never sets. Tailwind preflight sets
// box-sizing: border-box globally, so a padded mount point reports a height that already
// includes its padding and the subtraction is skipped. The grid then renders one row
// taller than the space available and the card's overflow-hidden eats the last row.
//
// This measures the real page rather than the arithmetic, because only the browser knows
// the used values.

const baseURL = process.env.HX_UI_BASE_URL
if (!baseURL) throw new Error("HX_UI_BASE_URL is required")
const username = process.env.HX_UI_USERNAME || "terminal-layout"
const password = process.env.HX_UI_PASSWORD || "terminal-layout-password-2026"
const secret = process.env.HX_TERMINAL_TOTP
if (!secret) throw new Error("HX_TERMINAL_TOTP is required (Base32 TOTP secret)")

function totp(secret, at = Date.now()) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
  let bits = ""
  for (const ch of secret.replace(/=+$/, "").toUpperCase()) {
    const i = alphabet.indexOf(ch)
    if (i < 0) continue
    bits += i.toString(2).padStart(5, "0")
  }
  const key = Buffer.alloc(Math.floor(bits.length / 8))
  for (let i = 0; i < key.length; i++) key[i] = parseInt(bits.slice(i * 8, i * 8 + 8), 2)
  const buf = Buffer.alloc(8)
  buf.writeBigUInt64BE(BigInt(Math.floor(at / 1000 / 30)))
  const digest = crypto.createHmac("sha1", key).update(buf).digest()
  const offset = digest[digest.length - 1] & 0x0f
  const binary = ((digest[offset] & 0x7f) << 24) | (digest[offset + 1] << 16) | (digest[offset + 2] << 8) | digest[offset + 3]
  return String(binary % 1000000).padStart(6, "0")
}

const browser = await chromium.launch({ executablePath: process.env.HX_UI_CHROMIUM || "/usr/bin/chromium", headless: true })
const failures = []
try {
  for (const viewport of [{ width: 1440, height: 900 }, { width: 1280, height: 800 }, { width: 1600, height: 1000 }]) {
    const page = await browser.newPage({ viewport, deviceScaleFactor: 1 })
    await page.goto(baseURL, { waitUntil: "networkidle" })
    const setupField = page.getByLabel("一次性初始化 Token")
    if (await setupField.isVisible().catch(() => false)) {
      await setupField.fill(process.env.HX_UI_SETUP_TOKEN || "")
      await page.getByLabel("用户名").fill(username)
      await page.getByLabel("密码").fill(password)
      await page.getByRole("button", { name: "初始化并登录" }).click()
    } else {
      await page.getByLabel("用户名").fill(username)
      await page.getByLabel("密码").fill(password)
      await page.getByRole("button", { name: "登录", exact: true }).click()
    }
    await page.getByRole("heading", { name: "总览" }).waitFor()

    await page.goto(baseURL + "/#/terminal", { waitUntil: "networkidle" })
    await page.waitForTimeout(1500)
    if (await page.getByLabel("终端 2FA 验证码").isVisible().catch(() => false)) {
      await page.getByLabel("终端 2FA 验证码").fill(totp(secret))
      await page.getByRole("button", { name: "解锁" }).click()
      await page.waitForTimeout(2500)
    }
    await page.getByRole("button", { name: "连接", exact: true }).click()
    await page.waitForSelector(".xterm-rows", { timeout: 25000 })
    await page.waitForTimeout(1500)

    // Enough output that the grid must scroll rather than sit in the first page.
    await page.locator(".xterm").click({ position: { x: 60, y: 20 } })
    await page.keyboard.type("seq 1 300 | sed 's/^/terminal padding probe /'")
    await page.keyboard.press("Enter")
    await page.waitForTimeout(2000)

    const measured = await page.evaluate(() => {
      const surface = document.querySelector("[data-terminal-surface]")
      const rows = document.querySelector(".xterm-rows")
      const card = surface.closest("section")
      const bottom = (el) => el.getBoundingClientRect().bottom
      const pad = parseFloat(getComputedStyle(surface).paddingBottom) || 0
      const rowHeight = rows.firstElementChild ? rows.firstElementChild.getBoundingClientRect().height : 0
      return {
        surfacePadding: getComputedStyle(surface).padding,
        rowHeight: +rowHeight.toFixed(3),
        rowCount: rows.children.length,
        // > 0 means the grid is taller than the room the mount point offers.
        gridOverflowPx: +(bottom(rows) - (bottom(surface) - pad)).toFixed(2),
        // > 0 means rows are literally hidden behind the card edge.
        clippedByCardPx: +Math.max(0, bottom(rows) - bottom(card)).toFixed(2),
        withinViewportPx: +Math.max(0, bottom(rows) - window.innerHeight).toFixed(2),
      }
    })
    const label = viewport.width + "x" + viewport.height
    console.log(label, JSON.stringify(measured))
    if (measured.clippedByCardPx > 0.5) failures.push(label + ": " + measured.clippedByCardPx + "px of the grid is clipped by the card")
    if (measured.gridOverflowPx > 0.5) failures.push(label + ": grid overflows its mount point by " + measured.gridOverflowPx + "px")
    if (measured.withinViewportPx > 0.5) failures.push(label + ": terminal extends " + measured.withinViewportPx + "px past the viewport")
    await page.close()
  }
} finally {
  await browser.close()
}
if (failures.length) {
  console.error("terminal layout: FAIL")
  for (const f of failures) console.error("  " + f)
  process.exit(1)
}
console.log("terminal layout: no clipping at any tested viewport")
