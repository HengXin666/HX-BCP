# fingerprint-insight

指纹画像与对比, 用于解释差异从哪来。

> 站点到底用了哪些指纹面、把什么值带走、在哪一行代码读的它 —— 一屏看完。
> 分析 / 自动化工程师的取证工具: **只观测, 不改写**。

## 它解决什么

写分析脚本时最贵的一步不是"兼容", 而是**不知道要兼容什么**。
站点可能读 Canvas、可能读 WebGL、可能在权限 API 上认虚拟机、可能用音频浮点误差做指纹 ——
不监控就只能靠猜, 猜错的代价是脚本在别人机器上稳、在你机器上翻车。

本工具把这件事变成一次观测: 起一个受控 Chromium, 在**文档创建之前**把全部指纹 API 挂上观测点,
然后如实报告: 每个面被读了几次 (精确值)、读了什么参数、返回了什么、**从哪个脚本的哪一行读的**。

## 与 fp-audit 的分工 (别混用)

|  | fp-audit | **fp-insight (本工具)** |
|---|---|---|
| 回答 | "我这个环境干净吗 / 会不会被认出来" | "**这个站点在采我什么**" |
| 方向 | 自检 → 红黄绿 + 修复动作 | 观测 → 指纹画像 + 调用栈 |
| 会改环境吗 | 会 (可按需对齐时区等) | **绝不**: 不伪造任何返回值 |
| 对象 | 自己的环境 | 别人的站点 |

两者互补: 先用 fp-insight 弄清对方在看什么, 再用 fp-audit 保证自己的环境在那个面过关。

## 快速开始

```bash
cd workspaces/browser-auto/fp-insight

# ① 一条命令跑完, 直接出技术画像 (不需常驻服务)
uv run fp-insight run --url https://example.com --seconds 20

# ② 常驻服务 + 实时面板 (推荐: 可以边点边看)
./run.sh start fp-insight          # 或: uv run fp-insight serve
#   面板 http://127.0.0.1:28360/    (HX-Webx 入口 /proxy/fp-insight/)

# ③ 重建某次会话的画像
uv run fp-insight report --sid s1234abcd --out report.json
```

`run` 模式会自己挑一个空闲端口起内嵌服务 (探针必须连回 WS 才能回传, 所以服务是必需品),
所以**常驻服务开着的时候也能直接用 `run`**, 不会抢端口。

## 面板怎么用

三栏, 从左到右是"我想干什么 → 现在发生了什么 → 为什么"。

- **左**: 起会话 / 导航 / 打点 / 截图 / 清存储。清存储是刻意留的 —— 它是**对照实验**的入口:
  清掉 cookie 再看采集是否变化, 能判断某个面是不是用来认人的。
- **中上 (技术画像)**: 按**精确读取次数**排序的指纹面清单。这里就是"这个站点用了什么"。
- **中下 (调用流水)**: 每一行是一次真实调用, 带参数/返回/采集位置。可按类别过滤。
- **右上 (调用详情)**: 点任意一行, 看**完整调用栈**。栈顶第一帧就是分析入口 ——
  去那个脚本那一行打断点, 是最快的路。
- **右下 (变量纠察)**: 运行期**从不变化**的数据 (cookie 长度、硬件参数、字体…)。
  这是反爬用来认人的锚点。

## 变量纠察是什么

反爬识别你靠的不是"某个 API 被调用了", 而是"某个值**稳定且唯一**"。
所以本工具对每个被观测到的值做差分: 调用 n 次, 出现过几个不同值。

- `恒定` (1 个值): 认人锚点。要改的就是这些。
- `N 变体`: 自然变化的量 (如时间戳), 一般不用于识别。

支持打点: 点「打点」会把当前**全部 API 精确计数**和这一刻一起入库,
于是能回答"点了登录按钮之后, 这个站点**新读了**哪些面" —— 这是(动作 → 采集)的因果对照。

## 它明确不做什么

