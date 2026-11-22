# 控制台视觉体系, 组件面与现代交互 (纯 UI 打磨)

日期: 2026-11-15 · 类别: feature · 范围: apps/console-web, 不改后端, 不加新端点, 不加新业务入口

## 视觉体系 (定稿, 唯一出处 `apps/console-web/src/styles.css`)

- 颜色: 只用 @hx/ui 语义令牌 (`--background`, `--card`, `--muted`, `--accent`, `--border`, `--border-muted`, `--primary`,
  `--link`, `--ring`, `--heading`, `--done`), 不写色值; 深浅两套主题由 `lib/theme.ts` 落到 `html[data-mode]`.
- 字号: 六档 `t-page 20/600/28`, `t-section 15/600/22`, `t-card 13/500/18`, `t-body 13/400/20`, `t-aux 12/16`,
  `t-micro 11/500/14 大写加宽`. 库内半像素与任意字号并到这六档.
- 间距: 4 的倍数. 行内 8, 卡片内边距 16, 卡片间距 12, 区块间距 24.
- 层次: 卡片零阴影, 1px 边框分层; 只有浮层 (批量条, 弹层) 带阴影.
- 动效: 150 到 200ms ease-out (`--ease-out-soft`), 只有换页, 批量条进场, 树展开三处关键帧; `prefers-reduced-motion` 全关.
- 焦点: 键盘聚焦一律 2px `--ring` 描边; 输入框贴边, 其余外移 2px.
- 外壳: 顶栏高 `--topbar-h` (3.5rem), 侧栏 240 / 收起 56; 表头, 分组栏, 抽屉按 `--topbar-h` 吸顶.
- 表格密度: 舒适 44 / 默认 40 / 紧凑 32 行高, 由外壳 `data-density` 驱动 (本次接通, 此前只有 CSS 规则没有开关).

## 组件面 (页面只用这些拼, 不再各写一套)

| 角色 | 组件 | 位置 |
|---|---|---|
| 页头 | `PageTitle` | `src/pages/PageTitle.tsx` |
| 区块 | `Block` | `src/pages/Block.tsx` |
| 数据表 | @hx/ui `DataTable` + `styles.css` 统一行高与表头 | 各页 |
| 指标卡 / 列表行 | `.kpi-card`, `.list-row` | `styles.css` |
| 工具条筛选 | `.status-chip`, `.filter-chip` | `styles.css` |
| 浮动批量条 | `BatchBar` + `.batch-bar-inner` | `src/pages/browsers/BatchBar.tsx` |
| 首屏骨架 | `PageSkeleton` (形状与真实布局一致) | `src/pages/browsers/PageSkeleton.tsx` |
| 详情抽屉 | @hx/ui `Sheet` | `src/pages/browsers/DetailSheet.tsx` |
| 确认 / 提示 | @hx/ui `confirmDialog`, `toast` | 全站 |
| 命令面板 / 快捷键 | `CommandPalette`, `ShortcutsDialog`, `lib/hotkeys.ts` | `src/shell/` |

## 本次落地的交互

1. 乐观更新 (`lib/cache.ts`: `patch`, `onPatch`, `optimistic`; `lib/hooks.ts` 的 `usePoll` 订阅 `onPatch`).
   浏览器页的改名, 移动, 改标签, 删除 (单个与批量) 发请求前先改列表缓存, 界面立刻变成预期结果;
   失败撤销并作废该键重拉, 回到后端真实状态; 成功由写接口自身的 invalidate 重拉校正.
   启停以后端进程为准, 不预判, 仍是逐行转圈加成败闪烁.
   已知边界: 乐观改写之前发出的轮询若晚于改写返回, 会短暂显示旧值, 下一次重拉即校正.
2. 表格密度开关: `lib/prefs.ts` 新增 `Density`, `DENSITY_KEY`, `DENSITY_LABEL`; `App.tsx` 把偏好落到外壳 `data-density`.
3. 命令面板扩容 (`shell/CommandPalette.tsx`): 新增 "界面" 组, 收起或展开侧栏, 快捷键速查, 切换表格密度, 刷新全部数据
   (`invalidate("")` 作废会话读缓存, 在挂页面立即重拉). 不新增任何业务操作.

## 不做的与理由

- 虚拟滚动: 浏览器表已分页, 每页行数受 `query.size` 限制, 渲染量有上限, 加虚拟滚动与吸顶表头冲突且无收益.
- 骨架屏: 已有, 不动.
- 新业务入口 (账号包, Cookie, 批量新建, 体检可视化等) 不在本次范围.

## 验收 (交 CI, 本地不跑)

- `pnpm typecheck` 绿.
- 改名一个环境: 点保存后列表立即显示新名; 断开后端再改名: 列表先变后回滚, 出现失败提示.
- 命令面板输入 "密度": 出现另外两档, 选中后所有表格行高随之变化, 刷新页面后保持.
- 命令面板 "刷新全部数据": 当前页立即重新请求.
- 产物: `pnpm build` 输出到 `packages/browser/src/hx_browser/web/` (既有约定, 8325 托管).

