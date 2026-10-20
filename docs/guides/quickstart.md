# 快速开始

三段可以只用其中一段。下面按"只用缓存代理" -> "只用浏览器" -> "组合"给出命令。

## 0. 环境

- Python 3.10+, 用 [uv](https://docs.astral.sh/uv/) 管理依赖
- Node 22+ 与 pnpm (只做控制台时需要)
- 一个 Chromium / Chrome 可执行文件 (浏览器包默认从系统找, 也可用 `CHROMIUM_PATH` 指定)

## 1. 只用缓存代理

```sh
cd packages/cache-proxy
uv run hxcp serve --port 8898
curl -s localhost:8898/stats | jq
```

此时它就是一个普通 HTTP 代理, 任何客户端都能用:

```sh
curl -x http://127.0.0.1:8898 https://example.com -o /dev/null -w '%{http_code}\n'
```

## 2. 只用指纹浏览器

```sh
cd packages/browser
uv run hxb serve --port 8325
uv run hxb new --name demo --seed demo-1
uv run hxb open --name demo
```

浏览器默认直连; 需要走代理时显式指定, 可以是**任意**代理, 不限于本仓库:

```sh
uv run hxb open --name demo --proxy http://127.0.0.1:7890
uv run hxb open --name demo --proxy socks5://127.0.0.1:1080
```

## 3. 组合: 浏览器 -> 缓存代理 -> 上游代理

```sh
cd packages/cache-proxy && uv run hxcp serve --port 8898 &
cd packages/browser && uv run hxb serve --port 8325 &
uv run hxb open --name demo --proxy http://127.0.0.1:8898
```

缓存代理自己也可以指向上游:

```sh
uv run hxcp serve --port 8898 --upstream http://127.0.0.1:7890
```

## 4. 控制台

```sh
cd apps/console-web
pnpm install
pnpm dev
```

控制台里有两块互不相通的区域: **人类操作** (需要本机人类令牌) 与 **Agent 会话** (按项目分组)。

## 5. 指纹评分

```sh
cd packages/fingerprint-audit
uv run audit.py --launch headless --json
```

退出码: 0 绿 / 1 黄 / 2 红, 可直接当流水线闸门。见 [fingerprint-score.md](fingerprint-score.md)。

## 常见问题

| 现象 | 原因 | 处理 |
|---|---|---|
| 浏览器打不开页面 | 代理地址不可用 | 先 `curl -x <proxy>` 验证, 或去掉 `--proxy` 直连 |
| 缓存命中率一直是 0 | 目标资源不可缓存 | 只有静态资源会命中; 动态请求原样透传 |
| Agent 请求 400 project_required | 没带项目名 | 加 `?project=<名字>` 或 `X-BCP-Project` |
| 人类通道 401 human_token_required | 本机没有人类令牌 | `uv run bcp-lane issue` 生成 |
