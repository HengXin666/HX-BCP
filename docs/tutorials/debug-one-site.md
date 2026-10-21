# 教程: 用 浏览器 + 缓存代理 调试单个站点

本教程演示最小调试链路: **一个浏览器实例 → 本地缓存代理 → 目标站点**, 只针对一个站点, 看清楚哪些请求命中缓存、省了多少流量, 出问题时怎么定位。

```
┌──────────────┐   HTTP(S) 代理    ┌──────────────┐      上游       ┌──────────────┐
│ 浏览器 (独立  │ ───────────────▶ │  缓存代理     │ ─────────────▶ │  目标站点     │
│ profile)      │ ◀─────────────── │ (命中则不出网) │ ◀───────────── │ example.com  │
└──────────────┘                   └──────┬───────┘                └──────────────┘
                                          │ /stats
                                          ▼
                                    命中率 / 省流
```

> **合并说明 (给维护者)**: 本文由子 agent 在看不到仓库源码的情况下编写。下面「变量约定」里的启动命令、端口、stats 地址与字段名是**占位**, 合并前请按仓库实际 CLI / 配置校正, 正文其余步骤引用的都是这些变量, 改一处即可。

## 0. 变量约定

先在终端里设好这几个变量, 后面所有命令都引用它们:

```bash
# 要调试的站点 (只调这一个)
export SITE="https://example.com"

# 缓存代理监听地址 (占位: 按仓库默认配置修改)
export PROXY_HOST="127.0.0.1"
export PROXY_PORT="8080"
export PROXY="http://${PROXY_HOST}:${PROXY_PORT}"

# stats 接口 (占位: 按仓库实际路由修改)
export STATS_URL="http://${PROXY_HOST}:${PROXY_PORT}/stats"

# 缓存代理启动命令 (占位: 按仓库实际入口修改)
export CACHE_PROXY_CMD="python -m cache_proxy --listen ${PROXY_HOST}:${PROXY_PORT}"

# 浏览器专用 profile 目录, 和日常浏览器隔离
export PROFILE_DIR="/tmp/hx-bcp-debug-profile"
```

## 1. 起服务

在仓库根目录, 用 uv 起缓存代理 (前台运行, 方便看日志):

```bash
uv run --no-project ${CACHE_PROXY_CMD}
```

另开一个终端确认代理已就绪:

```bash
# 端口在监听
nc -z "${PROXY_HOST}" "${PROXY_PORT}" && echo "proxy up"

# stats 能访问
curl -s "${STATS_URL}"

# 走代理请求一次目标站点, 能拿到状态码说明链路通了
curl -s -o /dev/null -w '%{http_code}\n' -x "${PROXY}" "${SITE}"
```

如果站点是 HTTPS 且代理做 TLS 解密 (MITM) 才能缓存, 需要让客户端信任代理的 CA 证书。curl 可临时用 `--cacert <代理CA证书路径>` 验证; 浏览器见下一步。

## 2. 打开浏览器并指向缓存代理

用一个**独立 profile** 启动 Chromium/Chrome, 只让这个实例走代理, 不影响日常浏览器:

```bash
# Linux (macOS 把可执行文件换成
# "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
chromium \
  --user-data-dir="${PROFILE_DIR}" \
  --proxy-server="${PROXY}" \
  --proxy-bypass-list="<-loopback>" \
  --disable-quic \
  --no-first-run \
  "${SITE}"
```

参数说明:

| 参数 | 作用 |
|---|---|
| `--user-data-dir` | 独立 profile, 缓存/cookie/扩展与日常浏览器隔离 |
| `--proxy-server` | 所有 HTTP/HTTPS 请求走缓存代理 |
| `--proxy-bypass-list="<-loopback>"` | 去掉 Chromium 默认的 localhost 直连规则, 本地站点也走代理 (不调本地站点可省略) |
| `--disable-quic` | 禁用 HTTP/3 (QUIC/UDP), 否则部分请求会绕过 HTTP 代理 |

HTTPS 需要代理解密时, 把代理的 CA 证书导入这个 profile 信任 (设置 → 隐私和安全 → 安全 → 管理证书 → 导入), **不要**用 `--ignore-certificate-errors` 这类全局放开校验的参数长期调试。

调试时打开 DevTools → Network:

- 首次加载可勾选 **Disable cache**, 让请求全部打到代理, 便于看代理侧的命中情况;
- 之后取消勾选并刷新, 对比浏览器自身缓存与代理缓存各自的作用。

## 3. 看 stats 里的命中率与省流

先冷启动加载一次站点 (缓存是空的, 基本全是 miss), 再刷新 2~3 次, 然后看 stats:

