# 检测站门禁

分数来自公开检测站自己写在页面上的结论，不来自一份要跟着源码改的规则表。每一站测什么，写在 [`sites.ts`](sites.ts) 的 `measures` / `reads` / `passes`。

| 站 | 测什么 | 怎样算过 |
|---|---|---|
| [CreepJS](https://abrahamjuliot.github.io/creepjs/) | 有没有说谎：改过的函数、无头、躲检测补丁 | headless 和 like headless 都低于 50% |
| [Sannysoft](https://bot.sannysoft.com/) | 像不像被脚本控制（WebDriver、插件、UA） | 失败行是 0，且没有 WebDriver |
| [Iphey](https://iphey.com/) | 这套身份像不像真人。MX 是 0–100 | 不是 Unreliable |
| [BrowserScan](https://www.browserscan.net/) | 是不是机器人。真实度高也不抵消 | Bot Detection 不是 Yes |
| [Pixelscan](https://pixelscan.net/fingerprint-check) | 显卡、时区、代理是不是同一台机器 | 扫完并且是 Consistent。没扫完不打分 |
| [Device & Browser Info](https://deviceandbrowserinfo.com/are_you_a_bot) | 直接回答是不是机器人 | 它说你不是 |
| [BrowserLeaks WebRTC](https://browserleaks.com/webrtc) | 内网地址有没有漏出 | 页面写 No Leak |
| [BrowserLeaks Canvas](https://browserleaks.com/canvas) | 把画图差别收成签名，不判断机器人 | 不打分。同环境两次签名该一样 |
| [Clearcote](https://www.clearcotelabs.com/audit) | 系统、显卡、时区、语言是否互相打架 | 页面写 Coherent |

有一站失败就阻断。抽不到结论的站不进平均，门禁也不放行。Canvas 只记录。平均低于 75 也阻断。

## 怎么跑

抽取器测试不打开网站，CI 跑的是这一条：

```bash
node --experimental-strip-types --test services/hx-browser/gate/engine.test.ts
```

实测要接到已经启动的 hx-browser 环境。不要把本机无头 Chromium 的结果当成产品分。

```bash
uv run python services/hx-browser/gate/probe.py \
    --cdp http://127.0.0.1:43000 --out /tmp/sites.json
node --experimental-strip-types services/hx-browser/gate/score.ts /tmp/sites.json /tmp/scorecard.svg
```

## 对照

对照不在私有仓 HX-Jungle 里跑，那边 Actions 额度不够。流水线在公开仓 [HX-Workflows](https://github.com/HengXin666/HX-Workflows) 的虚拟桌面（Xvfb）上开同一组检测站。两排：

1. 本机浏览器，不走指纹：Google Chrome、Firefox（火狐）。各有头、无头。火绒是安全软件，不是浏览内核，所以没有火绒这一列。
2. hx-browser 的每个内核（cloak、chrome、chromium、edge、brave、firefox、librewolf），各有头、无头。拉私有仓复用 HX-Workflows 已有的 secret `HYGIENE_TOKEN`，和仓库卫生扫描同一份凭证，不另建。只取 `services/hx-browser`。没有这个 secret，或者 runner 上没有那个二进制，格子写未安装，不算 0 分。

有头和无头都套在 Xvfb 里，这样有头不是「没有显示器就退回无头」。


