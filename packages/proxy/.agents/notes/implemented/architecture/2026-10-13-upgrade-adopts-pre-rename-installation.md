# Agent Note: 升级接管改名前的部署, 发布链路随改名一起改完

Status: implemented

## Problem

2026-10-03 的改名把仓库内的标识符统一成 `hx-pxy`, 但范围停在源码与仓库内文档上, 两条**只在发布或升级时才执行**的链路被漏掉, 而它们的失败都是静默的:

1. `.github/workflows/release.yml` 仍然编译 `./cmd/hx-proxygroupd` —— 该目录在改名后已不存在 —— 并且上传、校验、发布的是 `hx-proxygroup_<tag>_linux_<arch>.tar.gz`。而 `scripts/package-release.sh` 产出的包名是 `hx-pxy_<tag>_linux_<arch>.tar.gz`。两者对不上, 所以下一个 tag 会在 `go build` 一步直接失败; 即便编译通过, `upload-artifact` 与 `gh release create` 也会因为匹配不到文件而失败。v0.12.0 是旧配置下最后一次成功发布, 改名之后还没有 tag 进来过, 因此这条链路从未被触发。
2. `install.sh` 的升级路径假定新布局已经就位: 数据在 `/var/lib/hx-pxy`, 服务账号 `hx-pxy`, 单元 `hx-pxy.service`。由 v0.12.0 装出来的机器上这些都不存在 —— 数据在 `/var/lib/hx-proxygroup`, 单元叫 `hx-proxygroup.service`, 且**旧控制面正占着 `127.0.0.1:19090`**。直接跑新版 `upgrade` 会新建一个空数据目录, 新控制面绑不上端口, readiness 失败, 安装器回滚到旧版本; 管理员看到的是"升级失败", 而他的订阅、节点与 Listener 从头到尾没有被读过。这是改名那篇 note 已经点名的"空库"风险的第二个实例, 只是这次连接管都还没写。

3. **接管的第一版实现把"停旧单元"绑在了搬迁的前置条件上, 于是一次失败会变成永久故障。** `adopt_legacy_installation` 的第一行是「新数据目录不存在且旧数据目录存在」才继续, 而 `ensure_account_and_directories` 会在同一次安装里建出新数据目录 —— 所以只要第一次安装失败过一次(比如 readiness 没过), 之后**每一次重跑都会跳过整段接管, 包括停旧单元**。旧控制面继续占着 `19090`, 新控制面每次都起不来, 安装器每次都回滚, 日志里只有一句 `installation failed readiness checks`。2026-10-13 的生产机就是这样坏的: 第一节 `install` 失败后, 后续重跑再也没有停过旧单元。同一个实现的第二个问题是回滚提示: 首次安装时 `old_target` 不存在(或就是刚装的那个版本), 它却会打印 `restoring <刚装的那个版本>` 并把同一个失败的启动再跑一遍。

两条链路的共同点是都不会在 `go build ./...` 或 `go test ./...` 里响: 前者是 YAML 里的一个字符串, 后者只在真实机器上执行。

## Decision

把改名补齐到这两条链路。

**发布**: `release.yml` 编译 `./cmd/hx-pxyd`, 并把产物前缀、artifact 名、`SHA256SUMS` 通配与 `gh release create` 的标题统一为 `hx-pxy` / `HX-Pxy`, 与 `package-release.sh` 的包名契约一致。

**升级**: `install.sh` 把接管拆成两步, 在 `install_or_upgrade` 里都排在 `ensure_account_and_directories` **之前** —— 顺序是硬要求, 否则新的空数据目录会先被建出来:

1. `stop_legacy_units()` —— **每次安装都跑, 幂等, 与"新布局是否存在"无关**: disable + stop 三个旧单元、删旧 unit 文件与旧安装器、`daemon-reload`。两代控制面都绑 `19090`, 不停旧的, 新的必然 readiness 失败;
2. `legacy_layout_pending()` 之后的搬迁 —— 门槛是「新**数据库**还不存在」而不是「新目录不存在」, 新旧数据目录都存在时逐项合并(已存在的条目保留新布局那一份, 并告警), 然后把 `hx-proxygroup.db`(连同 `-wal` / `-shm`) 改名为 `hx-pxy.db`; 配置、日志、安装根同样只在目标不存在时搬;
3. 服务账号与组 `hx-proxygroup` 就地改名为 `hx-pxy`(单元按名字引用账号, 重建会换 uid, 数据就没人认领了), 再 `chown -R` 兜底; 改名失败时退回"保持原名 + 按现存名字 chown"。

