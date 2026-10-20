#!/usr/bin/env python3
"""Gmail TOTP 实测 (Playwright Chromium): 真实走 accounts.google.com 登录.
用法: python scripts/gmail_totp_login_test_pw.py email:pwd:seed [email:pwd:seed ...]
"""
import asyncio
import base64
import hashlib
import hmac
import json
import re
import struct
import sys
import time
from pathlib import Path

from playwright.async_api import async_playwright

PROXY = "http://127.0.0.1:2334"
SHOT = Path(".run/totp-login-test")
SHOT.mkdir(parents=True, exist_ok=True)


def totp(seed: str) -> str:
    s = re.sub(r"[^A-Za-z2-7]", "", str(seed).upper())
    key = base64.b32decode(s)
    counter = int(time.time() // 30)
    h = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    o = h[-1] & 0x0F
    return f"{(struct.unpack('>I', h[o:o + 4])[0] & 0x7FFFFFFF) % 1_000_000:06d}"


async def login(page, email: str, pwd: str, seed: str, tag: str) -> dict:
    out = {"email": email, "tag": tag}
    await page.goto("https://accounts.google.com", wait_until="domcontentloaded", timeout=60000)
    await page.wait_for_timeout(2000)
    await page.screenshot(path=str(SHOT / f"{tag}-0.png"))

    # 使用其他账号
    try:
        link = page.locator("#identifierLink")
        if await link.count():
            await link.first.click()
            await page.wait_for_timeout(800)
    except Exception:
        pass

    # 邮箱
    try:
        ident = page.locator('input[name="identifier"]')
        await ident.first.wait_for(state="visible", timeout=15000)
        await ident.first.fill(email)
        await page.locator("#identifierNext").first.click()
    except Exception as e:
        out["stage"] = f"email_fail {e}"
        return out

    # 密码
    pw = None
    for _ in range(25):
        pw = page.locator('input[name="Passwd"], input[type="password"]').first
        try:
            await pw.wait_for(state="visible", timeout=1000)
            break
        except Exception:
            pw = None
        await page.wait_for_timeout(1000)
    if not pw:
        out["stage"] = "no_pw_field"
        out["html"] = (await page.content())[:1500]
        return out
    await pw.fill(pwd)
    await page.locator("#passwordNext").first.click()
    await page.wait_for_timeout(2500)
    out["after_pw_url"] = page.url
    out["after_pw_text"] = (await page.locator("body").inner_text())[:600]

    # 等待 TOTP 挑战 / 登录成功 / 错误
    totp_el = None
    for _ in range(30):
        await page.wait_for_timeout(1000)
        url = page.url
        for sel in ('input[autocomplete="one-time-code"]', "#totpPin", 'input[name="totpPin"]',
                    'input[type="tel"][name="idvPin"]'):
            el = page.locator(sel).first
            if await el.count() and await el.is_visible():
                totp_el = el
                break
        if totp_el:
            break
        if "myaccount.google.com" in url:
            break
        html = await page.content()
        if re.search(r"couldn.?t sign|cannot sign|无法登录|disabled|暂时无法", html, re.I):
            break
    await page.screenshot(path=str(SHOT / f"{tag}-1-afterpw.png"))

    if not totp_el:
        url = page.url
        html = await page.content()
        text = (await page.locator("body").inner_text())[:600]
        if "myaccount.google.com" in url:
            out["stage"] = "LOGGED_IN_NO_2FA_PROMPT"  # 账号根本没开 TOTP 2FA
        elif re.search(r"wrong password|couldn.?t find your Google Account|无法找到您的 Google 账号|密码不正确|wrong.*password", text + html, re.I):
            out["stage"] = "WRONG_PASSWORD"
        else:
            out["stage"] = "NO_TOTP_CHALLENGE"
        out["url"] = url
        out["text"] = text
        out["html"] = html[:1500]
        return out

    code = totp(seed)
    out["code_used"] = code
    await totp_el.fill(code)
    await page.wait_for_timeout(500)
    nxt = page.locator("#totpNext, button[type=submit]").first
    if await nxt.count():
        await nxt.click()

    verdict = "PENDING"
    for _ in range(25):
        await page.wait_for_timeout(1000)
        url = page.url
        html = await page.content()
        if "myaccount.google.com" in url or "accounts.google.com/signin/v2/success" in url:
            verdict = "ACCEPTED"
            break
        if re.search(r"验证码不正确|incorrect.*code|invalid.*code|wrong.*code", html, re.I):
            verdict = "REJECTED_WRONG_CODE"
            break
        if re.search(r"couldn.?t sign|cannot sign|无法登录|disabled", html, re.I):
            verdict = "BLOCKED"
            break
    out["stage"] = verdict
    out["url"] = page.url
    await page.screenshot(path=str(SHOT / f"{tag}-2-{verdict.lower()}.png"))
    out["text"] = (await page.locator("body").inner_text())[:500]
    out["html"] = (await page.content())[:2000]
    return out


async def main() -> None:
    accounts = [a.split(":") for a in sys.argv[1:]]
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True, proxy={"server": PROXY})
        ctx = await browser.new_context(locale="en-US", user_agent=(
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
            "(KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"))
        page = await ctx.new_page()
        results = []
        for i, (em, pw, seed) in enumerate(accounts):
            tag = f"{i}-{re.sub(r'[^a-zA-Z0-9]', '', em)[:16]}"
            print(f"=== [{i + 1}/{len(accounts)}] {em} ===", flush=True)
            try:
                r = await login(page, em, pw, seed, tag)
            except Exception as e:
                r = {"email": em, "stage": f"EXC {e}"}
            print(json.dumps(r, ensure_ascii=False)[:900], flush=True)
            results.append(r)
            await ctx.clear_cookies()
        await browser.close()
        print("=== RESULTS ===", flush=True)
        print(json.dumps(results, ensure_ascii=False, indent=1), flush=True)


if __name__ == "__main__":
    asyncio.run(main())
