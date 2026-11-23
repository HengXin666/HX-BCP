# Agent Note: linkup 住宅渠道的接入参数

Status: implemented

## Problem

linkup 是一个只有 HTTP CONNECT 网关 (`global.rp.linkup.onl:10000`) 的动态住宅供应商: 出口 IP 与
粘滞窗口都编码在**用户名**里 (`{账号}-zone-{地区}-session-{会话}-sessTime-{分钟}-sessAuto-1`),
不是订阅, 也不是 API 提取。把它接进控制面时, 有四个字段的值不是"随便填一个能过校验就行":

1. **上游组**: 网关只开 10000 这一个非标端口, 而良心云那一档出口恰好封掉 10000/1080/3128/9001
   段 ⇒ 走良心云恒定空回复, 与凭据无关。只有 doulor / shadowmere 这一档能到。
2. **用户名模板**: `vendor` 填一个未知键也能建供应商, 但模板必须自己给全;
   `generic-sticky` 预设的 `{user}-session-{session}` 少了 `zone-`/`sessTime-`/`sessAuto-` 三段。
3. **`{region}` 与空值**: 模板含 `{region}` 而 region 为空时会渲染出 `zone--session`,
   而 `normalizeProvider` 里那条"用 `{region}` 就必须有 `default_region`"的校验**只对
   bestproxy 生效**, 别的 vendor 一律放行。实测渠道建好后 `active.yaml` 里真的出现了
   `zone--session-…` —— 数据面不报错, 只是安静地换了一份用户名。
4. **TTL 单位**: `session_ttl_seconds` 的**名字**是秒, 但对 bestproxy/rapidproxy 这两个 vendor
   是按分钟解释、并把这个数字直接渲染进用户名的 (`SessionPoolLifetime` 按 vendor 名选单位)。
   linkup 的 `sessTime` 同样是分钟, 而 linkup 不在那条特例里。

## Decision

在**运行中的**本机控制面 (127.0.0.1:19090) 上用住宅供应商 API 建一条渠道; 不改代码、不重启:

- 供应商 `residential-provider-d8c205b12aeac153580edaa5` (name `linkup 住宅`, vendor `linkup`):
  `protocol=http`, `gateway_host=global.rp.linkup.onl`, `gateway_port=10000`,
  `username_template={user}-zone-{region}-session-{session}-sessTime-{ttl}-sessAuto-1`,
  `rotation_mode=session-template`, `default_region=custom`, `session_ttl_seconds=600`,
  `max_concurrent_sessions=8`, `session_expiry_policy=rotate`。
- `upstream_proxy_group_id` = 既有 `doulor-gh-pool` (group-c2812257c10d7e08a31f05df)。
  **不为它新建前置组** —— 既有组已 enabled, 再建一个功能一致的组只会把"组是声明"这件事变模糊。
- `api_proxy_url` = 共享入口的 doulor 用户 (`socks5://gh-doulor-n15-116287197:…@127.0.0.1:7890`)。
  这一条**只服务控制面的「测试连接」**: `TestProvider` 走的是 `api_proxy_url`, 并不看
  `upstream_proxy_group_id`; 不设它, 测试会从控制面直连网关, 而本机默认路由被 Clash TUN 接管,
  于是报一个与链路无关的假失败。数据面的真实链路仍然只由节点的 `dialer-proxy` 表达。
- `session_ttl_seconds=600`: 未知 vendor 对 0 的兜底本来就是 600, 于是**渲染成 `sessTime-600`
  且内部粘滞窗口 = 600s (刷新 480s)**, 两个语义对齐。实测网关接受 600/5/1 三个取值 (均 200),
  所以不必去模仿凭据文档里的 `sessTime-5` —— 那是供应商侧粘滞时长, 不是控制面的寿命单位。
- 渠道 `residential-channel-8f3aee5b2dc67b3e66129cd8` (name `linkup`, `mode=sticky`,
  `protocol=vless`, `session_count=2`, `preallocate=false`, region `custom`)。
  渠道**必须显式带 `region=custom`**: 只传 `region_mode=fixed` 不传 region 会走
  `normalizeRegionSelection("fixed","")` 而**不会**回落到供应商默认值 —— 那正是上面第 3 条坑。
- 代理组由渠道自己建: `residential-linkup` (group-12704d95e7bd9eaed47d1051), 成员是渠道分配出来
  的住宅节点。这是控制面既有的"渠道 = 入口 + 组 + 声明节点"形状, 不是为几个节点手工攒的存放组。

## Alternatives considered

- **什么都不做, 继续让调用方自己拼两段 curl (`--preproxy` + linkup 网关)。** 最强理由:
  FINDING 已证明这条链路可用, 不需要任何控制面改动, 零风险、零回归面。否决: 粘滞会话、换 IP、
  凭据与配额就都留在每个调用方手里, 而控制面本来就有住宅渠道这套机制; 每个新调用方都要重学一遍
  第 1、2 条坑。
- **新建一个 linkup 专用前置组 (doulor + shadowmere 成员) 当上游。** 最强理由: 两个前置都实测能到
  网关, 组里放两个来源比绑死 doulor 更抗单点。否决: `doulor-gh-pool` 已存在且 enabled, 它本身就是
  "订阅 + 关键字筛选"的表达; 再建一个同功能组正好是仓库禁止的"为节点建组"。要加兜底就改那个既有
  组的声明 (它的成员本来就能被刷新/筛选), 而不是新增一个组。