第 1 步与第 2 步分开是有代价换来的: 原实现把"停旧单元"写在搬迁的第一步, 而搬迁有前置条件, 于是重跑时旧单元再也不被停 —— 见 Problem 第 3 条。

失败时安装器还要说清楚原因: `diagnose_control_plane_failure` 在控制面起不来时读它的 journal, 命中 `address already in use` 就点名 `19090` 的占用者(`ss -ltnp`), 并列出仍 active 的旧单元; 回滚只在 `old_target` 确实指向**另一个**版本时发生 —— 否则"restoring <刚装的那个版本>"是把同一个失败的启动再跑一遍, 还会打印一句不可能兑现的恢复提示。

**手工迁移**: `scripts/migrate-from-hx-proxygroup.sh` 把同一套搬迁动作独立出来, 供"不想让安装器替我决定"或安装器版本过旧时使用。它与安装器接管共用同一组旧路径常量, 因此两者对"旧布局长什么样"有同一个答案。脚本默认 `--dry-run`, 真正的写入只有 `--yes` 之后才发生; 它只搬文件、改账号名、停旧单元, 不下载也不安装任何新版本, 所以任何一步失败都能按它打印的路径手工退回。

## Alternatives considered

- **什么都不做, 让管理员手工搬目录或先 `uninstall` 再 `install`。** 最强理由: 改名是一次性事件, 受影响的部署很少, 而一段只在旧布局存在时才运行的迁移代码会永久留在安装器里, 成为没人敢删的死代码; 手工 `mv` 加 `systemctl disable` 只要几分钟, 且管理员最清楚自己的机器。否决: `sudo hx-pxy-install upgrade` 的契约就是"一条命令升到最新", README 与关于页的一键更新都按这个契约调用它; 在这条路径上留下一个会**看起来成功**的分叉, 正是改名那篇 note 判定不可接受的失败形态。而且 `uninstall` 只保留数据、不搬目录, 手工步骤的每一步都有静默搞错的余地。
- **复用控制面里已有的 `migrateLegacyDatabasePath`, 不写 bash。** 最强理由: 那段接管逻辑已经存在且有单测覆盖, 再写一遍是重复实现, 两处迟早漂移。否决: 它只处理**同一个目录内**的文件改名, 而升级要跨目录、跨服务账号、跨 systemd 单元; 更关键的是它运行的前提是新实例已经能启动并读到 `/var/lib/hx-pxy`, 而那个目录此刻还不存在 —— 顺序上它到不了。
- **两代并存: 保留旧单元与旧路径, 让新旧都能跑。** 最强理由: 兼容期最平滑, 出问题时旧单元还能手动启回来。否决: 两代控制面绑同一个端口, 物理上无法共存; 保留旧单元只会把"到底哪个在跑"变成新的排障项。接管是一次性动作, 就做成一次性的。
- **只做安装器接管, 不给独立脚本。** 最强理由: 一条路径就没有漂移, 少一份需要长期维护的 bash; 接管本来就在 `install.sh` 里, 用户跑 `install` 就够。否决: 接管排在 `install_or_upgrade` 里, 意味着**必须先拿到新版的 `install.sh`**; 而卡在旧版本的机器上, 唯一能下载的安装器就是它自己那份。独立脚本让"先搬布局、再装新版"成为可能, 代价是两条实现要一起改 —— 由 `TestMigrationScriptAndInstallerAgreeOnLegacyLayout` 用同一组旧标识符钉住。

## Consequences

