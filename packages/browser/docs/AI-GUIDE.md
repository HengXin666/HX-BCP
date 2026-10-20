# HX-Browser AI 操作手册（Skill / 提示词）

> 给 AI 与自动化开发者看的手册：API 怎么用、Python 怎么对接、验证凭证（CF Turnstile / reCAPTCHA / 2FA）怎么处理、
> 以及**链路探索方法论**。内容持续沉淀，踩过的坑都写在这里。

## 0. 这是什么

HX-Browser 是 Linux 上 **Web 服务化浏览器环境管理器**（完全重构，非 local-browser 兼容版）：

- 每个环境 = 独立浏览器实例（独立 `user-data-dir` + 独立 CDP 端口 + 独立指纹 + 可选代理）
- 暴露 **REST API**（管理环境）+ **CDP 端点**（Python 爬虫框架直接接管浏览器）
- 支持 **账号包**：导出/导入登录态（Cookie + 指纹），跨机迁移
- 支持导入 **Firefox userdir**（ruyipage / 批量注册产物，登录态 100% 保留）

```
管理台 http://127.0.0.1:8325/ ｜ API 文档 http://127.0.0.1:8325/docs ｜ 本手册 = 记忆
```

## 1. API 速查（前缀 /api/v1）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/status` | 服务状态（浏览器路径、CDP 起始端口、运行数） |
| GET/PUT | `/settings` | 全局设置（含 `dark_mode` 暗色主题开关） |
| GET/POST | `/environments` | 环境列表 / 创建 |
| GET/PUT/DELETE | `/environments/{id}` | 详情 / 更新 / 删除 |
| POST | `/environments/batch` | 批量创建 `{count, base}` |
| POST | `/environments/{id}/duplicate` | 复制环境（不含登录态） |
| POST | `/environments/{id}/start` | **启动 → 返回 CDP 端点**（核心） |
| POST | `/environments/{id}/stop` | 停止（CDP Browser.close 优雅关） |
| GET | `/environments/{id}/status` | 运行状态（pid/port/login_state） |
| POST | `/environments/{id}/account-pack/export` | 导出账号包（按域名过滤 Cookie+指纹） |
| POST | `/account-packs/import` | 导入账号包 → 新环境 |
| POST | `/environments/import-profile` | 导入 Firefox userdir（`{profile_dir, mode}`） |
| GET/POST | `/environments/{id}/cookies` | 读 / 注入 Cookie |
| POST | `/environments/{id}/cache/clear` | 清缓存（cookies/localstorage/indexeddb/cache） |
| GET | `/environments/{id}/fingerprint/check` | 指纹一致性六维检测 |
| GET/POST | `/proxies` · POST `/proxies/check` | 代理库 CRUD / 检测（IP/国家/ASN/延迟） |
| GET/POST | `/extensions` · `/extensions/{id}/bind` | unpacked 扩展管理 |
| GET/POST | `/templates` · `/templates/{id}/apply` | 模板 |
| GET | `/logs` · `/groups` | 操作日志 / 分组 |

创建环境最小示例：

```bash
curl -X POST http://127.0.0.1:8325/api/v1/environments \
  -H 'Content-Type: application/json' \
  -d '{"name":"爬虫-01","platform":"google","location_mode":"match_ip",
       "fingerprint":{"browser":"sun","timezone":"America/New_York"}}'
```

启动（拿 CDP 端点）：

```bash
curl -X POST http://127.0.0.1:8325/api/v1/environments/env_xxx/start
# → data.runtime: {pid, debug_port, cdp_http: "http://127.0.0.1:43001", cdp_ws, login_state}
```

## 2. Python 爬虫框架对接（三件套，全实测通过）

### Playwright

```python
import asyncio
from playwright.async_api import async_playwright

async def main():
    async with async_playwright() as p:
        browser = await p.chromium.connect_over_cdp("http://127.0.0.1:43001")  # CDP 端口
        ctx = browser.contexts[0] if browser.contexts else await browser.new_context()
        page = ctx.pages[0] if ctx.pages else await ctx.new_page()
        await page.goto("https://example.com")
        print(await page.title())
        await browser.close()  # 注意：只断开连接，不杀浏览器
asyncio.run(main())
```

### DrissionPage（最省事）

```python
from DrissionPage import ChromiumPage
page = ChromiumPage(addr_or_opts="127.0.0.1:43001")
page.get("https://example.com")
print(page.title)
# page.quit() 会真的关闭浏览器实例！
```

