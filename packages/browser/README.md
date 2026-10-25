# browser

指纹浏览器: 环境创建 / 启动 / CDP 接入 / 指纹派生, 支持任意 http 或 socks5 代理。

Linux 版 Web 服务化浏览器环境管理器(参考 local-browser 二次开发)。

- **Web 服务**: FastAPI, 管理环境生命周期, 暴露 REST API + 轻量管理台
- **CDP 优先**: 每个环境一个独立调试端口, Playwright / DrissionPage / Selenium 直接接入
- **账号隔离**: 独立 `user-data-dir` + 独立端口 + 独立指纹 + 独立代理
- **账号包**: 按域名导出登录态(Cookie + 指纹)<100KB, 任意机器导入即恢复登录态
- **批量注册对接**: 直接导入 ruyipage / Firefox userdir(如 `data/freebuff-register/userdir_*`), 登录态 100% 保留
- **沙箱隔离** (`prefs.isolation=sandbox`, 需 bwrap): 独立 PID/IPC/UTS/**网络**命名空间 (出网只经本副本代理 relay, 访问不到宿主回环服务, WebRTC/UDP 无路可走)、只给本副本专属的显示 server (Xvfb / Xephyr 嵌套 / Wayland socket)、系统字体白名单、seccomp 拒绝表; 宿主内核仍共享, 详见 `.agents/notes/implemented/feature/2026-10-16-hxbrowser-strict-isolation.md`
- **窗口系统**: 默认 X11 (含 XWayland); `prefs.window_system=wayland` 走原生 Wayland (此时窗口无法被外部定位)
- **字体隔离** (`prefs.font_isolation=baseline|custom`, 所有内核): 网页只能枚举白名单字体, 中文字体总是保留

## 快速开始

```bash
cd packages/browser
uv sync                          # 安装依赖(含 dev 组: playwright/selenium/drissionpage)
uv run hxb serve                 # 默认 0.0.0.0:8325 (端口取 settings.api_port)
# 浏览器路径自动探测, 也可在管理台「设置」手动指定
```

打开 http://127.0.0.1:8325/ 即管理台; API 文档 http://127.0.0.1:8325/docs 。

装成库 (`uv add hx-browser`) 后入口同样是 `hxb serve`; 数据目录用环境变量覆盖,
优先级高于 `settings.json` 的 `data_dir`:

```bash
export HX_BROWSER_DATA_DIR=/srv/hx-browser-data
hxb serve --host 127.0.0.1 --port 8325
```

## 管理台前端

复用 local-browser 的 React 界面(环境管理/分组/代理/扩展/模板/指纹检测/设置), 通过
`frontend/public/localenv-shim.js` 把前端的 `window.localEnv`(原 Electron IPC 桥)映射到
HX-Browser 的 REST API——**后端完全自研, UI 沿用原版风格**。重建前端:

```bash
cd frontend && npm install && npx vite build && cd ..
rm -rf src/hx_browser/web/* && cp -r frontend/dist/* src/hx_browser/web/
```

界面截图见 [docs/screenshots/](docs/screenshots/)。

## Python 爬虫框架接入(核心)

### Playwright

```python
import asyncio
from playwright.async_api import async_playwright

async def main():
    async with async_playwright() as p:
        browser = await p.chromium.connect_over_cdp("http://127.0.0.1:43000")  # 环境 CDP 端口
        page = browser.contexts[0].pages[0]  # 或 browser.new_page()
        await page.goto("https://example.com")
        print(await page.title())
        await browser.close()

asyncio.run(main())
```

### DrissionPage

```python
from DrissionPage import ChromiumPage
page = ChromiumPage(addr_or_opts="127.0.0.1:43000")
page.get("https://example.com")
print(page.title)
```

### Selenium

```python
from selenium import webdriver
options = webdriver.ChromeOptions()
options.debugger_address = "127.0.0.1:43000"
driver = webdriver.Chrome(options=options)
driver.get("https://example.com")
print(driver.title)
```

## 账号包(谷歌账号迁移)

```bash
# 1. 启动已登录谷歌的环境, 导出账号包(只含 google 系域名 Cookie + 指纹)
curl -X POST http://127.0.0.1:8325/api/v1/environments/env_xxx/account-pack/export

# 2. 目标机导入, 自动建环境 + 注入登录态
curl -X POST http://127.0.0.1:8325/api/v1/account-packs/import \
  -H 'Content-Type: application/json' \
  -d '{"pack": {<导出的包>}, "name": "迁移-谷歌"}'
```

体量实测远小于 1MB(谷歌账号包通常 10~50KB)。

## 批量注册 userdir 导入

```bash
curl -X POST http://127.0.0.1:8325/api/v1/environments/import-profile \
  -H 'Content-Type: application/json' \
  -d '{"profile_dir": "/path/to/data/freebuff-register/userdir_20260823_050048_xepiq6", "mode": "copy"}'
```

导入后为 Firefox 环境, 可直接启动并自动化; `mode=reference` 可软链引用不占空间。

## API 速查

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/status` | 服务状态 |
| GET/POST | `/api/v1/environments` | 环境列表 / 创建 |
| PUT/DELETE | `/api/v1/environments/{id}` | 更新 / 删除 |
| POST | `/api/v1/environments/{id}/start` | 启动 → 返回 CDP 端点 |
| POST | `/api/v1/environments/{id}/stop` | 停止 |
| POST | `/api/v1/environments/{id}/account-pack/export` | 导出账号包 |
| POST | `/api/v1/account-packs/import` | 导入账号包 |
| POST | `/api/v1/environments/import-profile` | 导入 Firefox userdir |
| GET/POST | `/api/v1/environments/{id}/cookies` | 读 / 注入 Cookie |
| POST | `/api/v1/environments/{id}/cache/clear` | 清缓存 |
| GET | `/api/v1/environments/{id}/fingerprint/check` | 指纹一致性检测 |
| GET/POST | `/api/v1/proxies` · `/proxies/check` | 代理库 / 检测 |
| GET/POST | `/api/v1/templates` | 模板 |
| GET | `/api/v1/logs` | 操作日志 |
| GET | `/api/v1/live/envs` | 在跑副本的实时预览视图 (内核族 / 隔离档 / 窗口系统 / 是否支持) |
| WS | `/api/v1/environments/{id}/live?preset=grid\|focus[&key=API_KEY]` | 实时预览 + 远程操作 (CDP screencast; Firefox 系显式不支持)。配置了 api_key 时浏览器 WS 设不了头, 用 `&key=` 传 (脚本客户端也可用 `x-api-key` 头), 缺失以 4401 关闭; 带 `Origin` 的握手只接受回环来源、同 host 或 `HXBR_LIVE_ORIGINS` (逗号分隔) 列出的来源。前端组件见 HX-UI `BrowserLiveWall` |

## AI 操作手册(Skill 页面)

管理台左侧「Skill 指南」页面 = 给 AI 与自动化开发者的完整操作手册(内容维护在 `docs/AI-GUIDE.md`):

- **API 速查**: 全部端点 + curl 示例 + CDP 端点获取
- **Python 三件套对接**: Playwright / DrissionPage / Selenium 实测代码
- **验证凭证处理**: CF Turnstile 免打码(webdriver 覆写)、reCAPTCHA v3 双 token、2FA/TOTP 种子算码、OAuth 授权自动化、DataDome 应对——沉淀自 freebuff 实战
- **工作方法论**: 首次链路探索必须手动 + 实时观察再写码, 失败即观察, 踩坑即沉淀
- **常见坑速查表**

AI 每次接新任务前先读这份手册(或 `GET /api/v1/skill`), 避免重复踩坑。

完整规格见 [PRD.md](PRD.md)。

## 数据目录

```
data/
├── environments.json   # 环境配置(含账号字段)
├── proxies.json        # 代理库
├── templates.json      # 模板
├── logs.json           # 操作日志
└── profiles/<env_id>/  # 浏览器用户目录(隔离的登录态)
```

## 测试

```bash
uv run hxb serve &              # 先起服务
uv run pytest tests/ -v         # 冒烟测试
```

## 路线

- v0.1(本版): 环境隔离 / 指纹(MV3)/ 代理 / Cookie 注入 / 账号包 / profile 导入 / CDP 接入
- v0.2: TLS 指纹(uTLS 代理)、Firefox 扩展注入、IndexedDB 导出、账号包加密 UI
