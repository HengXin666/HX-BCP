# HX-BCP

**Browser · Cache Proxy · Proxy** —— 一条可拆分、可组合的调试链路: 指纹浏览器 + 省流缓存代理 + 统一出口代理。

面向两类使用者, 同一个仓库:

| 使用者 | 能做什么 | 用什么入口 |
|---|---|---|
| **人** | 打开真实浏览器窗口, 手动操作, 看流量与指纹 | 控制台 (Console) + 本地浏览器实例 |
| **Agent** | 通过 HTTP API 起会话、取页面、跑自检评分 | Agent 通道 (必须带项目名) |

两条链路**互不相通**: 人手动开的会话, Agent 无法接管; Agent 起的会话, 人可以只读查看。

## 形态

```text
packages/
  browser/              指纹浏览器: 环境创建 / 启动 / CDP 接入 / 指纹派生
  cache-proxy/          省流缓存代理: 静态资源缓存 (不终结 TLS) + 抓包回放
  fingerprint/          指纹采集与判定
  fingerprint-audit/    环境质量自检: 出红/黄/绿, 可直接当流水线闸门
  fingerprint-insight/  指纹画像与对比
apps/
  console-web/          控制台 (React + @hx/ui)
docs/                   架构 / 用法 / 参考
```

三段链路**可以只用其中一段**, 也可以串起来:

```text
浏览器 --(自定义代理)--> 缓存代理 --(可选)--> 上游代理 --> 目标站点
浏览器 --(自定义代理)--> 上游代理 --> 目标站点
浏览器 --(直连)--> 目标站点
```

不强制使用本仓库的上游代理: 浏览器支持任意 `http://` / `socks5://` 代理地址。

## 快速开始

```sh
# 缓存代理 (Python, 需要 uv)
cd packages/cache-proxy && uv run python -m hxcp.api --port 8898

# 指纹浏览器 (Python, 需要 uv)
cd packages/browser && uv run hxb serve --port 8325

# 控制台 (Node 22 + pnpm)
cd apps/console-web && pnpm install && pnpm dev
```

五个包都是可安装库, 同一份代码既能 `uv run`, 也能被下游项目当 git 依赖消费:

| 目录 | 发行名 | 导入名 | CLI 入口 | 数据目录覆盖 |
|---|---|---|---|---|
| `packages/browser` | hx-browser | `hx_browser` | `hxb serve --host --port [--reload]` | `HX_BROWSER_DATA_DIR` (优先于 `settings.json` 的 `data_dir`) |
| `packages/cache-proxy` | hx-capture-proxy | `hxcp` | `hxcp` (缓存库) / `python -m hxcp.api --port` (HTTP 服务) | `--cache-dir` |
| `packages/fingerprint` | hx-fingerprint | `probe` | `hxfp` | 无 |
| `packages/fingerprint-audit` | fp-audit | `audit` / `rules` / `probes` | `fp-audit` | 无 |
| `packages/fingerprint-insight` | fp-insight | `fpinsight` | `fp-insight serve --host --port` | `FPI_DATA_DIR` |

```sh
uv add "hx-browser @ git+https://github.com/HengXin666/HX-BCP.git@v0.2.0#subdirectory=packages/browser"
```

推 `v*` tag 时 `.github/workflows/release.yml` 构建五个 wheel 并挂到 GitHub Release。

操作细节见 [docs/guides/quickstart.md](docs/guides/quickstart.md)。

## 人机双通道

- **人类通道**: 控制台按钮 / 本地 CLI 发起, 会话归属 `human`, 记录手动操作。
  该通道需要一个只在本机生成、只落本地文件的 **人类令牌**, Agent 的凭证包里没有它。
- **Agent 通道**: 每个请求必须带 `project` 参数 (或 `X-BCP-Project` 头), 会话按项目名原生分组;
  不带项目名直接 400。Agent 无法创建人类通道的会话。

规则与判据见 [docs/architecture/lanes.md](docs/architecture/lanes.md)。

## 评分为指纹评分

流水线的评分只认一件事: **指纹环境自检结果** (红 / 黄 / 绿 + 具体字段差异)。
它会给出可复算的检查项与失败原因, 而不是主观打分。见 [docs/guides/fingerprint-score.md](docs/guides/fingerprint-score.md)。

## 设计取向

1. **不终结 TLS**: 缓存代理只缓存静态资源, 不改动浏览器看到的证书与连接语义。
2. **每段可独立**: 任一段都能单独用, 也能与其它工具组合; 不绑定本仓库的其它组件。
3. **判据只有一处**: 同一事实 (端口 / 环境是否可用 / 会话归属) 只有一个实现, 其它地方引用它。
4. **可复算**: 任何结论都给出可重跑的命令与其原始输出。

## 许可

MIT, 见 [LICENSE](LICENSE)。

## 致谢

指纹浏览器思路参考 [nodecloak](https://github.com/nodecloak/nodecloak); UI 组件来自
[@hx/ui](https://github.com/HengXin666/HX-UI)。第三方来源登记在 [NOTICE](NOTICE)。