### Selenium

```python
from selenium import webdriver
from selenium.webdriver.chrome.service import Service
opt = webdriver.ChromeOptions()
opt.debugger_address = "127.0.0.1:43001"
driver = webdriver.Chrome(service=Service("/usr/bin/chromedriver"), options=opt)
driver.get("https://example.com")
print(driver.title)
```

> ⚠️ **DrissionPage `quit()` / Selenium `quit()` 会关闭浏览器**（HX-Browser 状态会自动刷新为"已关闭"，属正常）。
> 多框架接力操作时，每步前先 `POST /environments/{id}/start` 确认存活。

## 3. 账号包（登录态迁移）

```bash
# 导出（只含目标平台域名 Cookie + 指纹，谷歌账号包实测 10~50KB）
curl -X POST http://127.0.0.1:8325/api/v1/environments/env_xxx/account-pack/export

# 导入（自动建环境 + 注入登录态）
curl -X POST http://127.0.0.1:8325/api/v1/account-packs/import \
  -H 'Content-Type: application/json' -d '{"pack": {...}}'
```

- 谷歌登录态 = SID/SSID/APISID/SAPISID/HSID/NID + `__Secure-*` 系 Cookie，导出即带
- 跨机/跨 IP 可能触发谷歌限流验证 → **建议同地区代理一起迁移**
- 批量注册 Firefox userdir 直接导入（登录态 100% 保留）：

```bash
curl -X POST http://127.0.0.1:8325/api/v1/environments/import-profile \
  -d '{"profile_dir": "/path/to/userdir_xxx", "mode": "copy"}'  # mode: copy|move|reference
```

## 4. 验证凭证处理（重点，沉淀自 freebuff 实战）

### 4.1 Cloudflare Turnstile（免打码！）

**freebuff 实证：Turnstile 不需要点击、不需要外置求解器**，关键只在一行环境自检：

```python
# ruyipage 场景：
page.add_preload_script("Object.defineProperty(Navigator.prototype, 'webdriver', {get: () => undefined})")
# 或 HX-Browser 场景：环境指纹扩展已默认覆盖 navigator.webdriver=false（指纹模块自带）
```

- token 自动生成在 `input[name=cf-turnstile-response]`（len≈688），iframe 显示 "Checking your Browser…" 转圈是 managed 模式自动评估，**无需点击**
- ⚠️ 注意：`navigator.webdriver=true` 时 token 恒为空 → 判定可疑
- ⚠️ **不要同时设置** `general.useragent.override` / `marionette.disable` / `dom.webdriver.enabled=false`（会破坏 Turnstile）——HX-Browser 指纹模块用启动参数 + MAIN world 扩展覆盖，不走这几条

### 4.2 reCAPTCHA v3（无感）

```js
const v3 = await window.grecaptcha.execute('SITEKEY', {action: 'signup'});
fetch('/api/auth/signup-challenge', {method:'POST', headers:{'content-type':'application/json'},
  body: JSON.stringify({token: turnstileToken, recaptchaV2Token: null, recaptchaV3Token: v3})});
```

- 双 token 组合提交（freebuff 实测：只提交 turnstile 会 200 ok 但 callback 报 `recaptcha_missing`）
- token **单次使用**：每次页面加载重新生成；403 是 CF 随机，**刷新页面重试即可**

### 4.3 2FA / TOTP（恢复码 = TOTP 种子）

**freebuff 重大发现：GitHub 账号"恢复码"实为 16 位 base32 TOTP 种子**（如 `QWJRRNXTMRBFH7SI`），用标准 TOTP 算法算实时码可登录。之前当恢复码输入全失败——它们根本不是恢复码。

```python
import hmac, hashlib, base64, struct, time

def totp(secret: str, window: int = 0) -> str:
    key = base64.b32decode(secret.upper())
    msg = struct.pack(">Q", int(time.time() // 30) + window)
    h = hmac.new(key, msg, hashlib.sha1).digest()
    o = h[-1] & 0x0F
    code = (struct.unpack(">I", h[o:o+4])[0] & 0x7FFFFFFF) % 1_000_000
    return f"{code:06d}"
```

- 2FA 密钥存环境 `account.two_factor` 字段；配套 `2faa.net`（2FA 生成）、`gmailver.com`（Gmail 免密判活）可做质量校验

