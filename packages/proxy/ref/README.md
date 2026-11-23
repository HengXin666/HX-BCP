# Reference Projects

参考项目默认放在本目录，但不作为 HX-ProxyGroup 源码的一部分提交。

## BPB-Worker-Panel

- 仓库：`https://github.com/bia-pain-bache/BPB-Worker-Panel`
- 目标目录：`ref/BPB-Worker-Panel`（已加入 `.gitignore`，需手动克隆：`git clone --depth 1 https://github.com/bia-pain-bache/BPB-Worker-Panel ref/BPB-Worker-Panel`）
- 许可证：GPL-3.0（研究参考仅阅读，不复制其源码；集成模型为本项目自有架构）。

### 值得参考的能力

BPB-Worker-Panel 是一个部署在 Cloudflare Workers / Pages 上的 VLESS/Trojan WebSocket 代理面板，主要包含：

- `/<securePath>/sub/raw?app=xray` 返回 Base64 编码的 VLESS/Trojan 分享 URI 列表。
- 每次请求通过 DoH 实时解析 Cloudflare 地址（`getConfigAddresses`），因此每次刷新得到不同出口地址。
- 分享 URI 携带 `host`、`type=ws`、`security=tls|none`、`path`（含 `?ed=2560`）、`sni`、`fp`、`alpn` 参数。
- WebSocket 入口按路径首段路由到 `vl` / `tr` 处理器。

### HX-ProxyGroup 复用与差异

- 仅复用其「面板链接 / `sub/raw` 订阅链接的解析约定」：住宅供应商预设 `bpb-panel`（`cf-worker` 轮换模式）把链接填入 `worker_url`，控制面经可选出口代理拉取并用现有订阅解析器解析 VLESS/Trojan 分享 URI。
- 住宅集成模型（Provider/Channel/Session、声明节点、control token、`dialer-proxy`、AEAD 加密信封、TTL 强制 0、用户主动 `next` 才轮换）为本项目自身架构，未复制其源码。
- 差异记录：`docs/RESIDENTIAL_V2_PROGRESS.md` 2.5 节。

## easy-proxies

- 仓库：`https://github.com/daimon3332/easy-proxies`
- 目标目录：`ref/easy-proxies`
- 许可证：MIT；使用具体代码前仍需保留原始许可和署名。
- 同步脚本：`../scripts/sync-reference.sh`

### 值得参考的能力

根据项目公开文档，easy-proxies 是一个基于 sing-box 的订阅优先代理节点导入、测试、池管理和多端口网关，主要包含：

- HTTP / HTTPS 订阅、URI 列表、Base64 和 Clash / Mihomo YAML 导入。
- 并发异步节点测试和实时进度。
- candidate、pooled、failed 节点生命周期，而不是静默丢弃失败节点。
- `multi-port`、`pool`、`hybrid` 三种运行模式。
- 节点复测、地区识别、订阅刷新、端口检查和日志。
- 使用成熟代理内核处理协议，而不是业务层自行实现协议。

### HX-ProxyGroup 重点借鉴

- [ ] 订阅导入后的候选、成功、失败状态均可见。
- [ ] 刷新失败保留上一版节点。
- [ ] 测试任务提供实时进度。
- [ ] 节点和端口具有稳定生命周期。
- [ ] 参考其 sing-box 配置生成和进程管理方式。
- [ ] 参考其订阅格式测试样例。

### HX-ProxyGroup 不直接照搬

- HX-ProxyGroup v1 的数据面选用 Mihomo，以复用 Proxy Provider、Proxy Group、Load Balance、Sticky Sessions 和多 Listener。
- HX-ProxyGroup 以“多个订阅 + 可解释规则流水线 + 多代理组”为核心，而不是默认一节点一端口。
- HX-ProxyGroup 需要 30 天统计、告警、配置事务、回滚、systemd 服务和长期运行约束。
- HX-ProxyGroup 管理面和数据面严格分离，控制面不进入转发路径。

## 使用方式

```bash
bash scripts/sync-reference.sh
```

脚本行为：

- 目录不存在时执行浅克隆。
- 已存在 Git 仓库时执行 fast-forward 更新。
- 工作区有未提交修改时拒绝覆盖。
- 不使用 `reset --hard` 清理本地改动。
