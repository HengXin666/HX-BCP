# 统一出口代理并入 HX-BCP, 入口用用户名方言

日期: 2026-11-23 · 分支: feat/proxy-unify

## 决策

1. hx-pxy 以 `git subtree` 带历史并入 `packages/proxy`, 取自 GitHub 主线 91d2bfc。
   旧的内嵌副本 (gpt-json-to-token 下) 不动, 现有服务继续用它。
2. 嵌套 `.github/` 不会被 GitHub 执行, 删掉; 根 `ci.yml` 的 `proxy` 作业下载固定版本 mihomo 后跑 `go vet` 与 `go test`,
   发版移到根 `proxy-release.yml`, tag 线 `proxy-v*`, 发布包名不变。
3. 删掉 `deep_find_php.sh` 与 `ref/` (只有说明文件)。
4. 住宅平台适配用"网关 + 用户名模板", 模板加过滤器吸收各家拼法差异。
   各家 TTL 单位不同 (秒 / 分钟), 过滤器 `{ttl|min}` 等让新平台一条模板就能写对。
5. 入口对外也用同一种用户名方言, 接入方只需会配代理用户名。N 个上游 x M 个接入方降为 N + 1。
6. 槽位与节点分离, 取代 `listener.node_id` 钉节点; 不复制节点, 也不冻结上游。
   rebind 优先同 server:port:协议的继任节点, 吸收上游改名与改参数。
7. 入口是控制面里的一个 HTTP/SOCKS5 前置, 不实现任何节点协议: Mihomo `IN-USER` 只认静态用户,
   带任意 session 的用户名无法编译进数据面。前置解析用户名, 选好节点后经环回 hub 拨入,
   hub 上每个节点和组一个内部用户, 所以节点协议仍全部由 Mihomo 处理。
8. 住宅平台由入口直接拨网关 (session 由槽位绑定派生), 不经住宅渠道, 因为渠道的声明节点数是固定的,
   而入口的槽位由客户端随意命名。渠道与 `/ctl` 保留给需要服务端互斥的自动化。
9. subresi 判定: 源声明为 residential 直接算; 否则经节点请求 ip-api.com, 非 hosting 且非 proxy 算住宅。
   判定带采样时间, 72 小时过期, 过期不参与选路。`sub` 是全部订阅节点的超集。
10. 租户密码用主密钥加密落库 (不是哈希), 因为 Clash 订阅要把密码写进配置; Clash 链接 token 由密码派生, 轮换即失效。
11. 默认池只有 sub, subresi: resi 花钱, direct 暴露本机网络, 代理组可能是别人的住宅渠道组, 都需显式授权
    (`group:<组名>` 或 `group`)。direct 只放行公网单播地址。
12. 入口默认关闭 (`HX_PXY_ENTRY_LISTEN` 为空), hub 默认 27890, 出口探测默认关闭; 升级后零行为变化。
13. hub 路由收进 `sub-rules`, 顶层只加一条 SUB-RULE; 带 hub 的配置应用失败时去掉 hub 重试, 入口不能拖垮现有 listener。
14. 明文 HTTP 代理一连接一请求, 防止后续请求带着 Proxy-Authorization 发给目标。
15. 住宅配额边传边计, 超额断开; 住宅槽位的绑定寿命不超过供应商 session 寿命。
16. BestProxy / RapidProxy 预设保持原样 (`{ttl}`), 过滤器只给新模板用; 改预设会改变现有渠道的粘性时长。
17. 节点名推断国家改为按词匹配并优先旗帜, 避免 Australia / Russia 被判成 US。

## 本地验证范围

按 AGENTS.md 本地只做 go build / go vet / tsc, 测试交给 CI。根包里 `TestRunScript*` 等用例在 GitHub 上游基线就失败
(run.sh 缺 VITE_BACKEND_TARGET / --strictPort, 发版用例读旧路径), 已改为读根目录 `proxy-release.yml` 与 `scripts/lib/frontend.sh`。

## 验证

- `internal/entry/live_test.go`: 编译带 hub 的配置, 起真 mihomo, 入口经 hub -> 节点 -> 目标打通; 未知 hub 用户被拒。
- `internal/entry/server_test.go` 与 `catalog_test.go`: SOCKS5 / HTTP CONNECT 入口, 粘性槽位, subresi 选路,
  代理组, 鉴权失败, 用量落库, Clash 名字稳定, 住宅网关 session 粘性与释放后换 session。
