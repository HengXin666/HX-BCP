# Agent Note: 第 4 类「HTTP/SOCKS 代理列表池」不新增实体, 它就是一条 Inline 订阅 + 一个列表读取器 + 一个一步入口

Status: implemented

## Problem

用户提出的第四个类型是「代理池」: 「可能有很多那种 HTTP 或者 socket 的代理, 但是目前的问题就是怎么样才能让 AI 快速的接入他们? 我希望是能提供一个统一的入口, 然后暴露出所有的代理。」

本仓库原先覆盖三类: 本机 DIRECT、聚合订阅代理(Remote/Inline/File + 去重 + 检测 + Proxy Group)、住宅代理(供应商→渠道→声明节点四层接入)。第四类**确实没有**, 但侦察后真正的缺口不是「少一个概念」, 而是两个各自可实测的缺陷。

**缺口一: 真实导出的代理清单根本解析不了。** 代理池的现实来源是供应商导出、检测器结果、爬来的资源, 它们给的不是订阅文档, 而是「一行一个端点」的扁平清单, 并且几乎总带一层导出标注。实测(本仓上游 `HX-Jungle` 目录里的真实产物):

```text
proxytest/input_socks.txt    51 行   socks5://184.178.172.13:4145#住宅-风险81% | AS22773 - Cox Communications Inc.
proxytest/input_http.txt   3418 行   http://user:pass@104.207.50.96:3129|http
proxytest/result_http.txt  3365 行   OK|http|http://user:pass@65.111.9.15:3129|65.111.9.15
```

改动前 `nodeparse.Parse` 对这三份文件的输出分别是 **0 节点 / 51 条失败**、**0 节点 / 3418 条失败**、**0 节点 / 3365 条失败**。根因有两条, 都在 `url.Parse`: `#住宅-风险81% | AS22773` 里的 `% |` 不是合法百分号转义, 整体报 `invalid URL escape`; `:3129|http` 让端口变成 `3129|http`, 报 `invalid port after host`。而裸 `1.2.3.4:8080` 连 scheme 都没有, `Parse` 直接以 `subscription format is not supported` 拒绝整份文档。

对照: 同一批清单里**没有标注**的 `proxy.txt`(3400 行, 3365 http + 35 socks5)改动前后都能导入 3400 节点。所以这不是「格式陌生」, 而是「**标注把好行变成了坏行**」—— 消费者拿到一份真实清单, 导入结果为零, 且失败原因逐行相同, 无从下手。

**缺口二: 没有一步入口。** 已成立的边界是「控制面是配置中心, 不做转发中继、不做消费者探测调度」(见 `docs/CONSUMER_INTEGRATION_CONTRACT.md` §2 与 `.agents/notes/implemented/feature/2026-09-14-consumer-nodes-api.md`)。在这个边界下, 「统一的入口、暴露出所有的代理」这句话的落点是**既有 Listener + `/nodes/` 契约**: 一份池子收成一条订阅 → 一个 Proxy Group → 一个 mixed Listener, 消费者拿到的是**一个** `protocol=mixed` 的地址, 背后几千条代理由数据面做健康检查与故障转移。这条路**已经存在**。

真正缺的是把清单送进去的那一步: `POST /api/v1/quickstart` 只接受 `subscription_url`(要先自己搭个服务器托管清单) 或已经存在的 `subscription_ids`。手上有 3400 行清单、没有 URL 的调用方被迫先去做托管, 这与「最快接入」直接冲突。

## Decision

**代理列表池不引入任何新实体。** 它就是一条 **Inline 订阅**, 所以去重、检测、Proxy Group、Listener、`/sub/`、`/nodes/` 全部沿用, 不存在第二套节点模型。为把它做成可用, 补两处最小改动加一条文档/skill 落点:

**1. `internal/nodeparse` 认识扁平清单的两种行形态**, 分两处处理, 因为它们的**识别单位不同**:

带 scheme 的标注行(缺口一的真实文件)是**逐行**识别, 因此留在原有的 `uri-list` 路径里, 由新的 `parseProxyListShareURI` 承担: 先按 `parseURI` 原样解析, **只有解析失败**的行才按 `|` 分列、并剥离 `#` 片段重试。这个顺序是刻意的 —— `socks5://h:p#HK-01` 这类**能独立解析**的行绝不被改动, 节点名作为数据被保留; 会被弱化的只有已经解析不了的行。

裸端点(`host:port`、`host:port:user:password`)没有 scheme 可供识别, 所以**整份文档**才是识别单位, 由新的 `parseProxyList` 承担, 报 `DetectedFormat = "proxy-list"`。它排在所有其它容器格式与 base64 之后, 且要求**非空行中至少一半**真的解析成端点(`looksLikeProxyList`), 否则一份散文会被逐行报成失败, 而不是得到原本那句 `subscription format is not supported`。

一条补充规则: 纯 `uri-list` 路径**只在文档里没有裸端点行时**才被采纳。否则「和裸端点混排的带 scheme 行」会被 URI 读取器**静默跳过**(它忽略不带 `://` 的行), 整份文档因此丢掉那些端点。混排文档统一走 `parseProxyList`, 两种行各按自己的协议导入。

**裸端点默认按 HTTP 导入。** 「host:port」在 curl / HttpClient 语境里就是 HTTP 代理; 按端口号猜 SOCKS(`:1080` 是 SOCKS 还是 HTTP 监听端口?) 会错得足够频繁, 比一个写明的默认值更糟。整池要走 SOCKS 就每行带 `socks5://`, 此时走 scheme 路径, 协议由行自己声明。

**2. `POST /api/v1/quickstart` 增加 `subscription_inline`**, 与 `subscription_url`、`subscription_ids` 三者互斥。它建出的就是一条普通 Inline 订阅(同样的 AEAD 加密落盘、同样的 loader/parser/去重/刷新/导出路径), 只是清单直接从请求体进来。该端点的请求体上限从默认 64 KiB 提到与 subscriptions 端点一致的 5 MiB —— 几千条端点会超过 64 KiB, 而失败会伪装成「请求畸形」而不是「你的清单太长」。

**3. 落点是文档与 skill, 而不是新端点。** 用户明确要求「直接内化到 skill 上去, 不要再有提示词」。因此: `.agents/skills/hx-consumer-api/SKILL.md` 增加「代理池」一节(消费侧视角: 拿到那个统一入口之后怎么用、什么时候反而该拿单条、别把上千条代理硬编码进程序); `.agents/skills/hx-proxy-admin/SKILL.md` 增加 `subscription_inline` 的用法(管理侧视角: 怎么把清单送进去)。格式能力写进 `docs/SUBSCRIPTIONS.md` §2 Inline 与 `docs/SUBSCRIPTION_COMPATIBILITY.md` 的矩阵。

### The unified entry, stated precisely

消费者不需要新端点。池子配置完成后, `GET /nodes/<share-token>` 的 `nodes[]` 里是**一个**节点:

```json
{"name": "代理池", "protocol": "mixed", "host": "proxy.example.com", "port": 7890,
 "auth": {"username": "svc-...", "password": "..."}, "transport": "tcp", "browser_compatible": true}
```

`protocol=mixed` 意味着这一个地址同时讲 HTTP 代理与 SOCKS5, `browser_compatible=true` 意味着浏览器与任意 HTTP 客户端可直接拨号。这就是用户那句「统一的入口, 暴露出所有的代理」在本仓边界内的准确形态 —— **控制面不中继流量**(数据面是 Mihomo 受托进程), 它只把池子收敛成一个地址并交出凭据。

## Alternatives considered

- **什么都不做, 让消费者继续把清单粘成 Inline 订阅 / 自己解析清单。** 这是最强的对手, 而且有一半是对的: Inline 订阅确实**已经能**装下一份清单(4 MiB 上限), 一行一个 URI, 去重/检测/导出全部现成。它输在两处**实测证据**上: 其一, 真实清单里带标注的行**一行都进不来**(3400 节点 → 0), 所以「已经能做」在现实数据上不成立; 其二, 就算把标注手工清干净, 调用方仍要先自己托管清单或手工粘贴 —— 而用户要的正是「最快接入」。所以否决的是「什么都不做」, **不是** Inline 这条载体(它被采用了)。

