# Agent Note: 新建代理服务默认不筛选地区

Status: implemented

## Problem

新建代理服务的表单里 `selectedRegions` 初值是 `["jp"]`。提交时只要非空就把
`regions` 发给后端, 而后端按 `len(spec.Regions) > 0` 决定是否加地区谓词。

于是用户勾了一个订阅、什么都没选地区, 系统却**替他加了"只要日本"这个条件**。
实测: shadowmere 订阅 172 个节点**全部是 `🇨🇦 Canada`** / `🇨🇦 QC, Canada`,
`matchesRegion(name, "jp")` 一个都不命中 → 预计命中 0 → 建成空组, 而表单仍允许
提交 (`validSource` 只看 `selectedSubscriptions.length > 0`)。

用户原话: 「为什么我用订阅他就一定要我指定使用地区呢？」

## Decision

默认 `[]` = **不限地区**。不新增"全部地区"选项 —— 那会引入一个与空集同义的
第二种表示, 而本仓已多次栽在"同一事实两处判据"上。

空数组在提交时被 `selectedRegions.length > 0` 判为假, 于是 `regions` 字段根本不发,
后端不加谓词, 语义天然一致。

## Consequences

用户勾订阅后若不主动选地区, 组里是该订阅的全部节点 (shadowmere 即 172 个),
不再静默变成空组。

## Alternatives considered

- **保留 `["jp"]` 但加提示**: 否决。默认值就是"系统替你做的决定", 提示改变不了
  用户没选却生效这个事实。
- **加一个"全部地区" chip**: 否决。与空集同义, 是同一事实的第二种表示。
- **在后端把空 regions 解释成 jp**: 否决。那是把错误默认值藏到更远的地方。