- 安装器多了一段每次都会跑的"停旧单元"步骤(幂等: 单元不存在时 `systemctl` 报错被吞掉, 单位文件不存在时不删任何东西), 因此全新机器与已升级过的机器上都是 no-op; 只有搬迁那一步是 one-shot, 门槛是「新数据库不存在」。
- 搬迁改成"逐项合并"而不是整体 `mv` 之后, 目标目录已存在但为空(首次失败留下的)也能恢复; 代价是新旧同名条目会保留新布局那一份并打印告警, 也就是**不覆盖已经存在的数据** —— 宁可让管理员看到一句告警, 也不静默丢掉一方。
- 接管依赖 `mv` 的原子性 —— 同文件系统内成立, 而两代目录都在 `/var/lib` 下, 天然同盘。
- 账号与组改名走 `groupmod` / `usermod --login`(不 `--move-home`: 数据已经搬到新路径, 让 `usermod` 再搬一次只会互相打架), 失败时保留原名并按现存名字 `chown`。未做旧名 -> 新名的别名, 因此改名失败会留下一个叫 `hx-proxygroup` 的账号 —— 服务仍能跑, 但账号名与文档不一致。
- 接管只覆盖数据、配置、日志、安装根、数据库文件名与账号, 不覆盖 `/etc/systemd/system` 里用户自行加的 drop-in 或环境文件(`EnvironmentFile` 里若写着旧路径, 升级后需要人工改)。
- 旧安装根被整体搬走; 其中的 `current` 是相对软链, 搬迁后依然有效。
- 发布链路修好之后, 下一个 tag 才能产出可下载的 Release; 在那之前, 在线安装与关于页一键更新只会 404。

## Testing

- `TestPackageReleaseBundleContract` 钉住 `package-release.sh` 产出的包名与 `SHA256SUMS` 内容; 新增的 `TestReleaseWorkflowPublishesRenamedBundle` 钉住 `release.yml` 的编译目标、上传路径与发布路径跟它一致 —— 只有这两条合起来才能发现"发布链路没跟着改名走"。
- 新增 `TestInstallScriptAdoptsPreRenameInstallation` 钉住旧路径常量、`adopt_legacy_installation` 的存在, 以及它必须排在 `ensure_account_and_directories` 之前。
- `bash -n install.sh` 与 `go test ./...` 通过; `node .agents/skills/hx-agent-notes/scripts/verify-all.ts` 全绿。
- 新增 `TestMigrationScriptMovesPreRenameLayout`: 在临时目录里造出旧布局, 先 `--dry-run` 断言"什么都没动", 再真跑一次断言数据/配置/日志/安装根与 `-wal`/`-shm` 侧车全部就位, 最后重跑一次断言它是幂等的 no-op。
- 新增 `TestMigrationScriptAndInstallerAgreeOnLegacyLayout`: 两个实现必须提到同一组旧标识符(三个旧单元、旧数据库名、四个旧路径、旧账号名)。
- `TestRunScriptStartsFrontendAndProxiesBackend` 改为走**出厂默认**的共享入口: 把聚合 Mixed 端口钉到测试自己占的端口, 然后按真实消费方的做法读 `/nodes/<share-token>` 拿到主机、端口与凭据再拨号, 并断言成员行存的端点就是家族端点。此前它假设"创建时填的端口就是实际监听端口", 而共享入口成为默认后这句话不再成立 —— 该测试在 v0.12.0 之后就没有再进过 CI(Release 只在 tag 上触发), 所以一直没人发现。
- `bash -n install.sh`、`bash -n scripts/migrate-from-hx-proxygroup.sh` 与 `go test ./...` 通过; `node .agents/skills/hx-agent-notes/scripts/verify-all.ts` 全绿。
- 新增 `TestInstallerStopsLegacyUnitsOnEveryRun`: 钉住「停旧单元」是独立且无条件的步骤、搬迁的门槛是新数据库而不是新目录, 以及 `adopt_legacy_installation` 里停单元必须先于"是否搬迁"的判断 —— 这三条合起来才能拦住 Problem 第 3 条的复发。
- 新增 `TestInstallerOnlyRollsBackToADifferentVersion`: 旧 target 与刚装版本相同时不许回滚, 并且失败路径必须带 `diagnose_control_plane_failure`。
- 2026-10-13 在一台真实的 pre-rename 部署(数据在 `/var/lib/hx-proxygroup`, 账号 `hx-proxygroup`)上走了一遍升级。**诚实结论: 安装器自身的接管路径在这台机器上没有完成搬迁** —— 它踩的正是 Problem 第 3 条: 第一次尝试已建出新数据目录, 重跑便跳过接管, 旧单元继续占 `19090`。数据最终由人工按本 note 的路径手工迁移完成, 迁移后的库为原库大小(WAL 已回放并 checkpoint)。因此"安装器自动接管"仍然**没有**在一台干净的旧机器上从零验证过; 脚本的系统动作(`systemctl` / `groupmod` / `usermod`)依旧只被文件系统层测试覆盖。
