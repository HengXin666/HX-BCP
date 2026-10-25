# Agent Note: hx-browser 子进程回收 + 运行态持久化 + CDP 接缝放开

Status: implemented

- 影响: `hx-browser/src/hx_browser/browser.py` (`_PROCS` / `_pid_alive` / `reap_finished` /
  `_start_reaper` / `_save_runtime` / `load_persisted_runtime` / `scan_orphans` /
  `cleanup_orphans` / `stop` / `stop_all` / `_spawn` / `_launch_chromium`)、
  `api.py` (`/orphans`、`/orphans/cleanup`、`start_env` 记录使用痕迹、启动接管)、
  `tests/test_lifecycle.py` (新增 11 项)

## Problem

三个症状, 一条根因链, 全都表现成"面板显示错了"而不是报错, 所以静静存在了几小时。

**症状 1: 环境永远显示"运行中"。** 5 个环境被记为运行中, 其中 **3 个的 pid 就是 3 个僵尸进程**
(`chrome <defunct>`)。判活用的是 `os.kill(pid, 0)` —— 它对僵尸**也成功**,
因为进程表项还在。实测对照: 三个 pid 全部 `kill(0)=真` 而 `/proc/<pid>/stat` 状态是 `Z`。

**症状 2: 僵尸杀不掉, 且没人回收。** `_spawn` 用 `subprocess.Popen(...)` 却只 `return proc.pid`
—— **Popen 对象被丢弃, 于是没有任何人调用 `wait()`**。子进程退出后只能由父进程 reap,
而父进程从不 reap, 于是永久滞留为 `<defunct>`。`stop()` 对它们空等满 10 秒
(`_pid_alive` 被骗 → 等循环跑满 deadline → `killpg` 对僵尸无效)。
实测: hx-browser 服务进程名下挂着 3 个 chrome 僵尸, 存活 3 小时以上。

**症状 3: 服务重启即与所有浏览器失联。** `RUNTIME: dict = {}` 是纯进程内存,
无任何持久化。实测重启后 **11 个 chrome 主进程既不被记录、也停不掉**
(`stop` 对未记录的环境直接 `return False`), 而它们仍占着内存与账号 profile。
合计 7.33 GB 常驻内存里有相当一部分是这类无人管理的浏览器。

**附带的接缝阻塞**: `_launch_chromium` 没加 `--remote-allow-origins`, 导致任何外部 CDP 客户端
(hxcp) 连接一律 `Handshake status 403 Forbidden` —— 实测直连 `43042` 复现。
而同仓 `hx-capture-proxy/src/hxcp/launcher.py:74` 加了这个 flag 并在注释里写明
"必须加, 否则 websocket 升级被拒"。同一个仓库里两套启动参数不一致。

## Decision

**四件事, 全部围绕"浏览器进程的生命周期必须由服务掌握且可跨重启重建"。**

1. **留住 Popen 并常驻回收线程。** `_PROCS: dict[str, Popen]` 保存引用;
   `reap_finished()` 非阻塞 `poll()` 回收; `_start_reaper()` 起 1 秒周期的守护线程。
   子进程只能由创建它的进程 reap —— 这件事没有替代方案, 必须留在父进程。
2. **判活看 `/proc/<pid>/stat` 的状态字段, 不只看 `kill(0)`。**
   `Z`/`X` 一律判"不活"。读不到的按存活处理 (不误杀)。
3. **运行态落盘 `data/runtime.json`**, 启动时 `load_persisted_runtime()` 接管:
   判据是 **pid 活着 + 命令行里带本环境 profile 目录** (防 pid 复用误认)。
   `scan_orphans()` 报出"在跑但无人认领"的进程, `cleanup_orphans()` 是**显式动作** ——
   不在启动路径里自动杀。
4. **`_launch_chromium` 补 `--remote-allow-origins=*`。** 服务只绑 `127.0.0.1`,
   放开 origin 校验不引入新的可达面。

另外补上 `open_count` / `last_open_at` 的写入 —— 这两个字段在 `models.py` 声明了
但**全仓无人写入**, 43 个环境恒为 0, 于是"哪个环境该清理"没有任何依据;
而清理必须依据最后使用时间 (删错 = 账号身份断裂, legacy 骨架不能动)。

## Alternatives considered

