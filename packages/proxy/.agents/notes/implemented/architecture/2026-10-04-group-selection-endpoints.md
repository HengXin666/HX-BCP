# Agent Note: 组内成员查询与选点端点

Status: implemented

## Problem

控制面此前无法回答"这个组里到底有哪些节点"。`GET /api/v1/proxy-groups/<id>`
只返回 `source_spec` (订阅 id + 筛选条件), 而成员是**编译期**结果 —— 于是任何
消费者看到"一个节点被拒", 都不知道这个组其实还有 145 个成员, 只能把整组当废掉。

gh-pool 的注册链路撞上的正是这个: 出口身份映射到组, 一个节点未放行就
`failed.add(身份)`, 把整组连同其余成员一起拉黑。

第一版绕过了这里 —— 让 gh-pool 直接连 mihomo 的 unix socket 读写
`/proxies/<组>`。三个问题:

1. **绕过所有者改数据面**: 本服务生成配置、原子发布、失败回滚, 并维护"控制面
   认为的配置"。外部直接改 `now` 会让配置事实来源与运行态分叉, 下一次 apply
   可能把它覆盖, 或让两者长期不一致而无人察觉。
2. **硬编码 socket 路径**: 写死 `/home/hx/.hxpxy-runtime/mihomo.sock` —— 那是
   上一次运行的死文件 (mtime 停在昨天)。重启后路径变成
   `~/.hx-pxy-runtime/<每次运行不同的 hash>/mihomo.sock`。
3. **判据错**: 死 socket 文件照样存在, `os.path.exists` 恒真, 于是"按存在性挑"
   永远挑中死文件。症状是界面显示 `组?` + `ConnectionRefusedError: [Errno 111]`
   —— 读不到成员表, 连"能不能换节点"都显示不出来。

## Decision

新增两个端点, 由本服务 (数据面所有者) 提供:

| 端点 | 作用 |
|---|---|
| `GET /api/v1/proxy-groups/<id>/members` | 组的真实成员表 + 实时 `now` |
| `PUT /api/v1/proxy-groups/<id>/selection` | 把 manual 组指向指定成员 |

实现要点:

1. `proxygroup.Service.Members` 用 **`ResolveMembers`** 解析成员 —— 与编译器
   同一条代码路径, 所以答案不可能与数据面实际运行的东西漂移。
2. `MembersView.Selectable` 由 `strategy == "manual"` 判定, **只在这里判一次**。
   非 manual 的 `PUT` 返回 409 `group_not_selectable`, 而不是接受后静默失效
   (url-test 按延迟重选、load-balance 轮询, 手动指定会被改回去)。
3. `now` 是**运行态**, 由 API 层从 `dataplane.ReadGroup` 读实时数据面填充,
   不混进存储层。读失败不影响成员表返回 —— 空值表示"未知", 不是"没选"。
4. `SelectProxy` 早已存在 (住宅轮换在用), 这里只是把它接到 HTTP 上;
   新增 `ReadGroup` 补上只读那一半。
5. 成员名用 `hx-node-<fingerprint 前 16 位>`, 与编译进 mihomo 的代理名一致,
   因此可以原样作为选点目标回传。

## Consequences

实测 (shadowmere):

```text
GET  /members    -> strategy=manual selectable=true count=146
                    now=hx-node-923ca15d0f780f0e
PUT  /selection  -> {"ok":true,"now":"hx-node-b2938d6809c442d5"}
校验 mihomo now  -> hx-node-b2938d6809c442d5   (真的变了)
非 manual 组      -> 409 group_not_selectable
```

`go build ./...` 通过, `go test ./internal/api/... ./internal/proxygroup/...` 通过,
`gofmt -l` 零不合规。

## Alternatives considered

- **什么都不做, 让调用方自己读 socket**: 否决, 见上面问题 1-3。用户的判断是
  架构性的 ("应该通过他的api去访问, 而不是hook数据"), 不是修一个路径 bug。
- **在 Members 里重新实现一遍成员解析**: 否决。`ResolveMembers` 是编译器的
  同一条流水线, 重写一遍必然在筛选规则变更时漂移 —— 同一事实两处判据。
- **`now` 也存进组记录**: 否决。那是运行态, 存下来就变成第三处需要同步的状态;
  数据面重启后存储值会与实际不符。
- **`PUT` 对非 manual 组也接受 (只记录意愿)**: 否决。数据面会静默改回去,
  调用方会以为出口已切换 —— 本仓把这种"看起来成功其实没生效"列为必须避免的缺陷。