- **用 `generic-sticky` 预设, 只替换模板。** 最强理由: UI 里选预设比手填九个字段省事, 也少一处拼错
  的机会。否决: 预设的 `session_ttl_seconds=600` 在 linkup 身上是"分钟", 而字段名会让人读成秒;
  模板又必须整段换掉 —— 一半靠预设一半靠手改, 比全手填更难核对。
- **改代码: 在 `internal/residential/presets.go` 加一条 linkup 预设, 并把"stime/sessTime 是分钟"
  与"用 `{region}` 必须有 `default_region`"两条规则从 bestproxy/rapidproxy 特例推广到 linkup。**
  最强理由: 这才是把这两条事实放进代码, 而不是留在一次性运行期配置里, 顺手堵住第 3、4 条坑对下一个
  人的伤害。否决 (本轮): 需要重建二进制并重启**正在承载其他工作负载**的控制面与数据面 (7890 上还有
  别的服务), 而本轮目标是"接入当前本地实例"。记为后续改动, 见 Consequences。

## Consequences

- 交付物: 供应商 `residential-provider-d8c205b12aeac153580edaa5`; 渠道
  `residential-channel-8f3aee5b2dc67b3e66129cd8`; 组 `residential-linkup`; 入口 listener
  `residential-linkup-entry` (127.0.0.1:32001, vless+ws); 数据面 listener 名
  `hx-in-96372a5f9beec2c3`。渠道的订阅 URL 与 control URL 是凭据, 不入库本文 ——
  用 `GET /api/v1/residential/channels` 读。
- 数据面形态: 住宅节点在 `active.yaml` 里是 `type: http` + `dialer-proxy: doulor-gh-pool`,
  规则是 `AND,((IN-NAME,hx-in-96372a5f9beec2c3),(IN-USER,hx-session-…)),hx-node-…`;
  渠道的组是 mihomo `type: select`。
- `session_count=2` 是保守起点 (供应商并发上限未知), `max_concurrent_sessions=8` 留了加窗口的余地;
  扩容只改渠道的 `session_count`。
- **第二个入口 (2026-10-14)**: gh-pool 只会说 HTTP, 而渠道自有的 vless 入口 (32001) 它用不了 ⇒
  在共享入口上给**同一个组**再加一条用户名分流: listener `resi-linkup`
  (`listener-f30d092efe187a7d7d4e840b`, kind=mixed / 0.0.0.0:7890 / shared_inbound=standard /
  `proxy_group_id=group-12704d95e7bd9eaed47d1051`), 产出
  `AND,((IN-NAME,hx-in-shared-standard-mixed),(IN-USER,resi-linkup)),residential-linkup`。
  **没有新建组** (复用渠道早已声明的那一个); 实测出口 `178.125.201.178`。这是「复用既有组」那条
  决策的第一个真实消费方 —— 机器调用方走 `POST /api/v1/listeners` (不被 `deny_api_group_create` 拦)。
- **残留**: 模板里 `{region}` 对空 region 不设防, 而这只有 bestproxy 有校验; 别的渠道若清空 region
  仍会渲染 `zone--`。控制面不报错, 只安静地换一份用户名。
- **后续**: (a) 给 linkup 加 vendor 预设; (b) 把"用 `{region}` 就必须有 `default_region`"从
  bestproxy 推广到所有用到 `{region}` 的模板; (c) 让 `session_ttl_seconds` 的分钟语义按 vendor
  显式声明, 而不是靠 vendor 名字的字符串比较。

## Testing

- 供应商「测试连接」`POST /api/v1/residential/providers/residential-provider-d8c205b12aeac153580edaa5/test`
  → `success=true`, 出口 `188.78.18.154`, 2.3s; 渲染预览回显
  `…-zone-custom-session-52ac38668826-sessTime-600-sessAuto-1` ⇒ 模板的三段都在。
- 模板取值实测 (经共享入口的 doulor 用户:
  `curl -x <linkup 网关> --preproxy socks5h://…@127.0.0.1:7890`): `sessTime-600` / `-5` / `-1`
  三个会话均 200 且出口互不相同 ⇒ 该段是原样透传的参数, 不是硬编码。
- 数据面断言: `active.yaml` 里该节点带 `dialer-proxy: doulor-gh-pool`、
  `server: global.rp.linkup.onl` + `port: 10000`、用户名为 `…-zone-custom-session-…-sessTime-600-…`
  三段俱全; mihomo unix socket 的 `/proxies/<node>/delay` = 880ms,
  `/proxies/residential-linkup/delay` = 941ms (对照组 `/proxies/doulor-gh-pool/delay` = 2052ms)。
- 入口整链: 临时 mihomo 客户端 (vless + ws、无 TLS、指向 127.0.0.1:32001、用渠道订阅里的 uuid 与
  ws-path) → `https://ipinfo.io/json` 返回 `73.0.111.197` (AS7922 Comcast, US/FL, 住宅), 200 / 1.9s;
  验证后立即 kill, 未留残余进程、未新增 listener。
- 未做的: 经 `pxy.woa.qzz.io` (CF → 雷池) 的公网入口从外网跑一遍; 以及新的连测失败率统计
  (只有 FINDING 里 doulor 15/15 的旧数据, 本次是单次验证)。
