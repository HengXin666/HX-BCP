# Agent Note: 启动后 DNS 泄漏强校验

Status: implemented

## Problem

用户需求 (2026-11-15, plan.md 红线一第 3 条):

> DNS 泄漏强校验. 任何挂了我代理的环境, 启动后跑一次 DNS 泄漏探针(dnsleaktest 类), 出口解析服务器必须落在出口 IP 同国家/同 ASN, 否则标红.

此前 doctor 的 `dns` 项只看 `prefs.dns_privacy` 有没有配, 属于 "传了参数", 不是实测. relay.py 的 socks5 远端解析也只证明 relay 自己不在本机解析, 证明不了浏览器本身 (预解析, DoH 配置, 代理没接上等) 不漏.

## Decision

- 新模块 `hx_browser/dnsleak.py`, 判据只在 `assess()` 一处.
- 适用范围 ("任何挂了我代理的环境"): `upstream_of(env.proxy) is not None`, 即 http / https / socks5 / hxpxy 自定义上游; 另含 system 模式且服务进程带代理环境变量 (`proxy_link.system_proxy_link`, 与沙箱 `egress_upstream` 同一判据), 此时探针 httpx 走 trust_env, 与浏览器同一条系统代理. direct 与无代理变量的 system 不探测 (出口就是本机, 没有 "漏" 可言). no_proxy 不探测: 非沙箱时 Chromium 会读环境变量, 沙箱时却走直连, 语义不定, 要挂代理请显式选 system 或自定义上游.
- 时机: `BrowserManager.start()` 里与 `_post_start` 并列起后台任务 `dnsleak.run_after_start`, 每次启动跑一次, 不阻塞启动响应. 先等 CDP 可达, 整体超时 60 s.
- 探针: bash.ws (dnsleaktest 同类, 公开 JSON 接口). 三步**全部在副本浏览器里**完成 (2026-11-15 第二版):
  1. 经 CDP `Target.createTarget` 开一个后台页, 直接打开 `https://bash.ws/id`, 等 `location.host == bash.ws` 且 `readyState == complete` (最长 20 s; 代理不通会停在错误页, 超时即 unknown).
  2. 页内读 body 得一次性 id, `fetch` 10 个 `https://<n>.<id>.bash.ws/` (no-cors), 让浏览器按它自己的网络路径解析.
  3. 页内同源 `fetch('/dnsleak/test/<id>?json')` 取结果 (该接口带 `Access-Control-Allow-Origin: *`, 同源更稳), 然后关页. 不在用户当前页执行, 不受页面 CSP 影响, 不打扰用户页面.
  - 为什么改: 第一版第 1, 3 步走 `netcheck.client_for(env)`, 它会**另起一条临时 relay**, 并不是浏览器正在用的那条连接. 轮换出口 / 按连接分配出口的代理 (住宅池, sticky session) 下, httpx 看到的出口与浏览器真实出口可能不同, 比对对象就错了, 会误红也会误绿. 违反 "传了参数 != 生效了": 出口必须是被测浏览器自己观测到的.
  - 页内返回值经 `rows_of()` (纯函数) 转结果行, 形状不对 (取不到 id, 结果非 200, 脚本异常) 一律 unknown.
- 判定: 每个 type=dns 的解析服务器, 与出口同 ASN 记 `match=asn`, 否则同国家记 `match=country`, 两者都不满足即泄漏. 任一泄漏 -> `status=error` (标红). "同国家/同 ASN" 按 "或" 理解: 公共解析 (8.8.8.8, 1.1.1.1) 的任播节点通常与出口同国但 ASN 不同, 按 "且" 会把正常的代理商解析全判红.
- 证据不足 (探针服务不可用, 浏览器内触发失败, 没观测到任何解析服务器, Gecko 内核) 一律 `unknown`, 绝不当通过 (沿用 audit "unknown 不放行").
- Gecko 内核: 探针靠 CDP Target / Runtime 在页面里发请求, Firefox 未接入, 直接 unknown 并写明原因.
- 结果存放: `RuntimeInfo.dns_leak` (与 doctor 检查项同形状, 另带 exit / resolvers / checked_at 证据), 随 runtime.json 落盘, 经 env_dict 的 runtime 暴露.
- 展示: doctor 体检多一项 `dns_leak` (读启动后结果, 体检时不重跑); 控制台环境列表状态格在 `status=error` 时出红色 "DNS 泄漏" badge, 悬停看泄漏的解析服务器; 探针跑完却是 unknown (服务不可用, 代理不通, Gecko) 时出黄色 "DNS 未验证" badge, 不让 "没证据" 看起来像 "通过"; "探测中" 与未挂代理不出 badge; "运行中" 与 "API 占用" 两种状态都挂这个 badge, Agent 通道起的环境同样标红.

## Consequences

- 每次启动挂代理的环境, 浏览器会向 bash.ws 发 12 个左右请求, 并短暂出现一个后台标签页 (约 10 到 30 s 内关闭).
- 依赖第三方探针服务; 服务不可用时结果为 unknown, 不会误报红, 也不会误报绿.
- browser.py 行数不变 (533, 棘轮未涨): 新增一行任务, 删去一行空行.
- 未做: 探测结果为红时自动停掉环境或拒绝启动. 需求只要求标红; 若要做成闸门, 在 run_after_start 里按 status 调 stop 即可.
- 未做: leak-gate 接入 CI (plan.md 验收判据), CI 上没有可用的上游代理.

## Verification

- tests/test_dnsleak.py: rows_of 页内返回值解析, 同 ASN 通过, 同国家通过, 外国外 ASN 标红, 缺证据 unknown, 适用范围 (含 system + 代理环境变量).
- 实测 (2026-11-15, 无头 Chromium + 本地 relay + 带凭据的 http 上游): 24 个解析服务器全部 match, status=healthy, 探针页已关闭.
- 第二版页内脚本 (2026-11-15): 用 node 模拟 document 对真实 bash.ws 跑通 id -> 10 次触发 -> 取结果, rows_of + assess 得 healthy (4 个解析服务器, 出口 AS16509). Hark 沙箱里的 Chromium 走带鉴权的系统代理拿到 407, 探针页停在加载中, 按设计报 unknown; 真实副本上的端到端复测待维护者本机跑一次.
