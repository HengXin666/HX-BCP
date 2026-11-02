# Agent Note: HX-Browser 检测站对照

Status: implemented

## Problem

用户要求给 hx-browser 的变更做质量评分，分数必须来自公开检测站自己写在页面上的结论，不能是一份要跟着源码维护的静态规则表。同一组站要在 GitHub 工作流的虚拟桌面上对照两排：本机 Google Chrome 与 Firefox（火狐）的有头和无头，以及 hx-browser 每个内核的有头和无头。

火绒是安全软件，不是浏览内核，不能当一列。

HX-Jungle 是私有仓库，Actions 额度不够撑 18 个浏览器格子。

## Decision

门禁代码放在 `services/hx-browser/gate/`，不进既有质量总线。行数棘轮另外记了 main 上已经超标、但不是这次改出来的 7 个文件的当前行数，并重算了质量指纹。不拆那些文件。

抽取器读页面原文，分数是站点自己的结论。有一站失败就阻断；抽不到结论不进平均，门禁也不放行；Canvas 只记录。每一站测什么写在 `sites.ts` 的 `measures` / `reads` / `passes`。

对照矩阵不在私有仓跑。流水线在公开仓 HengXin666/HX-Workflows 的 `.github/workflows/hx-browser-sites.yml`，脚本副本在该仓 `hx-browser-gate/`。公开仓的标准 runner 不占私有额度。有头和无头都套在 Xvfb 里。本机谷歌和火狐直接启动，不走指纹。hx 格子复用已有 secret `HYGIENE_TOKEN`（与仓库卫生扫描相同）做稀疏克隆，只取 `services/hx-browser`。令牌放在 git 的 `http.extraheader`，不写进克隆地址。没有这个 secret，或 runner 上没有那个二进制，格子写未安装，不当 0 分。

抽取器单测不打开网站，跟矩阵一起在公开仓跑。

## Alternatives considered

- **静态扫描源码里的指纹钩子** — 站点改了判据，规则表不会跟。用户明确否决。
- **把实测挂进既有质量总线** — 总线每个 PR 都跑，而且它的指纹棘轮会把新门禁当成未承认的阈值变动。单独一条 workflow。
- **把 18 个格子留在私有仓** — 额度不够。改到公开仓 HX-Workflows。
- **用本机无头 Chromium 的分数当产品分** — 那不是 hx-browser。对照列和产品列分开写。
- **runner 上缺内核就让任务失败** — CloakBrowser、Edge、Brave、LibreWolf 不一定在 GitHub 的镜像里。缺了写未安装，不把缺失读成 0 分。

## Consequences

一次对照是 18 个格子，每格打开 9 个外部站，分钟数记在公开仓上。站点改版、限流或扫描没跑完，格子是未测，不是产品回归。没有 `HYGIENE_TOKEN` 时，只有本机谷歌和火狐有数。

## Testing

- `node --experimental-strip-types --test hx-browser-gate/engine.test.ts`（公开仓）
- 工作流 `hx-browser site gate` 在 HX-Workflows：`readers` 跑抽取器，`cells` 在 Xvfb 上跑矩阵，`comparison` 收成 `site-comparison` 产物。