- **新增一种来源类型 `proxy_list`(第四个 `source_type`)。** 它的最强理由是语义清晰: 「代理列表」与「订阅文档」在一个枚举里被区分为两种东西, 校验器能对 `proxy_list` 单独施加更严的行格式约束, UI 也能给不同的表单。它输在**它不改变任何行为**: 存的是同一份文本、加载走同一个 `Load`、解析出同一批节点、后面全部共用。真正的差别只有「用哪段代码读这份文本」, 而那是 `nodeparse` 的内部实现, 不是来源类型的语义。多一个枚举值等于让 `subscription_source_type` 这个**已发布给 Agent 的封闭词元**承担一个纯实现细节, 并给所有消费方多一个必须处理的分支。否决: 复用 `inline`。

- **新增独立实体(仿住宅渠道的供应商→渠道→声明节点三层)。** 这是最诱人的一条: 住宅渠道证明过「一个渠道托管 N 个节点、有自己的凭据与生命周期」这套模型可行, 代理池看起来形状相同。它输在**它引入第二套节点模型** —— 而本仓刚在 `.agents/notes/implemented/feature/2026-09-14-consumer-nodes-api.md` 里把「只有一份导出结构, `/sub/` 与 `/nodes/` 不许出现两套真相」定为契约前提。池子里的代理**不是「被声明的会话」**: 它们没有服务器端轮换、没有租约、没有 CAS 版本, 它们只是会失效的地址。给它们一套「声明」外壳, 换来的是两条并行的节点生命周期, 以及每个消费者都要问「这个节点是从订阅来的还是从渠道来的」。否决。

- **让控制面提供一个真正的中继入口(一个本地/公网端口, 池子里的代理在它后面轮换)。** 这是用户原话「统一的入口」最字面的读法, 也最省消费者的事: 消费者只连一个地址, 控制面负责挑一条活代理。它输在 `AGENTS.md` §1 的产品边界 —— 控制面**不得进入实际代理流量转发路径**, 生产进程模型固定为一个控制面加一个数据面。而且数据面(Mihomo)本来就在做这件事: 一个 mixed Listener + 一个 url-test 组, 就是这个中继入口, 只是它的所有者是数据面而不是控制面。换句话说这条方案的**结果已经被交付**, 只是交付者不是控制面。否决(改边界另说)。

- **让控制面替消费者探测代理池、挑出「能用」的节点。** 控制面确实有 Probe 体系和延迟数据, 看起来顺手。它输在与消费者契约同一条职责边界: 控制面不知道消费者要访问哪个目标站、能接受多少延迟、愿意烧多少流量, 「好节点」是目标相关的。这正是 `docs/CONSUMER_INTEGRATION_CONTRACT.md` §2 明确「不提供、也不计划提供」的那一项 —— 把探测做进控制面只会得到一个对所有消费者都不最优、却必须永久兼容的接口。池子越大, 这条越致命(3400 条逐个探测是消费者的成本决策, 不是控制面的)。否决。

- **在前端「订阅」页加一个「粘贴代理列表」入口。** 它最强的地方是零 API 变化: 现有 Inline 表单粘进去就行。它输在它**不解决缺口一**: 粘贴的清单照样因为标注而导入 0 节点, 而且前端此时给出的还是一份逐行相同的失败列表。修解析才是根因; 前端便利入口的价值在解析修好之后才存在, 而那时它只是既有 Inline 表单的一个提示文案, 不值得单独占一条交付。否决为本次范围外。

## Consequences

正面: 用户要的第四类代理池**没有引入任何新概念** —— 一份清单从「导入 0 节点」变成「一条 Inline 订阅」, 之后走的全是已有链路, 所以它天然继承去重、检测、Proxy Group、两种订阅导出与 `/nodes/` 契约。调用方在**没有 URL** 的情况下也能一步建好池子。清单格式的兼容面变成可回归的测试而不是口头约定。

代价与风险:

- **解析器放宽了。** 标注剥离只对**已经解析失败**的行生效, 所以不会改变任何原本可解析订阅的含义; 但这是「容错」而不是「规范」, 边界必须由测试钉住(见 Testing)。反过来, 一份**故意**把 `|` 放进节点名的 URI 会被弱化 —— 只有在整行原本就解析不了时才会发生, 此时原本的结果是丢弃, 所以仍是净收益。
- **裸端点默认 HTTP 是一个会咬人的默认值。** 把一份 SOCKS 清单(没有 scheme 的 `ip:port`)粘进来会得到 3400 条 HTTP 节点。缓解: 文档与两个 skill 都写明「整池要走 SOCKS 就每行带 `socks5://`」, 且 `docs/SUBSCRIPTIONS.md` 把它列为「两条规则」之一。**没有**做「按端口号猜协议」的启发式 —— 那会把一个可解释的默认值换成一条无法解释的猜测。
- **`looksLikeProxyList` 的 50% 阈值是一条新引入的判定线。** 它存在的理由是**避免**误导性错误(散文不该被逐行报成「不是端点」), 代价是一份**少量端点混大量散文**的真实清单会被拒。目前判定逻辑只有一处(`nodeparse`), 不存在两处不同步的风险 —— 但它是本仓正在推行的「判定逻辑集中一处」纪律的又一个实例, 后续若有人在 API 层再加一层预检, 那条纪律会被破坏。
- **`subscription_inline` 让 quickstart 的请求体上限从 64 KiB 提到 5 MiB。** 这放宽了该端点的输入面(仍是认证后的管理端点, 不是公开面)。它必须与 subscriptions 端点保持一致, 否则会出现「同一个清单能建订阅却不能一步建」的诡异不一致。
- **`docs/SUBSCRIPTIONS.md` 现在有一份行格式清单, `docs/SUBSCRIPTION_COMPATIBILITY.md` 有一份矩阵, skill 里有第三份。** 三处都有漂移风险。缓解: 三处都指向同一篇决策(本文件), 且实际行为由 `internal/nodeparse/proxylist_test.go` 与 `internal/subscription/proxylist_test.go` 钉住 —— 与消费契约不同, 这里**没有**逐词元门禁, 因为行格式不是冻结的对外词元, 它只是解析能力的描述。

## Testing

解析层 `internal/nodeparse/proxylist_test.go`(10 项)钉住的是**识别边界**, 而不只是成功路径: 裸端点每个都导入且默认 HTTP; `host:port:user:pass` 保住凭据; **混排文档里带 scheme 的行保持自己的协议**(把它读成 HTTP 会静默产生不通的端点); 标注行 `#住宅-风险81% | AS22773` 与检测器列 `OK|http|URL|IP` 都被还原; **能独立解析的 URI 的节点名(`#HK-01`)不被改动**; Clash YAML / 纯 uri-list / sing-box JSON 都不被 `proxy-list` 抢走; 散文(10% 端点)被拒; **坏端口被报成失败而不是变成节点**; 同一端点两次得到同一指纹。

链路层 `internal/subscription/proxylist_test.go`(5 项)走真实 SQLite + 真实 loader + 真实 parser: 一份裸清单刷新出 3 个 `candidate` 状态节点; **改动前会导入 0 个的那两种标注形态现在导入 2 个**(一个 socks5、一个 http); 重复端点去重成 1; 无端点的文档**刷新失败**而不是静默发布空池; 端点凭据在数据库文件(WAL 含)里**找不到明文**。

`internal/quickstart/service_test.go` 增加: `subscription_inline` 被建成 `SourceInline`、文档原样送达、**不带 URL**、刷新发生在建组之前、组用 `sub-1` 选成员; 互斥矩阵新增两组(`url`+`inline`、`inline`+`ids`), 且纯空白的 `inline` 不算来源。

命令与实测结果(2026-09-26, 本机 Go 1.23.11):

```bash
export PATH=/home/hx/.local/toolchains/go/bin:$PATH
go build ./... && go vet ./internal/nodeparse/ ./internal/quickstart/ ./internal/api/ ./internal/subscription/
go test ./internal/nodeparse/ ./internal/subscription/ ./internal/quickstart/ ./internal/api/ ./internal/listener/
node scripts/verify-consumer-contract.ts     # ok: 契约文档 / 渲染器 / skill 三方一致
node scripts/verify-admin-catalog.ts         # ok: 目录 / skill / 脚本三方一致
```

