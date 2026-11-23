# Agent Note: YAML 路由是意图层, 翻译进现有规则而不是第二套编译器

Status: implemented

## Problem

指纹浏览器不能在运行中换代理。一个环境要按目标分流, 就得在 hx-pxy 热更新规则, 而且调用方拿的是一份通用 YAML, 不是 Mihomo 规则文本, 也不是现在这份 JSON 规则文档。

直接把 YAML 交给 Mihomo, 或者再写一个编译器, 会绕开现有的校验、保存和失败回滚。浏览器走的是共享入口: 用户名决定出口。普通监听器上的 `proxy` 字段则让 Mihomo 根本不看全局规则。与此同时, 同一个共享入口上还有按用户名钉死到某个节点的身份 (gh-pool), 住宅监听器在没分到节点时必须进失败关闭组。一份给浏览器写的 YAML 不能把这三类流量一起改掉。

## Decision

`POST /api/v1/routing-rules/apply` 收 YAML 正文 (上限 64KB, 超出 413)。必须是一份文档。`TranslateYAML` 把它翻成完整的 `Config`, 再走 `Service.Update`。`Update` 的锁盖住保存、热更新和回滚。

`POST /api/v1/mcp` 是同一套操作的 MCP JSON-RPC: `apply_rules(yaml)`、`list_groups()`、`get_rules()`。工具调用进的是同一个 `ApplyYAML` / 组列表 / `Get`, 不另做校验。

一条 YAML 规则变成一个规则集, id 从 `yaml-01` 起, 优先级 10、20、30。`match` 恰好一个键。`domain_suffix` 去掉开头的点, `geoip` 大写, `network` 小写。`group` 先按 id 再按唯一的启用名称解析。

`applies_to` 是这张默认值管到的代理组。不写它, 专用监听器的 `proxy` 保持原样, 共享入口上的用户名规则也全部留在站点规则前面, 所以 `default: direct` 和 `default: reject` 不会改到别的环境。写了它, 只有这些组的普通监听器去掉 `proxy` 改走规则; 也只有这些组里未钉节点的用户名排在站点规则后面, 从而能被 YAML 分流。`default: direct` 或 `reject` 时, 这些组的用户名兜底被去掉。钉死到节点的用户名始终排在站点规则前面。组身份在生成 `IN-USER` 时记在规则结构上, 不从规则文本里切, 因为组名本身可以含有 `),`。

有 YAML `default` 时的顺序是: 住宅会话规则, 住宅监听器的 `IN-NAME` 兜底, 钉节点的 `IN-USER`, 不在 `applies_to` 里的组用户, 站点规则, `applies_to` 里且 `default: proxy` 的组用户, 这些组的专用监听器兜底, TUN, 然后才是 `applies_to` 非空且 `default: reject` 时的 tcp/udp REJECT, 最后 `MATCH,DIRECT`。没分配节点的住宅会话因此进失败关闭组, 不会先吃到 YAML 的 direct。`default` 只能是 `proxy`、`direct` 或 `reject`。能力目录把这三项收成枚举 `routing_default`, 和 `SupportedDefaults` 是同一份列表。空的 `default` 是旧 JSON 文档, 顺序和 `proxy` 字段都保持原样; 空 `default` 再配非空 `applies_to` 直接拒绝, 编译器也不会因为这份组合删掉 `proxy`。

管理页 PUT 只提交 `rule_sets`。服务在同一把锁里读出已存文档, 只换规则集。已经删除的 `applies_to` 组 id 会丢掉, 停用的组还留着, 重新启用后作用范围还在。请求里就算带了 `default` 或 `applies_to` 也不写入, 避免过期页面盖掉刚应用的 YAML。新的 YAML 文档仍然拒绝停用、重名或找不到的组。`applies_to` 的组名会去掉首尾空白。MCP 工具失败时, 只有校验错误会把原文返回给调用方, 其余记日志后回一句通用失败。

## Alternatives considered

### 让调用方继续 PUT JSON

管理页的 `PUT /api/v1/routing-rules` 只替换 `rule_sets`。`default` 和 `applies_to` 留在已存文档里, 热更新和回滚仍走原来的锁。否决把 PUT 当作浏览器的接口: 调用方拿的是 YAML, 不是这份 JSON 规则集。整份意图由 `POST /api/v1/routing-rules/apply` 替换。

### 另写一个 Mihomo YAML 编译器

issue 初稿把翻译放在 `internal/compiler`。否决: 规则文本、作用域、组名和回滚已经在 `routingrules.Compile` 和 `Service.Update` 里。

### 把 `MATCH,DIRECT` 改成可配置

否决。那一行不是“送回入站组”。入站组来自监听器的 `proxy`, 或显式的 `IN-NAME` / `IN-USER`。拒绝规则只加在这些兜底和 TUN 之后, 而且只在 `applies_to` 非空时加。

### 不区分钉节点的身份和浏览器组

把所有 `IN-USER` 都放在站点规则前面, YAML 对浏览器不生效。都放在后面, 别的环境 (ps-pool) 和 gh-pool 的流量会被站点规则抢走。现在钉节点的身份, 以及不在 `applies_to` 里的组用户, 留在站点规则前面; 只有被点名的组排在后面。钉节点与否看生成规则时记下的组 id, 不看规则文本里最后一个 `),`。

### 只接受代理组 id

调用方写的是组名。名称必须唯一且启用。

## Consequences

`process_name` 仍只能用 PUT JSON。`default` 不能写成一个组名。不写 `applies_to` 时, 站点规则碰不到已经有用户名兜底或 `proxy` 绑定的流量, 也不会追加 REJECT。空规则加 `default: proxy` 会清空已存规则。停用一个已被 `applies_to` 点名的组不会让管理页保存失败, 删掉这个组则下次保存时去掉那个 id。`GET /api/v1/proxy-groups` 仍然是组列表的 HTTP 形式, MCP 的 `list_groups` 读的是同一份。

## Testing

`yaml_test.go` 覆盖文档形状, 以及 `applies_to` 按名称、id、空白、停用、重名和缺失解析。`compiler_yaml_order_test.go` 覆盖未限定作用域时监听器仍带 `proxy` 且别的组用户在站点规则前面、`applies_to` 的兜底在站点规则之后、direct 和 reject 都只丢掉被点名的组、住宅兜底和 TUN 在拒绝规则之前、钉节点的身份在站点规则之前、组名含 `),` 时仍按组 id 排在站点规则后面, 以及空 `default` 不删 `proxy`。`service_test.go` 覆盖空 `default` 加 `applies_to` 被拒绝, 停用的组可以留在 `applies_to` 里, 已删除的 id 在规则保存时丢掉, 以及这次保存不会和 YAML 热更新重叠。`routing_rules_test.go` 覆盖 PUT 带上过期的 `default` 时仍保留原值, MCP 的 `apply_rules` 和 `tools/list`, 尾随 JSON, 超限 413, 以及未分类工具错误不把内部文本返回给调用方。
