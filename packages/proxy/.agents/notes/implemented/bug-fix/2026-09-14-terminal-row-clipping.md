# Agent Note: xterm 挂载点不能带 padding, 否则最后一行被卡片裁掉

Status: implemented

## Problem

终端页在内容变长后, **底部会有一部分被遮挡**: 最后一行文字和提示符看不见, 而终端本身"看起来"是满的,
不会出现滚动条。用户报告的就是这个现象。

根因在 FitAddon 的测量方式, 不在样式写得难看:

1. `FitAddon.proposeDimensions()` 用 `getComputedStyle(parentElement).height` 取可用高度,
   再减去 **`.xterm` 自身**的 `padding-top` / `padding-bottom`。
2. 而 `xterm.css` 从不给 `.xterm` 设 padding —— 那两个减数恒为 0。
3. Tailwind preflight 全局设了 `box-sizing: border-box`, 所以带 padding 的挂载点报出的 computed height
   **已经包含**它的 padding。那个本该扣掉的 16px 就这样漏掉了。

结果是 FitAddon 按 728px 算出 56 行, 而挂载点的**内容区**只有 712px —— 网格比可用空间高一行,
多出来的部分被外层 `section` 的 `overflow-hidden` 吃掉。

**为什么这个 bug 能活到现在**: 它只在容器高度不是行高整数倍时暴露, 而且溢出量小于一行,
所以"少了一行"看起来只是终端没排满, 不是错误。旧代码 `absolute inset-0 p-2` 的写法本身毫不起眼。

## Decision

挂载点改成 `absolute inset-2`(**去掉 padding, 用 inset 做同样 8px 的留白**)。

这样挂载点的 computed height 就是它能提供的全部空间, FitAddon 的减法算不算都不影响结果 ——
不依赖"padding 是 0"这个前提去赌 FitAddon 的实现细节, 而是让它要减的那个量本来就不存在。

同时给挂载点加 `data-terminal-surface` 测试锚点(与既有 `data-sidebar` 同一惯例),
让端到端测试能在真实页面上量出遮挡, 而不是去猜 FitAddon 的内部算术。

## Consequences

- 终端行数在每个视口下都少了 1 行 —— 这就是被裁掉的那一行, 不是功能退化。
- **副作用是好的**: 过去那 16px 视觉留白现在由 inset 提供, 观感不变。
- 同一模式(`absolute inset-0 p-*` 交给 xterm)在仓库里没有第二处; 若将来新增终端面板必须沿用 inset 写法。
- 该约束写在代码注释里, 因为它是"看起来可以随手清理"的那类东西: 把 `inset-2` 改回 `inset-0 p-2`
  会被认为只是换个写法, 而它正好是回归。

## Alternatives considered

**什么都不做, 让用户手动点「重新适配窗口尺寸」按钮。**
按钮确实存在且能临时修好, 这是它最强的理由 —— 零改动。
但它要求用户先意识到"少了一行", 而且每次布局变化(收起/展开侧栏、切标签页回来、窗口缩放)
都会重新触发; 它把控制面自己的布局缺陷转嫁成用户的例行操作。否决。

**给 `.xterm` 设 `padding: 8px` 并去掉父容器 padding, 让 FitAddon 的减法真正生效。**
这是"顺着 FitAddon 的实现去用它"的思路, 看起来最贴合它的 API 设计。
但依赖的是 addon 内部"只减自身 padding"的实现细节: 一旦 addon 换成量父容器 content-box,
或者 xterm 自己引入默认 padding, 这个平衡就无声崩掉。而且它把留白画在了终端元素内部,
滚动条和选区会跟着内缩, 视觉上不是我们想要的。否决。

**在 `fit()` 之后用 JS 把 `rows` 减 1 兜底。**
能立刻消掉症状, 且不碰任何 CSS。
但它是在猜"溢出一定小于一行": 行高一变(字号、缩放、不同等宽字体)就不准,
而且把它写死在 `fit` 之后, 会让下一个读代码的人以为行数计算本来就该补一。否决。

**用 `ResizeObserver` 每次都重新 fit 直到稳定。**
真实的 resize 处理已经在做(容器变化即 refit), 再加一层"反复 fit 到收敛"只会掩盖测量口径错误,
并引入难以预测的重排。根因不是"没重新量", 而是"量错了"。否决。
