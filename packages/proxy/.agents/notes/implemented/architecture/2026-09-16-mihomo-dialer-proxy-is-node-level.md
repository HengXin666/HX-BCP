# Agent Note: 组级链路代理用派生 proxy 变体实现, 而不是组上的 dialer-proxy

Status: implemented

## Problem

需求是"任意东西的任意链路代理": 让**任意代理组**把出口经由**另一个代理组**, 任意深度。

最自然的实现是在组上加一个 `dialer-proxy` 字段。**Mihomo 1.19.30 拒绝这个形状,
而且拒绝方式是最坏的一类**: 只打一条 error 日志, 退出码仍是 0, `-t` 照样报
`test is successful`。也就是说组上的 dialer-proxy 会被**静默忽略**, 链路退化成直连,
而配置校验说没问题 —— 流量在操作者以为走了链路的时候裸奔出去。

实测 (真实 Mihomo 1.19.30):

```text
level=error msg="The group [exit] with dialer-proxy configuration is not allowed,
                 please set it directly on the proxy instead"
exit code: 0        # 不是失败
test is successful  # -t 也这么说
```

同一个字段放在 **proxy (节点)** 上完全有效, 且值可以是**组名**。另外三个形状则是**硬失败**
(exit 1): 重名 proxy、proxy 名等于组名、dialer-proxy 指向不存在的名字。
所以命名方案是可正确性的一部分, 不是审美问题。

## Decision

新增 `proxy_groups.dialer_proxy_group_id` (迁移 v34, `ON DELETE RESTRICT`),
语义是"组 G 经组 D 出网"。**编译器把它展开成派生节点, 组本身不写 dialer-proxy**:

- 为 G 的每个成员节点 N 生成一份副本, 名为 `nodeProxyName(N.fingerprint) + "|via|" + G.ID`,
  内容复制 N 并加 `dialer-proxy: <D 的组名>`。
- G 的 `proxies` 引用这个派生名, 而不是原名。
- 因为 dialer 的值是**组名**, 而链式组自身也解析成它自己的派生成员, **任意深度靠组引用
  自然组合**, 不需要在一个名字上叠加多层后缀。

关键取舍与理由:

- **ON DELETE RESTRICT 而非 SET NULL。** 同库已经有一个 `fallback_target_id` 用 SET NULL,
  看起来是现成先例。但那个字段**从未被编译器读取**, 是死管道; dialer 是承载流量的关系。
  目标组消失若变成 NULL, 链路会静默变直连(不可见的路由/隐私变更); 若留着悬空 id,
  编译出的 dialer-proxy 指向不存在的名字, Mihomo 硬失败(exit 1)并拖垮整个配置发布。
  RESTRICT 让删除变成干净的 409。服务层仍先查引用, 好让操作者看到组名而不是 "constraint failed"。
- **按链式组 G 命名, 不按 dialer D 命名。** 两个组经同一个 D 出网时, 各自得到不相交的派生集合。
  共用一个派生条目技术上合法(实测一个名字可被两个组引用), 但那要求按 D 命名,
  而"同一节点经两条路径派生"会在三组三角里撞名。多几个 proxy 条目换每个组独立的
  健康检查与出口路径。
- **派生名必须是 (指纹, 组 ID) 的纯函数。** 仓库硬规则要求同输入产出字节相同的配置;
  节点名格式本就是 `hx-node-<16位hex>`, 派生名沿用前缀再加后缀。结果排序后写入,
  不依赖 map 迭代顺序。
- **`|via|` 必须在操作者组名里不可能出现。** 否则一个组可以被命名成与某个派生 proxy 同名,
  而 Mihomo 对重名是硬失败(exit 1), 整个文档发布失败。`normalize` 原来只限制长度与保留字,
  不限制字符集, 因此现在显式拒绝包含该分隔符的名字。
- **环必须在写时拒绝, 且要覆盖 dialer 边。** 成员边与 dialer 边属于同一张依赖图。原来的
  `groupEdges` 只看 `spec.GroupIDs`, 而且调用处还被 `len(spec.GroupIDs) > 0` 挡着 ——
  一个只有 `node_ids` 加 dialer 的组会**完全跳过环路检测**。现在两者都纳入, 并把 dialer
  当作引用边供删除保护使用。
- **编译期必须有兜底。** API 不是唯一的写入者(迁移、住宅物化也改组行), 而 Mihomo 对运行时
  dialer 环是**直接接受**的(实测), 静态查不出来。因此 `validateGroupDialerChains` 独立于
  服务层再走一遍: 自引用、缺失/被禁用的目标、成员∪dialer 环路。**失败必须是硬的** ——
  因为 Mihomo 对非法 dialer 是静默忽略, 控制面一旦也放过, 链路就无声退化成直连。
- **节点已有 dialer 时拒绝, 不覆盖。** 住宅路径把 dialer 烧进节点加密配置。一个节点只能有一条
  出口路径; 组链路与供应商链路同时存在时, 覆盖会**静默丢掉供应商那一跳**, 改变该通道真实的
  出口位置。所以宁可报错让操作者二选一。
