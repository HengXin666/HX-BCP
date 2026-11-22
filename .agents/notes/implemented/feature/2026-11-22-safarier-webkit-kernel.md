# Agent Note: Safarier 作为 webkit 族外部引擎, 内核管理页

日期: 2026-11-22 · 类别: feature

## Problem

维护者要接入 Safarier (github.com/Cd1s/safarier, macOS 原生 WebKit 多环境浏览器). 它不是 "一个二进制 + profile 目录 + CDP",
而是自带 profile 库, 代理栈 (sing-box) 与 runner 进程的应用, 对外只有 CLI (`profile/proxy/launch/agent ...`).
宿主是 Linux, Safarier 只能跑在 macOS 14+. 同时控制台对内核的管理只有状态卡上一行路径, 看不出路径来源, 谁在用, 能做什么.

## Decision

- kernels.py 增加第三族 `webkit` 与 `Kernel.mode` (binary / external); safarier 一行登记, 判据仍只在 kernels.py.
- safarier.py 适配器: 命令取 `settings.kernel_paths["safarier"]` 按 shell 规则切分, 可是本机可执行文件, 也可是
  `ssh my-mac /Applications/Safarier.app/Contents/MacOS/Safarier` 这类远程命令; 未配置时只在 macOS 上自动探测.
  每个 HX 环境对应一个 Safarier persistent profile (`Fingerprint.external_profile`, 首次启动创建并落盘),
  语言与时区跟随代理出口, 环境代理只映射 socks5 / http (`proxy add`, 密码走 stdin 不进命令行).
  启动 `launch ID --url --background`, 记录 runner pid (远程时记 `RuntimeInfo.remote_pid`, 本机不判活); 停止本机 SIGTERM, 远程经同一包装 `kill`.
- browser.py 只加两处分支 (start / _stop) 转给 safarier.py, 不碰 Chromium / Firefox 启动路径. browser.py 存量已超 500 行, 本次 +9 行, 待拆.
- 能力表 `kernels.FEATURES`: webkit 无 CDP, 实时预览, Cookie 注入, 沙箱, 指纹体检; 控制台据此置灰说明, 不假装支持.
- 新增 api_kernels.py: `GET /kernels` (含 source, note, features, envs 使用数, default, 继承环境数), `PUT /kernels/{id}/path`,
  `PUT /kernels/{id}/default` (`settings.default_kernel`), `POST /kernels/{id}/probe` (binary 跑 `--version`, safarier 跑 `agent spaces`).
  写接口不在 AGENT_PATHS, Agent 只能读.
- 控制台 "系统 / 内核" 页: 按族分组的卡片, 生效路径与来源, 可编辑路径, 检测, 设为默认, 使用数, 能力标签.

## 未做 / 已知

- Safarier 没有 CDP: 登录态扫描, 实时预览, 指纹体检对它不可用; 页面自动化走 Safarier 自带 `agent` CLI.
- 未在真实 macOS 上联调 (宿主无 Mac), 适配器按 Safarier v1.5.0 源码 ManagerCLI.swift 的命令与输出格式实现, 测试用假 CLI.
