# 端口与默认值

| 组件 | 默认端口 | 覆盖方式 |
|---|---|---|
| 缓存代理 | 8898 | `python -m hxcp.api --port` |
| 指纹浏览器 API | 8325 | `hxb serve --port` |
| 统一出口代理共享入口 (packages/proxy) | 7890 | 控制台里的入口配置; 也可换成任意外部上游 |
| 统一出口代理用户名方言入口 | 关闭 (建议 7880) | `HX_PXY_ENTRY_LISTEN`, 见 docs/architecture/proxy-entry.md |
| 统一出口代理内部 hub (仅环回) | 27890 | `HX_PXY_ENTRY_HUB_PORT` |
| 控制台 dev server | 由 Vite 决定 | `pnpm dev --port` |

## 约定

- 所有服务默认只监听 `127.0.0.1`; 需要对外时显式 `--host 0.0.0.0`。
- 端口只在各包的 `config` 里出现一次, 其它地方引用配置值。
- 上游代理地址属于**配置**, 不属于代码; 仓库里不写死任何具体线路。packages/proxy 只是默认实现, 浏览器与缓存代理不依赖它。
