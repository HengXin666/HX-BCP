# Agent Note: 「接入 AI」选一个实时节点源, 文案带 share token 与 /rot/

Status: implemented

Archived: 2026-09-26

## Problem

上一轮的「交给 AI」文案只给文档地址。AI 读到的是接口**形状**, 不是这台机器**现在有哪些节点**。
用户每次接入还要自己把订阅 URL 再贴一遍; 节点一刷新, 那段话就过期。用户的原话是
「接入节点选择。这样可以直接实时同步远程节点。只需要区分是住宅还是普通代理」。

同一轮还撞上一个会让「实时」立刻失效的缺口: Vite 开发代理覆盖了 `/api` `/health` `/sub` `/rot`,
**没有** `/nodes`。于是 `GET /nodes/<token>` 在面板所在的 origin 上返回 SPA 的 HTML,
复制出去的「实时清单」其实是一段 `<!doctype html>`。生产安装不受影响 (Go 在 SPA 兜底之前
注册了 `ConsumerNodesPath`), 但本仓库默认的 `run.sh` / HX-Webx 开发形态会。

## Decision

**1. 入口从 About 页的三行固定文案, 变成独立的「接入 AI」页, 列出真实接入源。**
侧栏新增 `#/ai-entry`。页上只分两类:

- 普通代理: 启用、有 `share_path`、不是共享入口载体、也不是住宅渠道占用的 Listener;
- 住宅代理: 启用且 `endpoint.share_path` 非空的渠道。

住宅渠道占用的 Listener 不再出现在「普通代理」里, 避免同一份 token 列两次。
「配置控制面」仍是文档入口, 不带令牌, 放在两类下面。

**2. 选中的源把实时 URL 写进文案。** `GET <origin+子路径>/nodes/<share-token>` 对两类都成立
(`handleConsumerNodes` 先查住宅渠道, 再回落到 Listener)。住宅 sticky 渠道额外给
`POST …/rot/<token>/next`, 并写明「不要用 /ctl/」—— `/ctl/` 能烧供应商配额, 不是消费者凭据。
share token 从已有的 `/sub/<token>` 派生 (`nodesPathFromSharePath`), 对不上前缀就返回空串,
调用方不得编造。

**3. 对外根从 `window.location` 推导, 保留子路径。** HX-Webx 把面板挂在
`/proxy/hx-proxygroup/`。`publicBaseURL` 用 pathname (去掉尾斜杠) 拼 origin,
所以复制出的 URL 是 `http://host:36000/proxy/hx-proxygroup/nodes/<token>`,
适配层剥前缀后交给 Vite, Vite 再代理到后端。这与「只拿 `host` + `/nodes/`」不同:
后者会打到 HX-Webx 自己的 SPA 兜底, 拿到 HTML。

**4. Vite 把 `/nodes` 加进开发代理。** 与 `/sub` `/rot` 同类: token 寻址、不走管理员 Session。
不加这一条, 决策 2 在默认开发形态下是假的。

**5. 文案仍由 `web/src/lib/ai-entry.ts` 单点生成。** 门禁新增第 6 项: 派生函数的接受/拒绝两侧,
以及「普通代理文案含 GET /nodes、不含 /rot/; 住宅文案含 GET /nodes 与 POST /rot/…/next,
并警告 /ctl/」。

## Alternatives considered

- **什么都不做, 继续只给文档地址。** 文档锁的是形状, 不是内容。节点清单每天都在变,
  AI 按文档猜「有哪些节点」一定过期。用户已经明确否决了这一层。否决。
- **复用订阅 URL (`/sub/<token>`) 当作实时入口。** 订阅是渲染产物 (clash / v2rayn / sing-box
  各一套), 正是 `GET /nodes/` 契约要替代的东西。给 AI 一份 YAML 等于逼它再写解析器。否决。
- **把管理员 `/ctl/<token>` 一并写入住宅文案。** 换出口确实在 `/ctl/next`, 而且权限更完整。
  它输在权限分级: `/ctl/` 能消耗供应商配额、轮换声明会话、读临时鉴权; 只想「每个任务换一个
  出口 IP」的消费者不该握着它。`/rot/<token>/next` 已经覆盖这条诉求。否决。
- **URL 写成 `origin + /nodes/<token>`, 不管子路径。** 这是 `listenerShareURL` 今天的写法,
  在直连 Vite 时碰巧能用。挂在 HX-Webx 子路径下会打到面板自己的 SPA, 返回 HTML ——
  本轮对照实验已经看到 `36000/nodes/x -> 200 text/html`。否决。
- **继续放在 About 页。** 动态列表会把关于页撑长, 且与「版本 / 更新」不是同一类操作。
  用户选择独立子页。否决。

## Consequences

- 复制出去的文字**含 share token**, 等同于订阅 URL。只应发给操作者信任的 AI; 页面脚注写明这一点。
- 住宅渠道若不是 sticky、没有 `rotate_path`, 文案会如实说「当前没有 /rot/」, 而不是假装能换出口。
- 共享入口的载体行 (`shared_inbound_aggregate`) 不出现在列表里: 它没有自己的凭据。
- `vite.config.ts` 的 `/nodes` 代理是这条决策能在 `run.sh` 下成立的前提; 拿掉它, 实时 URL
  会再次变成 HTML。

## Testing

- `npx tsx@4 scripts/verify-ai-entry.ts` 第六项覆盖: 子路径保留、`/sub/→/nodes/` 派生、
  拒绝 `/rot/` 与 `/ctl/` 冒充 share path、普通/住宅文案各含该有的绝对 URL。
- Vite 代理: 加 `/nodes` 后, 开发 origin 上对未知 token 应返回后端的 JSON/plain 404,
  而不是 SPA HTML。
- 页面: 普通代理与住宅代理分组、住宅占用的 Listener 不重复出现、无源时给空态、
  「配置控制面」仍可复制且不含令牌。
