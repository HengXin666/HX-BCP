# Agent Note: 本机回环直接算人类通道

日期: 2026-11-01. 证据: .review/loopback-human.md

## Problem

人类通道只认 x-bcp-human-token 头或 bcp_human 会话 cookie, 两者都要先粘一次本机令牌. 本机浏览器打开 8325 也弹 "解锁人类通道", 等于让本机用户证明 "我是我自己". HX-WebX 面板 (server/src/guard.ts) 已按同一理由对本机回环免登录.

## Decision

- lane.py 新增 is_loopback(peer), host_is_loopback(host), loopback_admitted(peer, host); decide() 新增 loopback 参数. 判据只在 lane.py, lane_guard.decide_from 只负责把 conn.client.host 与 Host 头传进去.
- peer 只取连接的真实对端 (ASGI scope client = socket 地址). 不看 X-Forwarded-For / X-Real-IP / Forwarded. hxb serve 以 proxy_headers=False 启动 uvicorn, 否则 uvicorn 会在对端属于 forwarded_allow_ips 时用 X-Forwarded-For 改写 scope client (FORWARDED_ALLOW_IPS='*' 时公网可伪装 127.0.0.1, 已实测).
- Host 是附加收窄条件, 不是证明: peer 必须是回环, 且 Host 必须是 localhost / 127.x / [::1]. 它只用来挡 DNS rebinding (恶意网页把自己的域名解析到 127.0.0.1 后, 请求对端是回环, 但 Host 是它的域名). 单靠 Host 永远不会放行.
- 通道仍只由 x-bcp-lane 头选择: 回环但不带 human 头仍是 agent, 仍要项目名, 两条通道没有合并.
- 三种人类证明是 "任一即可" 的并列关系, 按 loopback, session, token 的顺序检查, 命中哪条记在 audit["by"], /api/v1/lane/session 回 by 字段. 顺序只影响审计记录, 不影响放行结果.
- 前端 HumanGate 不改: 它先 GET /api/v1/lane/session (带 x-bcp-lane: human), 回环下直接得到 human=true, 不再弹窗.

## 同权限论证 (为什么不扩大攻击面)

- 8325 由 HX-WebX 以 --host 127.0.0.1 启动, 能连上它回环端口的只有本机进程. 本机进程与 HX-BCP 同一用户时, 它本来就能 cat ~/.bcp/human.token (0600 只挡别的用户) 或直接改 data 目录, 令牌对它不构成门槛. 所以对回环要令牌只增加了人的摩擦, 没有增加安全.
- 本机其他用户的进程也能连回环, 这一点之前也成立: 他们拿不到令牌, 但过去它们能做的只是 agent 通道; 现在它们拿到人类通道. 这是本次唯一的实质让步, 前提是这台机器是单用户开发机 (与 HX-WebX 面板的假设相同). 多用户机器上不应启用本机放行.
- 经 HX-WebX /proxy/hx-browser/ 进来的请求对端也是 127.0.0.1, 因此也算本机. 这意味着 "谁通过了面板鉴权, 谁就是 8325 的人类": 面板对回环免登录, 对 LAN 要面板会话. 面板是边界, 这与用户在面板侧的决定一致.
- "人类令牌只在本地文件" 这条没变: 令牌仍只写本机 0600 文件, 不进前端产物, 不进 Agent 环境; 非本机来源仍必须出示令牌或由令牌换来的会话.

## 必须同步改的情形

- 在 8325 前面加任何反向代理 (nginx, cloudflared, safeline, 面板以外的转发): 所有请求都会以 127.0.0.1 到达, 本条放行会把整台服务暴露出去. 加反代前必须先改 loopback_admitted (例如只信任特定 unix socket 或关掉本机放行).
- 改成直接用 uvicorn 启动或改 cli.py 时, 必须保持 proxy_headers=False.
- HX-WebX 若开始把面板暴露到公网而不鉴权, 本条同样随之失效.

## Alternatives considered

- 看 X-Forwarded-For / X-Real-IP: 客户端可写, 否决.
- 只看 Host 是 localhost: 客户端可写, 否决; 只保留为 rebinding 收窄.
- 回环时自动签发会话 cookie: 多一层状态, 收益为零, 否决.
