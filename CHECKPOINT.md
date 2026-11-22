# CHECKPOINT (2026-11-22 下午)

本轮 (未 commit, 接在上午那组之上):
- 本机 WebKit 内核 `webkit` (webkit_local.py + external.py): Playwright 的 Linux WebKit 构建, 一个环境一个 runner 进程, 代理走 relay, 无 CDP. 笔记 `.agents/notes/implemented/feature/2026-11-22-webkit-local-kernel.md`.
- 逐环境检测 site_checks.py: 从控制台对在跑的 Chromium 系环境跑 gate/probe.py + score.ts 与 fingerprint-audit, 结果在环境详情 指纹页. 笔记 `.agents/notes/implemented/feature/2026-11-22-console-site-checks.md`.
- claude-env 评分未接入产品 (见笔记).
- 沙箱验证: tsc 0 错, vite build 过; pytest browser 非 e2e 223 过 (test_smoke 要 8325 在跑, hxcp 用例要 hxcp 可执行文件, 均为环境原因).
- browser.py 543 行, 仍超 500, 待拆.
- hxoa git 只读, 提交要在本机做.

---

# CHECKPOINT (2026-11-22)

本轮 (未 commit, 在 11-15 两组改动之上):
- 通道收敛: 带 x-api-key 即 Agent, 声明 human 403; 回环算人类改为 settings.human_loopback 默认关; Agent 永远要密钥 (启动自动生成); 解锁链接 bcp-lane unlock-url; hxb serve 默认 127.0.0.1. 笔记 `.agents/notes/implemented/feature/2026-11-22-lane-convergence.md`.
- Safarier 接入为 webkit 族外部引擎 (safarier.py, 可本机或 ssh 远程命令), 内核管理接口 api_kernels.py. 笔记 `.agents/notes/implemented/feature/2026-11-22-safarier-webkit-kernel.md`.
- 控制台新增 系统/内核, 系统/外部接入 两页; HumanGate 支持 #/unlock?code=.
- 已在沙箱镜像跑: pnpm 同款 tsc --noEmit 0 错, vite build 通过; pytest lane 全部 + browser 非 e2e 181 过 (hxcp_link/race 5 条因沙箱无 hxcp 可执行文件失败, 与本轮无关).
- 未验证: 真机 macOS + Safarier 联调; 两个新页面未目测.

---

# CHECKPOINT (2026-11-15, 上一轮)

接手的人请先读本文件, 再读两份决策笔记:
- 功能: `.agents/notes/implemented/feature/2026-11-15-console-visual-system-and-interaction.md` (控制台视觉与交互, 分六轮)
- 修复: `.agents/notes/implemented/bug-fix/2026-11-15-console-api-key-401.md` (api_key 401)

访问方式: 工作区经 hxoa 挂到本机 `/home/hx/Loli/code/AI-Code/HX-BCP`. hxoa 只能 ls/cat/put, 不能跑命令 (typecheck/build 要在本机跑).

## 已完成 (全部未 commit, 是否提交由你定)

### 1. api_key 401 修复
- 新增 `packages/browser/src/hx_browser/api_key_gate.py` (admitted(): 守 /api/, /mcp, /docs, /redoc, /openapi.json; /api/v1/lane/session 放行; 人工车道经 loopback/session/token 豁免 key; 常量时间比较).
- `packages/browser/src/hx_browser/api.py` 第 62 行中间件调用 api_key_gate.admitted.
- `apps/console-web/README.md` 已知问题 #7 更新.
- loopback 豁免前提: 8325 前面没有反向代理 (尚未与用户确认).

### 2. 控制台前端视觉与交互统一 (六轮)
改的都是 `apps/console-web/src/` 下的文件:
- lib/cache.ts (patch/onPatch/optimistic), lib/hooks.ts (usePoll 订阅 onPatch), lib/prefs.ts (Density), App.tsx (data-density), shell/CommandPalette.tsx ("界面" 组).
- browsers/useEnvActions.ts (改名/移动/标签/删除乐观).
- pages/PageLoading.tsx (新增, 表格/卡片两种首屏骨架).
- pages 根容器统一 `flex min-w-0 flex-col`, 去双层内边距; 页标题与 shell/nav.ts 同名 (总览, 实时预览).
- 系统页/资源页外框 + 7 个 NavTabs 子页补 min-w-0; SettingsPage 与 LogsPage 首屏改 PageLoading.
- 资源三页空态统一 ResParts.ResEmpty; 删除走乐观 (ResParts.confirmRemove drop).
- OverviewPage 可点卡片加 focus-visible 焦点环; 计数统一 tabular-nums (资源/日志/孤儿/实时).

## 正在做
- 无. 第六轮后前端已无中性收尾可做. 浏览器页刻意保留自有版式 (PageSkeleton + 分组栏), 不在统一范围.

## 下一步 (接手第一件事)
1. 本机跑 `pnpm typecheck` (CI 同款), 把报错发回修.
2. 跑 `pnpm build`, 产物到 `packages/browser/src/hx_browser/web/`; 8325 托管的界面构建后才更新.
3. 都绿后决定是否 commit 以上两组改动.

## 已知坑 / 未验证
- typecheck/build 六轮都没本地跑过.
- 乐观删除断后端时的回滚路径未实测; 轮询晚于乐观改写返回会短暂显示旧值, 下次重拉即正.
- focus-visible:ring-ring 依赖 styles.css 的 --ring, 未目测.
- 窄屏 (<768px) 宽表是否不再撑破外框, 未目测.
- api_key loopback 豁免的反代前提未与用户确认.

## 红线 (勿碰)
- `.hark/plan.md` 是用户的红线方案 v3, 不覆盖.
- 不做检测规避核心 (防泄露/指纹自洽/TLS-H2 伪装). 逐环境体检 UI 已按用户 2026-11-22 明确要求做了 (只跑既有工具, 不新增规避逻辑); claude-env 评分不接入产品.
