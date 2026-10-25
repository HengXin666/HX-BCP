# fingerprint-audit

环境质量自检: 输出红/黄/绿与逐字段证据, 退出码可当闸门。

> 把一个浏览器环境「能不能拿去用」变成一条可重复执行、有明确红黄绿的红线,
> 而不是靠感觉或事后翻车。对**任意** CDP 可达的环境都适用。

## 为什么需要它

本工作区反复栽在同一件事上: 环境看起来正常, 上线才发现被识别。具体教训:

- **2026-09-14**: 改指纹时才发现「换 seed 其实没换指纹」—— 3 个不同 seed 的
  `platform/UA/cores/screen/WebGL` **6/7 完全相同**, 因为启动器硬编码了
  `--fingerprint-platform=windows`。若早有本工具, 这条会在第一次改动时就被抓到。
- **2026-09-12**: pixelscan 对照实测发现「时区一致性」是真风险, 而
  「Masking detected」连干净系统 Chrome 都报 —— **没有区分力的信号会制造狼来了**,
  红线必须是筛过的。

## 与 `antibot/` 的区别 (别混用)

| | `antibot/` | `fp-audit/` (本目录) |
|---|---|---|
| 定位 | **攻击**: 协议重放 / 靶场调试 | **质检**: 环境自检红线 |
| 回答 | 「怎么过这个检测」 | 「我这环境现在能不能用」 |
| 输出 | 攻击 payload / 案例 | 红黄绿报告 + 修复动作 |
| 归属 | `antibot-protocol-replay` skill | 本目录独立 |

两者互补: 靶场用来研究检测**怎么工作**, fp-audit 用来保证**自己的环境**达标。

## 用法

```bash
# ① 质检一个已在运行的环境 (最常用)
uv run fp-audit --endpoint http://127.0.0.1:43020

# ② 顺带拉起一个环境再质检 (headless, 不占桌面)
uv run fp-audit --launch headless --profile /tmp/fp-test --proxy http://127.0.0.1:7892

# ③ 指定期望时区 (按出口国家给, 这是最有区分力的一条)
uv run fp-audit --endpoint http://127.0.0.1:43020 --expect-tz Asia/Tokyo

# ③b 拉起时**直接对齐**时区 (不只比对, 真的传进去)
uv run fp-audit --launch headless --expect-tz Asia/Tokyo --fix-tz

# ④ 只跑一站 / 机器可读
uv run fp-audit --endpoint http://127.0.0.1:43020 --only creepjs --json
```

退出码: `0` = 绿或黄 (可用), `1` = 有红线 (不可用), `2` = 端点不可达。
→ 可直接串进任何流水线当闸门。

## 四个站各查什么

