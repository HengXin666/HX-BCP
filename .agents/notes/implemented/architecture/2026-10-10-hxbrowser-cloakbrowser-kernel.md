# Agent Note: hx-browser 内核换为 CloakBrowser

Status: implemented

## Problem

hx-browser 一直用 `/usr/bin/google-chrome-stable` (`config.detect_chromium` 优先找它),
两个各自独立、且**都静默**的问题:

1. **browser_gate 判它违规** —— 本仓只允许 CloakBrowser (Chromix), 这是存量棘轮内的一条;
2. **Chrome 136+ 收紧、154 彻底移除 `--load-extension`** ⇒ 扩展里的整套指纹伪装
   (UA / platform / WebGL / Canvas / Audio / WebRTC) **从未生效**, 而面板显示一切正常。

第 2 条此前已用 CDP 注入 (`guard.inject_fingerprint`) 补上, 但那是在**没打补丁的**
Chromium 上硬凑: `--force-webrtc-ip-handling-policy=disable_non_proxied_udp` 在本机
Chrome 154 上四组交叉实测**零效果**, 而同参数下 CloakBrowser 146 的 7 个 STUN 全 empty。
差别在二进制打没打补丁, 不在参数。

## Decision

`config.detect_chromium()` 只返回 CloakBrowser 二进制 (`~/.cloakbrowser/chromium-*/chrome`),
**找不到时不静默退回 google-chrome** —— 静默退回正是上面两个问题的来源, 等于把故障藏起来。
`settings.json` 的 `chromium_path` 同步改指 CloakBrowser。

新模块 `cloak.py` 承担: 二进制定位、逐环境 seed、指纹派生、原生 `--fingerprint-*` 展开。
启动参数构建放在 `cloak.launch_args()`, 不塞进 `browser.py` (那文件已顶行数棘轮)。

四条不变量:

- **逐环境稳定、彼此不同**: seed 由 `env.id` 哈希并落盘 `profile/.cloak-seed`,
  同环境每次启动同一张脸; 实测三个不同 id 得到 8007/24760/69344 三张不同的脸。
- **UA 主版本取自真实二进制** (`kernel_major`, 实测 146), 不是 cloak_launch 的
  `_CHROME_MAJORS=[131..136]`。引擎是 146 而 UA 说 133 是**可直接检测**的不一致。
- **宿主同源**: 只在 Linux 上派生 linux 档。在 Linux 上声称 Windows 会让 UA/platform
  与真实字体、GPU 矛盾 (本仓 2026-09-18 实测直接被 DataDome 拦)。
- **两套指纹系统互斥, 不叠加**: 走 CloakBrowser 时**不**再走 CDP JS 注入。
  叠加必然出现"UA 说 A、WebGL 说 B", 而自相矛盾本身就是稳定指纹。

WebRTC 走原生 `--fingerprint-webrtc-ip=disable_non_proxied_udp`, 不再依赖
"命令行 WebRTC 开关是否被遵守"。

## Alternatives considered

- **什么都不做 (继续用 google-chrome-stable + CDP 注入)**: 最强理由是它**已经跑通了** ——
  CDP 注入后 A卷 9/9 通过, 换内核有回归风险。否决: ① 它违反 browser_gate, 本仓明令只允许
  CloakBrowser; ② CDP 注入是在**无补丁内核**上逐项补, 而 WebRTC 那条只能靠 JS 层拦
  (`iceServers` 清空), 属于"事后粉饰"而非"根本不发"; ③ 同一份伪装逻辑要同时维护
  扩展路径、CDP 路径、还有 settings 里的 UA, 三处必然不同步。
- **复用现有 (直接调 `cloak_launch.py`)**: 它的池子是**生产验证过**的 (所有 GitHub 账号
  都由该路径注册), 所以 `cloak.py` 直接沿用它的 `_GPU`/`_SCREENS`/`_CORES` 而不是自造。
  但不能直接复用 `cloak_launch.py` 本身: 它启动的是**一次性**浏览器进程 (带 xvfb 虚拟屏、
  按 profile 目录管理), 而 hx-browser 是常驻服务 + 46 个环境的生命周期管理,
  两者的进程模型不同; 且它的 UA 主版本硬编码 131-136, 与 146 引擎矛盾 (见上)。
  折中: 共用池子, 单独实现派生。
- **保留 google-chrome 作为 fallback**: 否决, 见上"静默退回"一条。

## Consequences

- hx-browser 现在**合规**了: browser_gate 不再新增违规, 且底层是打过补丁的浏览器。
- 指纹首次真正生效且**跨环境有差异**: 实测两个环境 UA / 核数 / 屏幕 / GPU 各不相同。
- 修掉一个派生缺陷: `cloak_launch` 的 `_GPU["linux"]` 只有 **2 条**, 实测两个环境就撞车
  (同一张 GPU 脸)。已扩到 5 条 (与 `fingerprint.WEBGL_BY_OS["Linux"]` 同一套),
  40 个 seed 抽样得 5 种 GPU / 6 种屏幕 / 4 种核数。
- **代价**: 46 个存量环境的 `user_agent` / `resolution` 字段在 CloakBrowser 路径下
  **不再生效** (改由 seed 派生)。此前这些字段也从未生效过 (扩展空转), 所以无身份断裂风险;
  但若某环境已绑定账号, 其暴露的 UA 会从"配置值"变成"派生值", 需知会使用者。
- **代价**: 非 CloakBrowser 内核仍走旧的扩展 + CDP 注入路径, 两条路径并存, 各自要维护。
- A卷 (防泄露 9 条断言) 在 CloakBrowser 内核下 9/9 通过。

## 验证

```
cd /tmp/a-exam && python3 exam.py       # 防泄露 9 条, 9/9 PASS
python3 /tmp/verify_cloak.py            # 真实命令行含 --fingerprint-*, 跨环境差异
```

## 复现 / 回滚

回滚 = 把 `services/hx-browser/data/settings.json` 的 `chromium_path` 改回
`/usr/bin/google-chrome-stable`; 代码路径会自动退回扩展 + CDP 注入分支。
