# Agent Note: 产品自己的日志按 mtime 保留 30 天 (mtime-based log retention)

Status: implemented

## Problem

2026-10-14 用户要求: "日志仅保留30天，过期自动清理"。范围是**产品自己写的日志文件**:
受管 Mihomo 的日志、运行时目录、data 目录下产品写的 `*.log` —— 不是 run.sh 的
`.tmp/run/logs`, 也不把实时日志落库。

两处都只有"按字节"的机制, 没有时间维度:

- `internal/dataplane/mihomo/manager.go` 用 `newRotatingLogWriter(m.logPath, ...)` 写
  `<runtimeDir>/mihomo.log`, 默认 8 MiB × 2 个备份。轮转只保证**总量**有界, 不保证
  时间有界: 一个几乎不写日志的实例可以把一年前的 `mihomo.log.2` 一直留在盘上。
- 控制面按运行目录的 sha256 前 8 字节在 `~/.hx-pxy-runtime/<hash>/` 建短实例目录
  (见 `shortControllerSocketPath`)。每个不同的运行目录 (测试的 `t.TempDir()`、旧的
  安装路径) 都会留下一个新目录, 而**没有任何东西删过它**。本机实测已累积 92 个。

文件日志清理需要与系统日志的保留职责明确分离。

## Decision

新增 `internal/logretention`: 一个只做文件系统删除的小包。

- `Prune(ctx, Policy, whitelist, maxEntries)` 遍历一个根目录, 删除 `Days` 天前 (按 mtime)
  的条目。两种模式:
  - `ModeFiles`: 只删 basename 匹配 `mihomo.log*` 的**普通文件** (覆盖 `mihomo.log` 与
    `.1/.2/...` 轮转备份)。目录结构、`active.yaml`、数据库、`master.key`、快照、artifacts
    都不匹配, 因此不会被碰。
  - `ModeInstanceDirectories`: 把 `<home>/.hx-pxy-runtime` 下的每个直接子目录当成实例目录,
    过期的整体删除; 子目录名必须是 16 位小写十六进制 (产品自己的哈希命名) 且含
    `mihomo.sock`, 两个条件都不满足就宁可漏清。
- 年龄判据是 mtime。文件用自己的 mtime; 实例目录取"目录自身 mtime 与直接子项 mtime 中
  最新者"—— 删除目录会连内容一起删, 用一个比内容更新的时间做判据只会更保守。
- 活跃路径 **不是** 靠 mtime 猜的: 调用方必须显式传入白名单 (当前活跃实例目录 + 活跃
  `mihomo.log`), 白名单路径永不删除且不进入其子树。
- 上限: 一次 `Prune` 最多访问 2000 个条目（包括不匹配的文件与目录） (`Report.Truncated` 标记被截断, 下一个 tick 重新扫描),
  单文件超过 64 MiB 只记录不删。符号链接一律不跟随、不删除, 删除前再 `lstat` 复核一次
  (判定与删除之间路径可能被换成链接)。删除失败记进 `Report`, 不中断本轮。
- `Runner` 是控制面的后台任务: 启动时立即跑一次, 之后每 24h 一次, `Run` 随 ctx 取消返回
  (与 scheduler 同一形状, 不留永久 goroutine)。
- 保留天数来自 `internal/systemsettings` 的新字段 `log_retention_days` (默认 30, 0 禁用,
  合法区间 0..3650)。`Runner` 每次 tick 重新读设置, 因此设置页改动无需重启即可生效;
  `Load` 在 `Default()` 之上解码, 所以没写过这个键的旧文档拿到默认 30, 而显式存过 0 的
  文档仍然是禁用。

生产 systemd unit 使用独立 `hx-pxy` journal namespace，最长保留 30 天、1 天轮转，另有容量上限。
安装器要求 systemd >=245 并部署 journald@hx-pxy.conf；不全局 vacuum 旧共享日志。
该策略与文件清理天数设置独立，非 systemd stdout 由调用方管理。

## Consequences

- 受管 Mihomo 的轮转备份与 data 目录下的旧日志按 mtime 和每日扫描周期清理; 正在写的 `mihomo.log`
  由白名单保护，在下一次写入时按字节或 24 小时年龄轮转。此策略不是逐行精确 TTL；频繁重启会影响活动文件的年龄。