- **拒绝 `include_direct` + dialer 的组合。** DIRECT 是内置项, 不可能带上 dialer,
  那个成员会真的绕过链路。静默接受等于放一条直连在"链路组"里。

## Alternatives considered

**什么都不做, 只保留住宅链式。** 现状, 零风险, 住宅链式已经能用。但需求明确是"任意东西的
任意链路", 而住宅路径只能让住宅节点挂上游, 普通订阅节点完全不能。维持现状等于承认这个需求
不做。否决。

**给 proxy-group 直接写 `dialer-proxy`。** 最自然、代码最少: 在 `compileGroup` 的 map 字面量后
加一行, 拓扑排序也已经在了。**它就是被 Mihomo 拒绝的那个形状**, 而且因为它只打日志不报错,
会让所有链路静默变成直连, 配置校验还说过 —— 比不做更危险。否决。

**复用 `fallback_target_id` 承载链路语义。** 它已在库里、已在 API 目录里、已贯穿过三层,
看起来最省事。但它**从未被任何编译器读取** —— 是死管道。把链路语义塞进去会让一个已有公开
文档的字段悄悄改变含义, 而现存消费者可能正依赖它现在的(无)效果。否决。

**把链路限制成两层。** Mihomo 本身没有层数限制(实测六层通过), 限制成两层省掉递归与深度环检测。
但"任意链路"里的"任意"就是要点, 而复杂度主要来自确定性命名与环检测, 那部分无论几层都要解决。
砍到两层并不显著省事, 却直接违背需求。否决。

**在派生命名上叠加多层后缀(模仿组的嵌套路径)。** 看似能一次表达深度。但 dialer 的值本来就是
组名, 组的成员又已经各自派生过, 再叠后缀会产出没有归属的孤儿名字, 且后缀长度随深度膨胀。
组引用已经免费给了组合性。否决。

## Consequences

- 链式从"住宅专属"变成通用能力, 普通订阅节点也能挂上游。
- 编译产物里的 proxy 条目变多(每个链式成员多一个派生副本), `status.proxy_count` 的含义
  从"节点数"变成"proxy 条目数"。Mihomo 只把它当就绪判定的布尔触发器, 未受影响。
- 住宅链路与组链路重叠时是**报错**而不是静默取其一, 所以已存在的住宅通道不会在有人配上
  组链路时悄悄改变出口。
- 组名多了一条字符集约束(`|via|`)。这是本次为安全性引入的可见限制。
- `/sub/<token>` 与 `/nodes/<token>` 不受影响: 它们渲染的是 listener 自己的入口地址,
  不读编译产物, 派生名与 dialer 不可能泄漏给消费者。

## Testing

**实证 (真实 Mihomo 1.19.30, 不是文档推断):**

- 组级 `dialer-proxy` → error 日志 + exit 0 + `test is successful`(静默忽略的证据)。
- 节点级 `dialer-proxy: <组名>` → 编译通过且**真实流量经上行跳**。
- **三跳派生链 → 真实流量逐跳验证**。三个假上游各自记录收到的下一跳:

  ```text
  hub  saw: ['CONNECT 127.0.0.1:18887']   ← hub 被要求连到 mid
  mid  saw: ['CONNECT 127.0.0.1:18889']   ← mid 被要求连到 exit
  exit saw: ['CONNECT 127.0.0.1:18080']   ← exit 被要求连到目标
  curl body: TARGET-OK
  RESULT: 3-HOP CHAIN CONFIRMED
  ```

  这同时是**泄漏探测器**: 任何一层的 dialer 被忽略, 那一层日志就会是空的而 curl 仍成功。
  三层全部有记录, 所以每一跳都真的走了链路。

**HX 自己的编译产物交给原生 Mihomo 校验通过** (`compiler_chain_live_test.go` 导出,
宿主侧 `mihomo -t` 验证): 产物含
`- {dialer-proxy: hop1, name: hx-node-…|via|g2}`, 组 `hop2` 引用该派生名, 且**没有任何组**
带 dialer-proxy, 结果为 `test is successful`。

**Go 侧测试:**

- `compiler_chain_derivation_test.go`: 派生成员与其 dialer 值、两组经同一上游时互不干扰、
  两跳组合、**字节稳定性**(同输入编译两次 YAML 相同)、已有 dialer 的节点被拒。
- `compiler_chain_live_test.go`: 真实二进制校验, 并显式断言没有
  `dialer-proxy configuration is not allowed`(因为 exit code 单独不足以证明)。
- `validateGroupDialerChains` 的单元测试: 缺失/禁用目标、自引用、两节点环、`include_direct` 组合。
- `graph_test.go`: dialer 边参与环路检测; `referencedBy` 把 dialer 当引用, 且成员兼 dialer
  只报一次。
