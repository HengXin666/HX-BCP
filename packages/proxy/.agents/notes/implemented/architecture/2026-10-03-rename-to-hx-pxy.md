# Agent Note: 项目名统一为 HX-Pxy, 数据库文件改用新名并自动接管旧文件

Status: implemented

## Problem

同一个项目在仓库里同时存在四种写法, 覆盖了从品牌名到磁盘路径的每一层:

| 形态 | 旧值 | 出现规模 |
| --- | --- | --- |
| 品牌显示名 | `HX-ProxyGroup` | 187 文件 |
| Git 仓库名 | `HengXin666/HX-ProxyGroup` | 152 文件 |
| 控制面二进制 | `hx-proxygroupd` | 46 处 |
| systemd 单元 / 服务账号 / 路径 | `hx-proxygroup*`, `/var/lib/hx-proxygroup` | 205 处 |
| 环境变量前缀 | `HX_PROXYGROUP_*` | 44 处 |

而现实早已是另一个名字: 远端 `origin` 指向 `HengXin666/hx-pxy` (旧名 301 重定向过去),
HX-Jungle 侧的启动器、面板注册、外部消费者 (gh-registrar / gh-pool / freebuff-rotator)
全部按 `hx-pxy` 称呼它。也就是说**外部按新名调用, 仓库内部按旧名自称**。

这不是审美问题, 它已经产生了一个真实故障: `/home/hx/.local/bin/mihomo` 是一个指向
`.../gpt-json-to-token/HX-ProxyGroup/.tmp/hxwebx/bin/mihomo` 的符号链接, 而旧仓库路径
在项目搬进 `HX-Jungle/toolbox/hx-pxy` 后已不存在 —— 链接是**断的**。依赖 `mihomo`
出现在 PATH 上的调用方 (含本仓 `run_test.go` 起的端到端流程) 因此拿到的是"命令不存在"。

同一类风险的第二个实例更贵: 数据库默认文件名从 `hx-proxygroup.db` 改成 `hx-pxy.db`
后, 任何按新默认值启动的实例都会在空路径上**新建一个空库** —— 管理员账号、订阅、节点、
Listener 全没了, 而表面症状只是"登录不进"。

## Decision

一次性把项目名统一为 **HX-Pxy**(标识符小写 `hx-pxy`), 并对旧数据库文件做**自动接管**。

替换矩阵按"该字符串进不进机器"分两档:

- **机器标识符**: 二进制 `hx-proxyd`→`hx-pxyd`, 安装器 `hx-pxy-install`, systemd 单元
  `hx-pxy.service` / `hx-pxy-dataplane.service` / `hx-pxy-terminal.service`, 服务账号与
  路径 `hx-pxy` / `/var/lib/hx-pxy`, 数据库 `hx-pxy.db`, 环境变量 `HX_PXY_*`,
  Go module `github.com/HengXin666/hx-pxy`。
- **显示名**: `HX-ProxyGroup` → `HX-Pxy`, 含前端标题、README、docs、LICENSE、注释。

数据库接管放在 `cmd/hx-pxyd/main.go` 的 `migrateLegacyDatabasePath`: 仅当**目标路径不存在
且旧文件存在**时, 把 `hx-proxygroup.db` 连同 `-wal` / `-shm` 侧车一起改名到新名。
`-wal` 不是可选项 —— WAL 模式下已提交数据留在 `-wal` 里直到 checkpoint, 只改主文件会
静默丢掉最近的写入。

## Alternatives considered

**什么都不做, 只改显示的字符串。** 这是最省事的读法, 也确实能消掉用户看到的不一致。
但断掉的 `mihomo` 链接与"空库"风险都不是显示问题: 前者让端到端测试拿到 503, 后者会让
登录在改名后**真的**坏掉。只改显示等于把两个真故障留在地板上。

**改 db 文件名时不用迁移, 直接把旧文件 `mv` 一次。** 一次性脚本更简单, 且没有长期代码。
但旧文件散落在多处 (`.tmp/hxwebx/rundata/`、`.tmp/run-data/`、备份目录), 我无法保证覆盖
每一个部署形态; 漏掉的那个就是"启动即空库"。把接管放在启动路径上, 则**任何**仍留着旧
文件的部署都会自愈, 不依赖我去枚举。代价是一段只在路径缺失时才运行的代码。

**保留 `HX_PROXYGROUP_*` 环境变量名以兼容既有调用方。** 环境变量是最难一次性替换的形态:
本机 gh-registrar 确实读 `HX_PROXYGROUP_BASE` / `HX_PROXYGROUP_KEY`, freebuff-rotator 读
`HX_PROXYGROUP_URL` / `HX_PROXYGROUP_API_KEY`。但它们的**默认值**已经是新地址
(`http://127.0.0.1:19090`), 环境变量只是可选覆盖; 而 `run.sh` 从不导出这些变量, 生产启动
也没有任何地方设置它们 (systemd 单元里只有 `GOMAXPROCS` / `HOME` / `PATH`)。为一个实际
无人设置的名字保留双写, 会把"统一"变成"再加一层别名", 违背本次要求。

