# Agent Note: 控制面 socket 路径必须与数据面一致

Status: implemented

## Problem

2026-10-13 生产机升级到 v0.13.0 之后: 数据面在跑、`7890`/`7891` 都在 LISTEN、HTML 秒开,
但面板一个数据点都出不来, 而且控制面日志每几秒刷一次:

```
collect Mihomo traffic snapshot: mihomo is not running
node quality pass failed: ... dial unix /var/lib/hx-pxy/.hx-pxy-runtime/e9f7df3e967e0c91/mihomo.sock:
    connect: no such file or directory
edge relay connection failed: dial tcp 127.0.0.1:7891: connect: connection refused   (每秒一次)
```

而 mihomo 本身完全正常: 它监听 `/var/lib/hx-pxy/runtime/mihomo.sock`, `active.yaml` 里写的
也正是这一条。也就是说**配置说的是一处, 控制面拨的是另一处**。

根因是 `controllerSocketPath` 按 `HOME` 有没有被设置二选一: 有值就返回
`<HOME>/.hx-pxy-runtime/<runtimeDirectory 摘要>/mihomo.sock`, 没有就返回
`runtimeDirectory/mihomo.sock`。而**同一个二进制会以不同环境被调用** —— systemd 用 `User=`
起常驻服务时会设 `HOME`, 而写 `active.yaml` 的那次进程没有。UNIX socket 路径就是这条链路的
身份, 差一个输入就等于两边各自对着空气说话, 且失败形态是"面板空白"而不是报错。

还有第二层, 比第一层更硬: 短路径对 systemd 托管的数据面**根本不可用**。data plane 的 unit 是
`ProtectSystem=strict` + `ReadWritePaths=/var/lib/hx-pxy/runtime`, mihomo 在 `.hx-pxy-runtime/` 下
建不了 socket。控制面一旦把短路径写进 YAML, 数据面会直接起不来 —— 全新安装就是这个组合。

## Decision

socket 路径由**数据面形态**决定, 并且在 `options` 之后计算 (`externalProcess` 必须先已知):

1. 直接路径 `runtimeDirectory/mihomo.sock`, 长度不超过内核 `sun_path` 上限 (107 字节可用) 时
   **一律用它**。它是数据面自己 `-d` 的目录, 两边天然一致, 且不依赖任何环境变量;
2. 只有直接路径超过上限时才挪到 HOME 短目录 —— 原方案保留, 但降级为窄路径;
3. systemd 托管 (`externalProcess`) 永远用直接路径, 即使超长。沙箱不允许别处, 此时应当让
   "路径太长"在明处失败, 而不是把失败伪装成"控制面认定数据面没运行"。

这条规则是 2026-10-03-rename-to-hx-pxy.md 里"按 runtimeDirectory 摘要派生短目录"那条的**收窄**:
摘要逻辑仍然存在, 但只作为超长时的回退。

## Alternatives considered

- **什么都不做, 让运维给 `hx-pxy.service` 加一条 `Environment=HOME=` 的 drop-in。** 最强理由:
  一行就能修好现场, 不动代码、不重启数据面、无停机; 2026-10-13 的现场就是这么止住血的,
  效果也被日志证实 (`external Mihomo process became available`)。否决: 它把"两边必须一致"
  这个不变式寄托在部署者记得设一个与代码无关的环境变量上; 全新安装仍然会写短路径, 而那样的
  数据面在沙箱下起不来。止血手段留在迁移文档里, 不作为修法。
- **扩展数据面沙箱 (`ReadWritePaths=.../.hx-pxy-runtime`), 保留原短路径。** 最强理由: 改动最小,
  逻辑一行不用动, 还顺手解决"沙箱不允许"这一层。否决: 把 socket 放到 `-d` 目录之外, 意味着
  以后每次排查都要先想"配置里的路径和 `-d` 不是一回事"; 而且它只治 systemd 这一种形态,
  HOME 敏感性还在 —— 同一个 bug 会在别的调用方式下原样重演。
- **把 runtimeDirectory 换成软链缩短。** 最强理由: 根因是路径长度, 这是唯一直接治根的选项。
  否决: `--data-dir` 同时决定数据库与 snapshots 的位置, 换它要迁数据; 这是 2026-09-28 已经
  否决过的方向, 结论不变。

## Consequences

- 路径不再随 `HOME` 变化; `HOME` 现在只影响"超长时回退"这一条窄路径。
- "systemd 托管 + runtimeDirectory 超过 107 字节"会明确失败 (mihomo bind 失败), 而不是悄悄错位。
  这是有意的: 该组合在沙箱下无解, 需要改部署而不是改代码。
- `.hx-pxy-runtime/<摘要>/` 不再被创建; 已存在的空目录是历史残留, 可以直接删。
- `shortControllerSocketPath` 保留, 但只服务"自带数据面 + 深目录"(开发机) 这一种场景。

## Testing

- `TestSystemdManagedSocketPathIgnoresHome`: 三组 `HOME` (空 / `/var/lib/hx-pxy` / `/root`) 下,
  systemd 托管的数据面都拿到 `runtimeDirectory/mihomo.sock`, 且断言
  `compiler.controllerSocket == manager.controllerSocket` —— 这条直接钉住本次事故: 写进
  `active.yaml` 的路径和拨号用的路径必须是同一条。
- `TestSelfManagedSocketPathUsesDirectPathWhenItFits`、
  `TestSelfManagedSocketPathFallsBackToShortPathWhenTooLong` (短 HOME 下回退路径确实落在
  107 字节内)、`TestSystemdManagedSocketPathStaysDirectEvenWhenTooLong`。
- 现场验证: 事故机以 `Environment=HOME=` 止血后, 控制面日志出现
  `external Mihomo process became available`, `7890`/`7891` 由 mihomo 正常 LISTEN。
- 未做的: 全新安装 (空库 + 全新 unit) 的端到端演练。上面的结论来自单元测试与现场日志,
  没有在干净机器上从零装过一遍。