- **不伪造返回值**。改值会让被测站点的行为偏离真实, 那是 fp-audit 的领域。
- **不自动接受弹窗**。静默放行本身就是一种伪装。
- **不用 Playwright 的 `page.evaluate` 读 DOM**。读到的每个属性都会经过自己的钩子,
  变量纠察立刻被自己的读数污染 —— 全部改走原始 CDP 域 (`DOM.getDocument`)。
  唯一例外是读探针自己的计数表, 它只碰自己的闭包。

## 架构

```
注入探针 (MAIN world, 文档创建前落地)
    │  Page.addScriptToEvaluateOnNewDocument  (CDP, 不经任何自动化框架包装)
    │  记录: 参数 / 返回 / 耗时 / 调用栈 / 精确计数
    ▼
WebSocket /ws/fp  ──►  Hub (SQLite, 单写线程)
                          │  队列内哨兵做"读前落盘"屏障
                          ├──► /ws/ui  实时扇出给面板
                          └──► /api/*  画像 / 纠察 / 调用栈 / 导出
```

| 文件 | 职责 |
|---|---|
| `fpinsight/inject.js` | 页内探针: 127 个观测点, 自包含、无外部依赖 |
| `fpinsight/session.py` | 受控 Chromium + 原始 CDP 动作原语 |
| `fpinsight/hub.py` | 事件中枢: 落盘 / 幂等合并 / 可见性屏障 |
| `fpinsight/app.py` | FastAPI: 探针入站 + 面板流 + 证据查询 |
| `fpinsight/cli.py` | `serve` / `run` / `report` / `tail` / `sessions` |
| `fpinsight/dashboard/` | 面板 (原生 ESM, 无构建步骤) |

## 覆盖的指纹面

| 类别 | 关键观测点 |
|---|---|
| `canvas` | toDataURL / getImageData / fillText / measureText / font |
| `webgl` | getParameter / getExtension / getShaderPrecisionFormat / readPixels / WebGPU |
| `audio` | OfflineAudioContext / DynamicsCompressor / AnalyserNode / AudioBuffer |
| `navigator` | UA / platform / hardwareConcurrency / deviceMemory / UAData 高熵值 |
| `screen` | 分辨率 / 色深 / devicePixelRatio / orientation |
| `locale` | Intl.* / 时区偏移 |
| `font` | FontFace / FontFaceSet.check |
| `permissions` | permissions.query 全名枚举 / enumerateDevices / getUserMedia |
| `webrtc` | RTCPeerConnection / createDataChannel / createOffer |
| `storage` | cookie 读写 / localStorage / sessionStorage |
| `battery` / `network` | getBattery / connection 有效类型 |
| `detect` | Function.prototype.toString / visibilityState / hasFocus (爬虫识别) |

## 已实测的坑 (改代码前先读)

1. **探针内部计时绝不能走 `performance.now()`** —— 探针自己也 hook 了它,
   直接爆栈。而爆栈发生在包装体的第一行 (try 之外), 异常逃逸到站点脚本,
   结果是**一个钩子都没装上而页面毫无报错**。必须用注入瞬间缓存的原始引用。
2. **`navigator.prototype` 是 `undefined`**。`P(navigator)` 静默装不上任何 Navigator/Screen 钩子。
   单例实例必须用 `Object.getPrototypeOf()`。
3. **探针自己的读取会污染证据**。探针做环境快照时读 `navigator.userAgent` 会命中自己的钩子,
   在证据里凭空多出一条"站点读了 UA"。解法: 注入前冻结原始值 + 中性 Proxy 视图 + 冠 `env:`/`store:` 前缀。
4. **栈里分不出探针和页面**: 注入脚本是匿名的, 与页面内联脚本同为 `<anonymous>`。
   唯一可靠切分 = 第一个带真实资源 URL 的帧。
5. **读库前必须等落盘**。事件异步回传, 不等就会读到"站点什么都没采"的空快照 ——
   这是最容易被误当成结论的假象。

## 验证

```bash
uv run python -m pytest tests -q      # 21 项: 静态不变量 + 中枢 + 真浏览器端到端 + 面板 UI
```

端到端用例跑的是真实路径 (起服务 → 起 Chromium → 加载夹具页 → 读库),
夹具 `tests/fixtures/fp-live.html` 刻意调用每一类指纹面。
