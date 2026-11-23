# 终端目录同步（无感、零历史污染）

> 20260910 用户决策：终端面板跟随 Shell 目录时，不得在用户会话中留下任何
> 痕迹——既不能出现在屏幕上，也不能进入命令历史。

## 1. 旧实现的问题

面板与 Shell 的目录同步靠**向 PTY 写入探测命令**：

```text
连接成功      -> 发送 "pwd\n"
回车后无法推断 -> 发送 "pwd\n"
```

后果：

1. 每一次连接、每一次无法词法解析的命令（`cd`、`pushd`、别名、未知工具）都会在
   终端里回显一行用户没有输入过的 `pwd`；
2. 这些行**会进入 `~/.bash_history` / `~/.zsh_history`**，历史被大量噪声占用；
3. 与全屏程序竞态：若 `pwd` 恰好落在 vim/less 启动之后，命令会被注入应用内部。
   旧代码用「raw-mode 命令黑名单 + canonical 状态机」缓解，但这只是补丁。

这就是用户描述的「无论切换到哪里，它总是会给我用 LS 命令，命令历史会被占用」。

## 2. 市面方案

所有成熟的网页终端都用同一套 Shell 集成协议，而不是注入命令：

| 产品 | 机制 |
| --- | --- |
| VS Code 终端 | `--init-file` 注入 shell integration 脚本，读取 OSC 633 / OSC 7 |
| WezTerm | 文档化的 shell integration，OSC 7 报告 cwd |
| Kitty | `kitty +kitten shell-integration`，OSC 7 |
| iTerm2 | Shell Integration（OSC 133/7），`iterm2_shell_integration` |
| Tabby | OSC 7 |
| ttyd | 依赖客户端自行上报 |

OSC 7 的格式是事实标准：

```text
ESC ] 7 ; file://<host><path> ST        (ST 可以是 BEL 或 ESC \)
```

本项目的实现沿用这一方案，并额外增加一层内核兜底。

## 3. 三层实现（按优先级）

### 3.1 OSC 7（首选，跨 ssh/反代都生效）

`internal/terminal/shell_integration.go` 在每次启动 Shell 时生成一个启动片段：

- **bash**：`--rcfile <generated>`；生成的 rc 先 `. "$HX_ORIGINAL_BASHRC"` 载入用户
  原有配置，再把 `hx_osc7` 前置到 `PROMPT_COMMAND`，用户自己的钩子继续运行。
- **zsh**：`ZDOTDIR` 指向生成目录，shim `.zshrc` 先载入用户原配置，再用
  `add-zsh-hook precmd` 注册。
- 其他 Shell（fish、自定义 rc）不注入：不会破坏用户的配置，由第 2 层兜底。

片段是**幂等**的：`HX_OSC7_ACTIVE` 保证重复 source 不会叠加钩子；
`PROMPT_COMMAND` 的前置只在缺少 `hx_osc7` 时发生。

### 3.2 内核 cwd（兜底，永不依赖用户配置）

控制面/root helper 拥有 PTY，直接读取 `/proc/<shell pid>/cwd`：

- 本地 PTY：`ptySession.Cwd()`；
- root helper：`helperWriter.cwd()` 在输出帧前附带 `frameCwd`，控制面 `remoteSession.Cwd()`
  读取；这样文件面板看到的是 **root Shell 的真实目录**，与控制面沙箱无关。

shell 退出但前台子进程仍存在时，回退到 `TIOCGPGRP` 取到的前台进程组目录。

### 3.3 不回退

两层都拿不到时，面板保持最后一次已知目录。**永远不会为了让面板动一下而写命令。**

## 4. 前端

`web/src/lib/terminal-cwd.ts` 只保留纯路径工具与 OSC 7 解析器（`detectOsc7Directory`、
`createOsc7Scanner`）。终端页：

- 消费服务端 `{"type":"cwd","cwd":...}` 控制帧（每 900ms 轮询变更，15s 心跳重发）；
- 同时解析 PTY 输出里的 OSC 7 作为更快的本地信号；
- 不再存在 `pwd` 探测、raw-mode 黑名单与 canonical 状态机。

## 5. 面板导航的静默

文件面板点击目录时发送的命令带一个前导空格：

```text
" cd '/path/to/dir'\n"
```

启动片段同时设置：

- bash：`HISTCONTROL=ignorespace:ignoredups`
- zsh：`HIST_IGNORE_SPACE=1`

`HISTCONTROL=ignorespace` 加上 `ignoredups`（`ignoredups` 在语法里属于
`ignorespace:ignoredups` 组合），因此面板导航**既不进入历史，也不打断重复命令折叠**。
bash 与 zsh 都会在解析命令前剥掉前导空白，命令语义不变。

## 6. 验证

```bash
go test -run TestShellIntegration -v ./internal/terminal/
cd web && npm run test:terminal-cwd
```

`TestShellIntegrationReportsOsc7WithoutPollutingHistory` 启动真实交互式 bash，
用面板的方式导航，然后断言：

- 会话输出里出现 OSC 7 且报出正确的起始目录；
- 会话输出里**没有** `pwd`；
- `~/.bash_history` 里没有面板导航命令，但有用户自己输入的命令。

## 7. 环境变量

| 变量 | 作用 |
| --- | --- |
| `HX_PXY_SHELL_INTEGRATION=0` | 关闭启动片段注入（内核 cwd 仍然生效） |
| `HX_PXY_RUNTIME_DIR` | 生成片段与所有运行时文件所在目录 |