| 站 | 用途 | 纳入红线的读数 |
|---|---|---|
| [creepjs](https://abrahamjuliot.github.io/creepjs/) | 综合指纹 + 自动化/谎言检测 | `bot` / `headless` / `webdriver` / `lies` / `trust score` |
| [browserleaks](https://browserleaks.com/) | 分项泄漏 (IP/时区/WebRTC) | JS 时区、出口 IP |
| [amiunique](https://amiunique.org/) | 指纹唯一性统计 | **不参与判定** (唯一性是隐私指标) |
| [pixelscan](https://pixelscan.net/) | **跨信号一致性** | `consistent/inconsistent` / `timezone spoofed` / `User Type by IP` |

## 红线规则 (rules.py)

| 编号 | 判定 | 级别 | 修复方向 |
|---|---|---|---|
| R1 | creepjs 报自动化痕迹 | 🔴 | 用 headed/xvfb, 查启动参数 |
| R2 | creepjs 报 lies (指纹自相矛盾) | 🔴 | 所有字段同源派生 |
| R3 | trust score <30 / 30-70 | 🔴/🟡 | 逐项修复带动综合分 |
| R4 | 时区与出口不一致 | 🔴 | `--fingerprint-timezone` + `TZ` 双保险 |
| R5 | 时区未对齐预期 | 🔴 | 按出口国家重设 |
| R6 | 出口被标为机房 | 🟡 | 属出口属性, 与浏览器无关 |
| R7 | 出口被标为代理 | 🟡 | 同上 |
| R8 | 无自动化行为 | 🟢 | 好信号, 说明行为模型没抓到 |
| R9 | 某站没取到读数 | ⚪ | **不等于通过**, 需人工看 evidence |
| R10 | 指纹字段缺失 (WebGL 为空等) | 🔴 | 真实浏览器不应为空; 查 GPU/无头模式 |

### 两条铁律

1. **无区分力的信号永不纳入红线**。`NOISE_SIGNALS` 是显式常量而不是"不写就行",
   为的是挡住日后有人"顺手"把它加回来。已实测反例: `Masking detected` 对干净
   系统 Chrome 也报 (依据 `antibot/pixelscan/RESEARCH-2026-09-12.md`)。
2. **探针 `unknown` 不等于通过**。抓不到就读不到, 绝不放行 —— 否则站点一改版,
   红线就静默失效, 比没有红线更危险。

## 指纹多样性守卫 (diversity.py)

> 专治 2026-09-14 那次事故: **「换 seed 没换指纹」**。

事故现场: 3 个不同 seed 的 `platform/UA/cores/screen/WebGL` **6/7 字段完全相同**,
根因是启动器硬编码了 `--fingerprint-platform=windows`,`--fingerprint=<seed>` 只改
canvas 与 WebGL renderer。于是"每轮换新指纹"名不副实 —— 而这一点**只靠看代码看不出来**。

```bash
# 用 3 个不同 seed 各起一个环境, 采样并两两比对
uv run diversity.py --seeds 44444,55555,66666 --proxy http://127.0.0.1:7892

# 把采样存入基线 (供日后检查"某账号指纹是否被动过")
uv run diversity.py --seeds 44444 --save-baseline
```

退出码: `0` = 关键维度确实变化, `1` = **没变化 (红线, 与事故同型)**, `2` = 采样失败。

### 判据 (D1/D2)

| 编号 | 判定 | 级别 |
|---|---|---|
| D1 | 两个 seed 的**关键维度全部相同** | 🔴 没换指纹 |
| D1 | 关键维度有变化 | 🟢 |
| D2 | 整体变化面 <20% | 🟡 可能只有少数项在变 |

关键维度 = `platform / ua / cores / screen / gpuVendor / gpuRenderer / timezone`
—— 取自事故实测的那批字段。

### 一条重要的设计原则

采样是**从浏览器实际读值**, 不是读启动参数。因为"传了参数" ≠ "生效了" ——
事故的根因正是参数传了但没生效。**守卫必须观测结果, 不能信任输入。**

### 能力边界 (设计上的诚实, 不是缺陷)

只能验证**本工具采集到的维度**。fp-audit 通过 CDP 在页面里读, 所以看得到
navigator/screen/WebGL/时区等, 但**看不到** TLS 指纹、HTTP/2 帧序、字体逐字节顺序
等非 JS 维度。所以"守卫通过" ≠ "指纹完全隔离", 只等于**"JS 可见维度上确实换了"**。
要覆盖非 JS 维度需另用抓包/端侧方案。

## 目录

```
fp-audit/
├── audit.py             CLI 入口 (拉起/探测/出报告)
├── diversity.py         指纹多样性守卫 (证明不同 seed 真的不同)
├── rules.py             红线规则引擎 (红黄绿 + 修复动作)
├── probes/
│   ├── cdp.py           极简 CDP 驱动 (不引入自动化框架, 避免自身注入污染被测对象)
│   ├── sites.py         四个站的探针 (只采集, 不下结论)
│   └── diversity.py     指纹采样与比对 (从浏览器实际读值)
├── reports/             结构化 JSON 结果 (供回归比对)
└── evidence/            站点原始文本 (排查改版用)
```

## 后续 (待办)

- [ ] 接入 gh-registrar 的 `ensure_signup_ready`: 出口体检后自动跑一次红线,
      红灯直接换点 —— 把"事后发现"变成"事前拦截"。
- [ ] 建立基线: 第一次跑出的报告存为 `reports/BASELINE.json`, 之后比对**指纹字段
      是否真的变了** (这正是 2026-09-14 那个"换 seed 没换指纹"缺陷的自动守卫)。
- [ ] `--json` 输出接进 CI/预提交, 环境不达标就不许开注册任务。