```bash
curl -s "${STATS_URL}" | jq .
```

关注的指标 (字段名以仓库实际 stats 输出为准):

| 指标 | 含义 | 计算方式 |
|---|---|---|
| hits / misses | 命中 / 未命中次数 | 直接读 |
| **命中率** | 有多少请求由缓存直接返回 | `hits / (hits + misses)` |
| bytes_from_cache | 由缓存返回的字节数 | 直接读 |
| bytes_from_upstream | 实际从上游拉取的字节数 | 直接读 |
| **省流比例** | 少走了多少上游流量 | `bytes_from_cache / (bytes_from_cache + bytes_from_upstream)` |

如果 stats 只给原始计数, 可以用 jq 自己算 (字段名按实际修改):

```bash
curl -s "${STATS_URL}" | jq '{
  hit_rate:   (.hits / ((.hits + .misses) | if . == 0 then 1 else . end)),
  saved_ratio:(.bytes_from_cache / ((.bytes_from_cache + .bytes_from_upstream) | if . == 0 then 1 else . end)),
  saved_mb:   (.bytes_from_cache / 1048576)
}'
```

想持续观察, 每 2 秒刷一次:

```bash
watch -n 2 "curl -s '${STATS_URL}' | jq -c ."
```

怎么读这些数:

- 第一次加载命中率接近 0 是正常的;
- 刷新后静态资源 (JS/CSS/图片/字体) 应基本命中, HTML 与 API 请求通常不命中 (`Cache-Control: no-store` / `private`, 或带 cookie);
- 只看本站点的效果时, 每次实验前重启代理或清空计数, 并保证浏览器是独立 profile, 避免其它标签页的请求混进统计。

## 4. 常见故障排查表

| 现象 | 可能原因 | 排查 / 解决 |
|---|---|---|
| 浏览器报 `ERR_PROXY_CONNECTION_FAILED` | 代理没起来、端口不对、监听地址不对 | `nc -z ${PROXY_HOST} ${PROXY_PORT}`; 看代理终端日志; 确认 `--proxy-server` 与 `PROXY` 一致 |
| 启动代理报端口被占用 (`Address already in use`) | 上次进程没退出或端口被其它程序占用 | `lsof -i :${PROXY_PORT}` 找到进程结束它, 或换 `PROXY_PORT` |
| HTTPS 页面报 `NET::ERR_CERT_AUTHORITY_INVALID` | 代理做 TLS 解密, 但 profile 未信任代理 CA | 把代理 CA 导入该 profile; 用 `curl --cacert` 先验证证书链 |
| 页面能开, 但 stats 计数不涨 | 浏览器实例没走代理 (复用了已开着的旧实例, 参数没生效) | 关掉所有使用同一 `--user-data-dir` 的窗口后重开; 访问 `chrome://net-internals/#proxy` 确认生效的代理配置 |
| 部分请求不经过代理 | HTTP/3 (QUIC) 走 UDP 绕过; localhost 默认直连 | 加 `--disable-quic`; 加 `--proxy-bypass-list="<-loopback>"` |
| 命中率始终为 0 | DevTools 勾了 Disable cache 时请求头带 `Cache-Control: no-cache`; 响应 `no-store` / `private`; URL 带时间戳等防缓存参数 | 刷新前取消 Disable cache; 用 `curl -sI -x ${PROXY} <资源URL>` 看响应缓存头; 防缓存参数需按代理的缓存键规则处理 |
| 命中率高但页面内容是旧的 | 缓存未按 TTL / 校验头过期, 或缓存键忽略了关键参数 / `Vary` | 清空该站点缓存后重试; 检查缓存键是否包含必要的 query / 请求头 |
| 登录态异常、页面数据串号 | 带 cookie / `Authorization` 的响应被共享缓存 | 确认代理不缓存带认证信息的请求; 用独立 profile, 调试结束清理缓存 |
| 省流比例远低于命中率 | 命中的多是小文件, 大文件 (视频、大图) 未命中或走了 Range 请求 | 按 URL 看哪些大响应是 miss; 确认代理对 `Range` / 分段响应的处理 |
| WebSocket / SSE 连接断开 | 长连接经代理被超时或被缓冲 | 看代理日志里的超时; 这类连接不应被缓存, 确认代理直接透传 |
| stats 接口 404 或连不上 | stats 地址/端口与代理监听不同, 或未开启 | 按仓库配置修正 `STATS_URL` |

排查顺序建议: **先 curl 走代理验证链路 → 再看浏览器是否真的走代理 → 最后看缓存头与 stats**。每一步都能单独复现, 问题就被缩到一个环节里。
