# Agent Note: 终端子页自带滚动容器

Status: implemented

## Problem

终端页的 `main` 在 `lg` 断点是 `lg:h-full lg:overflow-hidden` —— 也就是说**它把高度锁死且不滚动**。
于是子页(Ops / Docker)内容超出时, 磁盘表与容器清单会被裁在视口外, 而且**没法滚到**。

这是在补全 2026-09-14 那篇 note 的后半段: 那篇解决的是 xterm 挂载点的 padding 与 FitAddon 测量,
只覆盖了终端本体; 同一套高度约定在子页上没有对应用法, 于是子页成了遗漏面。

这批改动于 2026-09-19 完成后一直留在工作区未提交 (也无 note), 直到 2026-09-26 被 coverage 门禁点名。

## Decision

把「根占满可用高度 + 子页 `flex-1` 拿剩余空间 + 子页自己 `overflow-y-auto`」这套约定**补齐到所有分支**,
包括两处此前漏掉的:

1. `terminal-page.tsx` 的**未解锁分支** (`!two_factor_verified`) —— 原为 `space-y-4`,
   与已解锁分支的高度约定不一致, 同一页面两副骨架。
2. `ops-page.tsx` —— 原为 `space-y-4`, 改成 `flex min-h-0 flex-1 flex-col overflow-y-auto`。

同时给终端页根节点补上 `px-4 py-5 sm:px-6 lg:px-8 lg:py-7`: 因为 `App` 的 `main` 在终端页
不再提供 padding, 滚动容器必须自带, 否则内容贴边。

## Alternatives considered

- **什么都不做, 让未提交的改动继续躺着**: 否决 (已实测后果)。coverage 门禁把它点名为「受保护源码改动
  缺 note」, 于是 `verify-all.ts` 整体变红 —— 一个真实的门禁信号被长期当作噪声, 比改动本身更糟。
- **只提交代码, 补 `NOTE-EXEMPT.md` 豁免**: 否决。这不是机械改动, 它改变的是**跨组件的高度约定**
  (根/子页各自承担什么), 正是 note 该记的那类「为什么是这个形状」。
- **改成给 `main` 恢复统一的 padding 与滚动**: 否决。`main` 在终端页不滚动是**故意的** ——
  终端本体需要接管滚动 (`xterm` 自己管), 由 `main` 滚动会与它冲突。所以滚动必须落在子页。
- **只修 `ops-page` 不修未解锁分支**: 否决。两处是同一个约定缺失, 只修一处会让同一页面
  在两种状态下表现不同, 下次还要再查一遍。

## Consequences

终端页全部分支共享一套高度约定; 子页内容超出时可滚动而不是被裁。

这批改动此前未提交、也无 note —— 本次一并补上, 使代码与说明同源。

Docker 子页的滚动容器是表格父 div；flex min-h-0 链把剩余高度交给该容器，底部说明留在其外。
web/e2e/docker-scroll-guard.mjs 用 40 个模拟容器在 1280/900/1440/390 宽度验证滚轮、末行可见与无页面横向溢出。