- `~/.hx-pxy-runtime` 下过期的测试/旧安装实例目录会被清掉; 当前活跃目录被排除；其他旧实例目录中的 Unix socket 若仍可连接也跳过，未知连接错误保守保留。
  那 92 个陈旧目录中, 没有 `mihomo.sock` 的空目录**不在**清理范围内 (宁可漏清) ——
  这是刻意的保守, 不是遗漏。
- 系统设置新增 `log_retention_days` 后, PUT /api/v1/settings 的全量文档语义会把没带这个键
  的客户端写成 0 (关闭清理) 而不是 30。与 guard 字段不同, 0 在这里是合法且明确的状态,
  所以没有加三态指针; 想要 30 的客户端必须显式发送它。
- 保留期从 30 改成别的值时, 不需要重启控制面; 下一个 tick 生效。

## Testing

- `internal/logretention/policy_test.go` 用 `t.TempDir()` 造过期/未过期文件与实例目录, 断言
  只删该删的: 过期删、29 天留、白名单留、符号链接跳、非日志文件 (db/key/yaml/zip) 留、
  非哈希命名目录留、无 `mihomo.sock` 目录留、条目上限截断、0 天禁用、根目录不存在不报错、
  取消后不再删、`Run` 取消即返回 (无 goroutine 泄漏)。
- `TestInstanceDirectoryMatchesManagerAlgorithm` 用固定的 sha256 结果钉住实例目录算法。

## Alternatives considered

- **什么都不做 / 交给 logrotate 或 systemd**: 最强的理由是 host 上本来就有 logrotate, 而且
  `install.sh`/systemd unit 都在本仓控制之下, 加一个 drop-in 配置看起来最省代码。但那会把
  清理逻辑放到本进程之外: 打包安装才能拿到, 开发/测试实例拿不到, 而"92 个陈旧实例目录"这类
  问题恰恰出现在开发机上; 而且外部工具不认识 `~/.hx-pxy-runtime` 里哪些目录是产品自己的。
  —— 否决。
- **扩展 `logwriter.go` 的字节轮转, 让它按时间删除**: 理由是最贴近日志写入路径, 能在轮转那一刻
  顺手删旧的。但字节轮转只在写满时才触发, 一个安静实例可能几个月不轮转, 过期备份就会一直留着;
  而且它只覆盖 `mihomo.log*`, 管不到实例目录。时间保留需要一个独立的、按天触发的路径。
  —— 否决。
- **不再复算摘要, 直接遍历 `~/.hx-pxy-runtime` 并靠"目录里有没有 socket"判断活跃**: 理由是不必
  在第二个包里重复 `shortControllerSocketPath` 的哈希算法 (重复算法正是 2026-10-13 那次
  "数据面在跑、控制面认定它没运行"事故的形态)。但"有 socket 文件"并不等于"正在使用" ——
  进程退出后 socket 文件会留下, 靠它区分不了活跃与陈旧, 反而会把活跃目录删掉。所以摘要算法
  仍然重复一次, 并用单测把两边的结果钉死; 一旦数据面改算法, 这个测试是第一个失败的。
  —— 否决。
- **在 `modeInstanceDirectories` 里把根目录下所有陈旧子目录都删掉 (不要求哈希名/marker)**:
  理由是本机 92 个目录里多数是空目录, 要求 marker 等于清不掉它们。但 `~/.hx-pxy-runtime`
  是一个普通的 HOME 子目录, 放宽到"任意陈旧子目录"就是把删除权交给一个别人也可能用的目录名;
  误删的代价 (用户数据) 远大于漏清空目录的代价。 —— 否决。

活跃 socket 保护与安静日志年龄轮转分别由 internal/logretention/socket_test.go 和 internal/dataplane/mihomo/logwriter_age_test.go 验证。

截断会记录告警；每轮从根重新扫描，不持久化扫描游标。超过上限且前部长期保留的树可能需要管理员收窄清理根或整理无关文件，不能声称下一轮必然到达尾部。
