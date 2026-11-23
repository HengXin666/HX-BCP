# 统一出口代理并入 HX-BCP, 入口用用户名方言

日期: 2026-11-23 · 分支: feat/proxy-unify

## 决策

1. hx-pxy 以 `git subtree` 带历史并入 `packages/proxy`, 取自 GitHub 主线 91d2bfc。
   旧的内嵌副本 (gpt-json-to-token 下) 不动, 现有服务继续用它。
2. 删掉子仓自带的 `.github/` (嵌套目录里的工作流不会被 GitHub 执行), 由根 `ci.yml` 的 `proxy` 作业跑 `go vet` 与 `go test`。
   发版流水线暂未并入根 `release.yml`, 待定。
3. 删掉 `deep_find_php.sh` 与 `ref/` (只有说明文件, 参考仓本就不入库)。
4. 住宅平台适配用"网关 + 用户名模板", 模板加过滤器 (`{ttl|min}` `{country|lower}`) 吸收各家拼法差异。
   原因: BestProxy `life` 与 RapidProxy `stime` 都是分钟, 旧模板把 `session_ttl_seconds` 原样塞进去, 60 秒被当成 60 分钟。
   预设只是创建时的起点, 已保存的 provider 模板不受影响。
5. 入口对外也用同一种用户名方言, 接入方只需会配代理用户名。N 个上游 x M 个接入方降为 N + 1。
6. 槽位与节点分离: 槽位 ID 由客户端给且不变, 节点消失按 `rebind` / `hold` / `fail` 处理,
   取代 `listener.node_id` 钉节点。不复制节点, 也不冻结上游。

## 未做

- 入口服务本体: Mihomo `IN-USER` 只认静态用户, 带任意 session 的用户名需要控制面前置一层 HTTP/SOCKS5 入口做鉴权与槽位解析,
  再按槽位拨到编译好的节点出站。这一层只解析代理握手, 不碰 TLS 与上层协议。