## 铺到各页 (同日第二轮)

- 页面根容器统一 `flex min-w-0 flex-col gap-4`, 不再自带 `p-3` (外壳 main 已有内边距, 此前总览, 实时预览, 最近删除,
  浏览器页是双层内边距). 页内区块之间仍用 `Block` 与 `gap-6`.
- 页标题与导航表 (`shell/nav.ts`) 同名: 总览 (原 "概览"), 实时预览 (原 "运行中"), 面包屑, 侧栏, 页头三处一致.
- 首屏骨架: 新增 `src/pages/PageLoading.tsx` (表格行 / 卡片格两种版式), 替换总览, 实时预览, 最近删除里的整块
  `Skeleton m-3 h-64`. 浏览器页仍用自己的 `PageSkeleton`.
- 乐观删除铺到资源三页: `ResParts.confirmRemove` 增加可选 `drop` (缓存键加行 id), 代理, 扩展, 模板删除确认后行立即消失,
  失败回滚. 资源页的新建与编辑仍以后端回包为准 (id, 检测结果由后端生成).
- 未动: 最近删除列表不走会话缓存 (`opsApi.trash` 未 keyed), 不做乐观; 系统页, 资源页外框已符合规范, 不改.

## 系统页与资源页收尾 (同日第三轮)

- 容器外框补 `min-w-0`: 系统页 (`SystemPage`), 资源页 (`ResourcesPage`) 的根容器此前是 `flex flex-col gap-4`,
  缺 `min-w-0`, 内部 NavTabs 下的宽表 (日志, 代理等) 在窄视口会把外框撑破; 补齐后与各叶子页一致.
- 各 NavTabs 子页根容器统一加 `min-w-0`: 状态 (`StatusTab`), 日志 (`LogsPage`), 孤儿进程 (`OrphansPage`),
  设置 (`SettingsPage`), 代理 (`ProxiesPage`), 扩展 (`ExtensionsPage`), 模板 (`TemplatesPage`).
  只动根容器的类名, 不动版式与逻辑.
- 设置页首屏: `if (!base || !d)` 的裸 `<EmptyState text="加载中" />` 换成共用的 `<PageLoading />`,
  与总览, 实时预览, 最近删除一致 (形状贴近真实表单, 不跳版); "未连接" 的 `EmptyState` 保留.
- 至此除浏览器页 (用自带的 `PageSkeleton` 与分组栏版式) 外, 所有页面的根容器间距, 页标题取名, 首屏骨架三者统一.
- 仍未动: 后端, 业务入口, 以及浏览器页自有版式; 均未在本次范围.

## 空态与焦点 (同日第四轮)

- 资源三页空态统一: `ResParts` 新增 `ResEmpty`. 有搜索词时显示 "没有匹配的 X" 和清除搜索; 库为空时显示一句用途说明和新建按钮.
  代理, 扩展, 模板都改用它 (此前扩展, 模板是 `emptyText` 一行字, 没有操作入口, 与代理不一致).
- 总览四张数字卡与 "最近打开" 行是可点按钮, 补 `focus-visible` 焦点环 (键盘 Tab 可见), 数字卡加轻 hover 阴影.
- 计划与记录统一写在本文件, 不写 `.hark/plan.md` (该文件另有用途, 不覆盖).

## 未验证的点 (交 CI 与人工)

- `pnpm typecheck`: 四轮改动都没本地跑过类型检查; `ResEmpty` 依赖 @hx/ui `DataTable` 的 `empty` 接收 ReactNode (代理页已这样用).
- `focus-visible:ring-ring` 依赖 styles.css 定义了 `--ring` 色; 未目测.
- 乐观删除 (浏览器页, 资源三页): 断后端时的回滚路径未实测.
- 窄视口 (< 768px) 下系统页日志表, 资源页宽表是否不再撑破外框: 未目测.
- `pnpm build` 产物 `packages/browser/src/hx_browser/web/` 未重新生成, 8325 托管的仍是旧界面, 需要构建后才可见.

## 日志页首屏 (同日第五轮)

- `LogsPage` 首次读取期间此前直接落到 "后端还没有记录任何操作" 空态 (数据未到即判空), 改为 `PageLoading rows={10}`,
  与其余页一致; 条数计数加 `tabular-nums`, 与资源工具条计数一致.
- 未验证: 同上一节, 另加本处首屏骨架未目测.

## 计数字体 (同日第六轮)

- 计数统一 `tabular-nums`, 数字跳动时不抖: 孤儿进程页头部计数, 实时预览页脚 "最早启动于". (资源工具条, 日志计数前几轮已加.)
- 至此计数显示全站一致. 前端已无可做的中性收尾, 后续是 typecheck / build / 目测.