真实清单的端到端实测(独立探针, 用改动后的 `nodeparse` 读上游真实产物):

```text
proxy.txt(3400 行, 无标注)   format=uri-list   nodes=3400  failures=0   (改动前同样 3400, 无回归)
input_socks.txt(51 行)       format=uri-list   nodes=51    failures=0   (改动前 0 节点 / 51 失败)
input_http.txt(3418 行)      format=uri-list   nodes=3418  failures=0   (改动前 0 节点 / 3418 失败)
result_http.txt(3365 行)     format=uri-list   nodes=3365  failures=0   (改动前 0 节点 / 3365 失败)
alive_urls.txt(3400 行)      format=uri-list   nodes=3400  failures=0
裸 host:port 合成清单        format=proxy-list nodes=3     failures=0   (改动前整份被拒)
```

真实守护进程的端到端实测(`hx-proxygroupd` 监听 127.0.0.1:28477, 数据目录 `.tmp/e2e`):

```text
1) POST /api/v1/subscriptions   source_type=inline, 内联 301 行真实标注清单(51 socks5 + 250 http) -> 201
2) POST /api/v1/subscriptions/<id>/refresh
   -> 200  detected_format=uri-list  estimated_nodes=251  size=20247
3) 节点库存按协议分布: http 178 / socks5 22(查询 limit=200 截断; 251 是刷新的权威计数)
4) POST /api/v1/proxy-groups(引用该订阅) -> 201
5) POST /api/v1/listeners(mixed, 127.0.0.1:27912, 带凭据) -> 201  share_path=/sub/56727fce...
6) GET /nodes/56727fce... -> 200, 单个节点 protocol=mixed transport=tcp browser_compatible=true
7) GET /sub/56727fce...?format=uri -> 200, 两行(http + socks5, 同一端口)
8) 真实流量 —— 这就是「统一入口」的证明:
   curl -x http://svc-e2e:WRONG@127.0.0.1:27912 ...          -> 403  (凭据错被拒)
   curl -x http://svc-e2e:...@127.0.0.1:27912 http://cp.cloudflare.com/generate_204 -> 204
   curl -x 同一入口 http://ifconfig.me/ip                     -> 68.71.241.33  (池内住宅 SOCKS5 出口)
   curl --socks5-hostname 同一入口:27912 ...                  -> 204  (同一端口同时讲 SOCKS5)
```

第 8 步是关键: 出口 IP `68.71.241.33` 来自被导入清单里的 `socks5://` 住宅端点(与 `alive_urls.txt` 中的 `68.71.252.38` / `68.71.254.6` 同段), 所以它证明的不是「端口能连」, 而是**请求真的穿过池内成员出网**。

未覆盖: 没有跑前端(本次未改 `web/`); 没有在真实公网反向代理(雷池/Cloudflare)后复验 `/nodes/` 的子路径形态 —— 那条坑由 `.agents/skills/hx-consumer-api/SKILL.md` 的文字与 `scripts/verify-ai-entry.ts` 覆盖, 与本次改动无关。

## Related

- [程序内接入契约](../../../../docs/CONSUMER_INTEGRATION_CONTRACT.md) — `/nodes/` 的字段与稳定性承诺, 以及 §2 的职责边界
- [订阅管理与刷新](../../../../docs/SUBSCRIPTIONS.md) — §2 Inline 的行格式清单
- [订阅与 Provider 兼容矩阵](../../../../docs/SUBSCRIPTION_COMPATIBILITY.md) — 容器格式与代理列表池条目
- [对外程序内接入用一份冻结的节点清单契约](2026-09-14-consumer-nodes-api.md) — 本决策复用的唯一导出结构; 也是「不引入第二套节点模型」的依据
- `.agents/skills/hx-consumer-api/SKILL.md` — 消费侧: 拿到统一入口后怎么用
- `.agents/skills/hx-proxy-admin/SKILL.md` — 管理侧: `subscription_inline` 怎么送进去