## Consequences

- **改名是跨 216 文件、968 处的机械替换**, 因此顺序敏感: 长标识符必须先于短前缀替换,
  否则 `hx-proxygroupd` 会被 `hx-proxygroup` 先吃掉。实现上按长度降序排列规则。
- **`/__hx-proxy__/` 保持不变。** 它是线上数据契约: 存量住宅渠道的
  `ws_path` 逐字存着它, 外部消费者 (gh-registrar 的 mihomo 配置、OutlookRegister 的测试)
  也按它拼 URL。只把**新建默认值的段名** `/__hx-proxy__/hx-proxy` 改成
  `/__hx-proxy__/hx-pxy`: 存量 vless 渠道的 `ws_path` 是显式值, 不读默认值, 不受影响。
- **Go package 名必须用合法标识符。** 首轮替换把 `package hxproxygroup` 变成了
  `package hx-pxy`(非法), 编译直接报 "found packages"。统一为 `package hxpxy`。
- **module path 大小写**: 替换先产出 `github.com/HengXin666/HX-Pxy`, 但 GitHub 仓库是全小写
  `hx-pxy`, 已再统一一次。
- **`/home/hx/.local/bin/mihomo` 断链已修复**并指向新仓库路径。
- 改名顺带撞出两个既有的跨文件缺陷, 都在本次一并修掉 (它们的触发只差一个字符串):
  - `run.sh` 用 `"${package_manager}" run dev -- <vite flags>` 起前端。pnpm / bun 会把
    `--` 之后的内容**原样**交给脚本, 于是 `--` 本身成了 Vite 的**位置参数**, 后面四个 flag
    全部被丢掉, Vite 落回默认端口 5173 —— 5173 被别的项目占用时就是
    `Port 5173 is already in use` 后退出, run.sh 认作"前端没起来"。按包管理器决定是否加
    `--`(npm / yarn 需要, pnpm / bun 不能加)。
  - `controllerSocketPath` 给所有 Manager 返回**同一个固定路径**。同进程里可以有多个
    Manager(测试即是), unix socket 路径就是它的身份, 共用一条路径会让第二个 Manager
    `bind: address already in use`。改为按 runtimeDirectory 摘要派生短目录 —— **2026-10-14 起
    这条摘要短目录只在直接路径超过 `sun_path` 上限时才使用, 且 systemd 托管的数据面永不使用**:
    它按 `HOME` 取值, 而写配置的进程与常驻服务不一定有同样的 `HOME`, 于是配置与拨号会指向
    两条路径; 原因与现场见
    2026-10-14-controller-socket-path-must-match-the-data-plane.md。
- 旧名残留仅剩历史文档中的记录 (knowledge 目录下的既有笔记)、`cmd/hx-pxyd/main.go` 里的
  `legacyDatabaseName` 常量与 `README`/`install.sh` 中说明改名的文字, 均属预期。
- `web/dist` 已被重建, 产物内无旧名。

## Testing

- `go build ./...` / `go vet ./...` 通过; `gofmt -l` 无输出。
- `go test ./internal/... ./cmd/...` 全过。
- 根包 `format` / `install` / `TestRunScriptUsesAndReleasesRandomHighPorts` 通过;
- 新增 `cmd/hx-pxyd/main_test.go` 4 条用例覆盖接管逻辑: 主线+侧车一起迁移、目标已存在时
  不覆盖、无旧文件时不创建、无侧车时仍可迁移。
- 实机验证: 用改名后的二进制对生产库副本启动, 旧库被接管为 `hx-pxy.db` (含 `-wal`),
  `loli` 账号完好 (password_version=2), 用正确口令登录得 200、旧口令得 401。
- 前端: `web/index.html` 与 Vite 开发实例均已显示 `HX-Pxy`; 经面板前缀
  `/proxy/hx-pxy/` 访问同样显示新名; `web/dist` 重建后无旧名。
- `node .agents/skills/hx-agent-notes/scripts/verify-all.ts` 全绿。
- **`TestRunScriptStartsFrontendAndProxiesBackend` 仍失败, 但不是本次改动引起**: 它在
  临时实例里建的 Listener 要让 Mihomo 绑 `0.0.0.0:7890`, 而本机生产实例正持有该端口 ——
  502 的错误体是
  `listener apply failed: Mihomo listener 0.0.0.0:7890 is unavailable: bind: address already in use`。
  在 `git worktree` 出来的 HEAD 干净副本上同条件复现, 同样是 502, 故与本改动无关。
