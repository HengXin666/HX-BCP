# 配置中心端点（/provision/<token>，v0.7.1，2026-08-21）

> 用户方案：HX-Pxy 是**配置中心/授权机构**，不是转发中继。消费者
> （如 HX-OutlookRegister 的 cf_bpb 预设）从 `/provision/<token>` 拉取 CF
> worker 订阅 URL 清单，**本地隧道直连 worker**，流量不经过本控制面。
>
> 住宅链路不变：住宅身份继续用 `/ctl/` `/rot/` 刷新 IP 接口。

## 端点

```
GET /provision/<token>
```

- **token 即唯一凭据**（同 `/sub/` `/rot/` `/ctl/` 模式，token 之外不要求会话）。
- 由全局设置门控：`provision { enabled, token }`（`PUT /api/v1/settings`）。
- 响应 `text/plain`：**每行一个启用 cf-worker provider 的订阅 URL**。
- 无启用 provider 时返回 204；token 错误 / 未启用 / 路径穿越一律 404
  （路由不可探测，`subtle.ConstantTimeCompare` 防时序侧信道）。
- token 校验 `validID`（1-32 位小写字母数字 `- _`）；`normalize` 时未启用
  自动清空 token。

示例：

```text
$ curl https://pxy.woa.qzz.io/provision/cfg-token-42
https://alder42.alexrennie293.workers.dev/ab7851d339bf/sub?app=xray
https://willow95.alexrennie293.workers.dev/345b09e69e47/sub?app=xray
```

## 服务层

`residential.Service.CfWorkerSubscriptions(ctx) ([]CfSubscription, error)`：
枚举所有 `enabled && rotation_mode=cf-worker` 的 provider，解密 `worker_url`
（secrets envelope），返回 `{name, url}` 清单。**纯配置导出**，不依赖
channel / 数据面 / 节点池。

## 消费者

HX-OutlookRegister cf_bpb 预设：`url` 填 `/provision/<token>` 时自动进入
配置中心模式（拉清单 → 轮换取用 → 本地直连），见其
`docs/architecture/CF_PROVISION_CENTER_20260821.md`。

## 安全

- token 不落日志（`/provision/` 前缀与其他公开 token 路由一样被日志脱敏——
  见 server.go 的 loggedPath 处理）。
- 只导出订阅 URL（worker 订阅本身是公开可拉的）；不导出 provider 凭据、
  节点、channel 信息。
- 404 统一应答，不区分"token 错"与"未启用"，防探测。