### 4.4 OAuth 授权自动化（GitHub 类）

- 浏览器内 `form.submit()` 提交 authorize 表单 → GitHub 返回 `error=access_denied`（拒绝自动化）
- **正确做法**：浏览器导航到 authorize 页 → JS 提取表单完整字段（authenticity_token 等）→ `requests` POST 到授权端点（**注入浏览器导出的 Cookie**）→ 302 拿 code → 浏览器导航 callback
- GitHub 授权一次后**记住授权**，后续同账号直接 302 code
- 免费邮箱实盘（Outlook 批量注册产物）：`maybellloweadl@outlook.com` 这类来自 `workspaces/browser-auto/scripts/freebuff-register/`

### 4.5 DataDome（注册限流，尽力而为）

- GitHub signup 被 DataDome 硬封锁（机房 IP + 住宅 IP 均拦）→ **放弃新号注册，改用现成账号**（恢复码解锁 2FA）
- 经验：注册面被硬封锁时，优先转向 OAuth 存量账号路径

## 5. 工作方法论（效率核心，务必遵守）

1. **首次探索 = 手动 + 实时观察，不是写死代码**：
   - 用 HX-Browser 启动环境 → CDP 端点直接连（Playwright/DrissionPage REPL）
   - **一步一步手动执行**，每步打印响应/页面状态，看清楚了再走下一步
   - 不要一开始就写完整逻辑然后卡住等半天——卡住一次=重来，成本极高
2. **决策依据 = 现场观察**：打开 DevTools/抓响应，看 cookie 是否生成、token 是否就位、跳转去了哪，再决定下一步
3. **链路打通后再固化成脚本**：手动跑通 → 提炼成可重复流程 → 最后才写死参数化脚本
4. **失败即观察**：403/超时是信息，不是终点——刷新重试（CF 随机 403）、看响应体、换路径
5. **踩坑即沉淀**：每次解决一个坑，就补进本手册（4.x 节），下次 AI 直接查

## 6. freebuff CF 打码经验存档（2026-08-23 实测 4/4 成功）

> 完整报告见 `../workspaces/browser-auto/knowledge/freebuff.com/freebuff-register-report.md`，此处为可复用要点。

- **站点指纹**：freebuff.com = Next.js on Render + NextAuth v4；`/api/auth/providers` 返回 JSON 即 v4
- **防护**：CF Turnstile（invisible）+ reCAPTCHA v3 双保险
- **注册链路**：收集双 token → POST `/api/auth/signup-challenge` → 双 cookie 就位 → signin → OAuth → callback
- **关键点**：signup-challenge 成功标志 = `freebuff_signup_challenge=ok` + `freebuff_signup_recaptcha_v3=ok:0.5` 双 cookie
- **成功流程**（可重复）：
  1. 指纹浏览器（代理 + webdriver 环境自检）挂机
  2. 登录 GitHub 目标账号（TOTP 种子算码）
  3. goto /login，等 12s 让 Turnstile token 自动生成
  4. combo 提交 → 双 cookie → 403 则刷新重试
  5. signin 拿 OAuth URL → 浏览器导航 authorize → JS 提表单 → requests POST（带 GitHub cookie）→ 302 code
  6. 导航 callback → 自动登录成功 → 提取 `__Secure-next-auth.session-token`
- **注意**：不要调服务端 `/api/auth/signout`（会吊销已提取 token）；清浏览器 cookie 用 delete 命令保留服务器 session
- **成果**：4/4 账号 + session token 验证 HTTP 200（详见 `kotae/26-08-23/freebuff.com/`）

## 7. 常见坑速查

| 症状 | 原因 | 解法 |
|---|---|---|
| CDP 连不上 | 环境没启动 / 被框架 quit 关了 | `POST /environments/{id}/start` 重启 |
| Turnstile token 恒空 | `navigator.webdriver=true` | 指纹扩展已覆写；不要手动设 marionette 类 pref |
| callback 报 recaptcha_missing | 只提交了 turnstile | 双 token 组合提交 |
| 403 随机 | CF 限流/限流随机 | 刷新重试，别改代码 |
| 谷歌提示不安全浏览器 | UA/指纹不一致或 IP 变 | 同地区代理 + 完整指纹，或走账号包迁移 |
| 恢复码登录失败 | 它其实是 TOTP 种子 | 用 TOTP 算法算实时码 |
