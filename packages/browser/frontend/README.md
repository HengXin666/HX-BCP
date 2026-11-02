# 前端 (已废弃)

> **已废弃, 不要构建.** 这是沿用 local-browser 的旧管理台, 8325 现在托管 `apps/console-web` 的构建产物 (`cd apps/console-web && pnpm install && pnpm build`). `vite.config.js` 一加载就抛错, 防止有人照旧流程把老页面拷回 `src/hx_browser/web/`.

> 路径: `services/hx-browser/frontend/` · 类型: 项目

## 这是什么

前端。

## 从哪开始

- 入口: `package.json`
- 子目录: `public`, `src`

## 内容划分

文件: `index.html`, `package-lock.json`, `package.json`, `vite.config.js`

## 约定与坑

- (待补: 这个目录的已知限制、易错点、为什么这么组织)

---

本文件由 `.agents/tools/readme_forge.py` 生成骨架。**请把「约定与坑」补成实话** ——
骨架的价值在于让目录自解释, 空话没有价值。