- **只在 `stop()` 里补 `proc.wait()`** (最强理由: 改动最小, 只碰一行, 直接对症"僵尸杀不掉")。
   否决: 只覆盖"用户主动停环境"这一条路径。浏览器自己崩溃、用户强杀主进程、
   服务被 `kill -9` —— 这些路径都不经过 `stop()`, 照样留僵尸。
   必须有**常驻回收**, 而不是在某个入口补一次 wait。
- **用 `os.waitpid(-1, WNOHANG)` 全局回收** (最强理由: 不用保存 Popen 引用, 一行解决)。
   否决: `-1` 会回收**本进程的任意子进程**, 而 hx-browser 服务进程下不止浏览器
   (还有 uvicorn 派生等)。误收别人的子进程会让那部分代码拿到错误的退出码。
   按 env_id 精确回收更安全。
- **不保存 Popen, 改用 pid + `os.waitpid(pid, WNOHANG)`** (最强理由: 省一个 dict,
   和现有的 pid 记账方式一致)。
   否决: pid 可能被复用; 且 `waitpid` 对非本进程的直接子进程会 `ECHILD`。
   `Popen.poll()` 是 Python 替你管好这些边界的正确抽象。
- **把 RUNTIME 直接改成"每次从 /proc 反查"** (最强理由: 消灭内存与磁盘两份真相,
   最能避免"两处判据不同步" —— 本仓栽过这个坑)。
   否决: 反查拿不到 `started_at` / `login_state` 等只有启动时才知道的信息,
   而这些是面板要展示的。改为**落盘为准 + 反推兜底**: 正常走落盘, 落盘缺失/不全时
   用 `/proc` 反推补全, 让 `RUNTIME` 始终反映现实。
   这一条是被实测逼出来的: 首次实现只读落盘, 结果独立脚本里 `RUNTIME` 为空,
   `scan_orphans` 把**正在用的 2 个环境报成 9 个孤儿** —— 用户一点"清理孤儿"
   就会杀掉正在用的环境 (含账号 profile 里的登录态)。
- **什么都不做** (最强理由: 僵尸不占 CPU/内存, 7GB 里它贡献 0; 面板显示错只影响观感)。
   否决: 僵尸不占内存, 但**它让环境无法重启** (`env.id in RUNTIME` → "已在运行"),
   且 `stop` 每次空等 10 秒; 更关键的是症状 3 —— 11 个失去管理的浏览器
   是真实的内存与 profile 占用, 且会让账号身份处于无人看管的状态。

## Consequences

- 服务启动时若接管到在跑的环境, 日志打印条数; 新增 `GET /api/v1/orphans`、
  `POST /api/v1/orphans/cleanup`。
- **路径用顶层 `/orphans` 而不是 `/environments/orphans`**: 后者被 `/environments/{env_id}`
  抢先匹配, 把 `orphans` 当 env_id → 404 "环境不存在" (实测踩到)。FastAPI 按注册顺序匹配,
  子路径里再挂固定段必然被通配吃掉。
- 关服务时 `stop_all()` 会回收所有已知环境 (含 `wait`) —— 这是设计:
  服务不该把无人管理的浏览器留在机器上。因此**正常重启后 `runtime.json` 是 `{}`**,
  这不是缺陷; 只有崩溃/强杀才会留下待接管的记录。
- 首次升级时落盘不存在, 由 `/proc` 反推补全, 因此**旧版本遗留的浏览器也能被认领**。

## Testing

```bash
cd hx-browser && uv run --no-sync python -m pytest tests/ -q     # 14 passed (含新增 11 项)
```

真机实证 (面板托管重启, 每一步都采集了实际值):

```text
[僵尸回收]  重启前 3 个 chrome <defunct> -> 重启后 0 个
[接管]      强杀服务(kill -9, 不走 stop_all) -> 浏览器存活 -> 重启后自动认领
            {"env_5dffa31f1a": pid=1999648, port=43042} | 孤儿数 0
[判活]      pid 235505/540218/846314: kill(0)=真 而 stat=Z -> _pid_alive=False
[使用痕迹]  启动一次后 open_count=1, last_open_at=1789834390483 (改前恒为 0)
[接缝]      chrome 命令行含 --remote-allow-origins=* ->
            hxcp 连 43042 成功 (改前: Handshake status 403 Forbidden)
            js-reverse-mcp 仍可挂: initialize OK v4.0.3, 25 个工具
```

结构门禁: `node .agents/skills/hx-agent-notes/scripts/verify-all.ts`。
