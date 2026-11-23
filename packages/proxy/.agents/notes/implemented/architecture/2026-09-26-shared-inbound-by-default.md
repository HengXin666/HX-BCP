# Agent Note: 共享入口是默认值, 独占端口不再是「不传参数就自动获得」的能力

Status: implemented

## Problem

控制面对外承诺只有**一个**入口, 但实机是**一个进程占 61 个端口**。

实测(\`ss -tlnp\`, mihomo pid 9199): 61 个 TCP 监听, 其中
- \`0.0.0.0:7890\` \`hx-in-shared-standard-mixed\` —— 唯一的对外入口;
- \`127.0.0.1:32000\` VLESS —— 住宅渠道入口;
- \`127.0.0.1:17890..17988\` 共 **59 个 mixed**, \`users=[]\` 全空。

控制面数据侧是 71 行 listener, 只有 12 行带 \`shared_inbound\` 标记, **59 行是空标记**。

根因不在编译器, 而在**默认值**: \`internal/listener\` 的 \`normalizeSharedInboundOwner("")\`
返回 \`("", nil)\` —— 空值直接放行成「独占端口」。2026-09-10 用户就决策了共享入口
(\`docs/SHARED_INBOUND.md\`), 但那条决策只落在「显式打开开关后自动迁移」这一层, 没固化成**默认**。
于是**每一个不知道共享入口的调用方**都在默默产出旧模型: 裸的管理 API \`POST /api/v1/listeners\`、
脚本、以及之后 8 天新建的功能。09-17 的 \`良友云\` 走了 quickstart(带标记), 09-18 的
\`AI-演示入口 :17890\` 走了裸 API —— 同一天, 两种模型并存。

用户对过渡方案的处置是明确的: 「我不管完全不需要过渡, 不会被外界使用, 使用也是错误的」。

## Decision

**一、默认值改成「按协议入族」。** 新增 \`DefaultSharedInboundOwnerForKind\`: 不给
\`shared_inbound\` 时, \`http\` / \`socks\` / \`mixed\` 归入 \`standard\` 族, 由唯一的 Mixed
入口按 \`IN-USER\` 分流。\`Create\` / \`Update\` 都走 \`sharedInboundOwnerFor\`, 显式标记优先。

**高级协议(vless / vmess / trojan)不进默认**, 这是**实测**结论而非偏好: 本 build 里唯一
会创建的高级协议 Listener 是住宅渠道入口(\`internal/residential/entrypoint.go\`), 它以
\`/__hx-proxy__/residential/<channel-id>\` 为 ws-path, 并携带 **per-session 的 IN-USER 路由**与
2 条住宅会话认证 —— 这些聚合入口表达不了。而创建请求里**没有任何字段能把住宅入口和普通
VLESS 服务区分开**(同为 vless、同绑环回、同为 userpass)。默认收编高级协议 = 下一个住宅渠道
**静默丢掉会话路由**。因此它们保持「不传即独占」, 显式标记仍然可用。

**二、版本化迁移 37 直接改写存量行, 不做兼容层。** 版本单调递增(36 → 37), 遵循本仓既有纪律。
改写带一个条件: **该安装确实在用共享入口**。\`per_service\` 是管理员的显式选择 ——
探针实测过不判这个条件的后果: 该模式下数据面里 **一个 Listener 都没有**
(直接监听路径按 \`owner != ""\` 跳过成员, 聚合路径又因 shared 关闭不编译)。

**三、缺凭据的成员在收敛时补发凭据。** 这是本次最容易踩空的一处, 见下。

**四、凭据修复必须在第一次编译之前跑。** 迁移只能改库(加密需要 master key, store 拿不到),
而启动路径的 \`mihomoManager.Apply\` **不走设置钩子**。实机首次重启实测到了这个缺口:
daemon 起来了、管理 API 在位, 但日志是
\`initial Mihomo apply failed ... ioa-vm-mixed has no credentials\`, 数据面 **0 个端口**。
修复: \`cmd/hx-proxygroupd/main.go\` 在 \`Apply\` 之前调 \`BackfillMemberCredentials\`
(实机日志 \`minted credentials for shared-inbound members count=58\`)。
**教训**: 迁移改库之后, 任何"编译前的修复"都必须挂在**每一条编译入口**上, 不能只挂在设置钩子上。

**五、成员行不再保留调用者给的端口。** 成员不拥有 socket(载体绑端口、成员靠用户名选中),
把调用者写来的端口留在成员行上, 会让"控制面导出的地址"又变成一个没人监听的端口 ——
正是本次要消灭的缺陷。\`convergeMemberEndpoint\` 在解析出家族后把成员行收敛到家族的入口端点。
探针实测: 不收敛时成员行是 \`port=17890\`, 收敛后是 \`7890\`, 订阅导出随之只剩 7890。

## Alternatives considered

**什么都不做, 继续让「不传参数」等于独占端口。** 它最强的理由是**兼容**: 存量调用方语义不变,
零迁移风险, 而且「要共享入口就显式标记」听起来是合理的显式性。
但默认值就是实际行为: 61 个端口证明了「显式标记」这条路没有人在走, 而每新增一个功能就多一个端口。
用户的原话是这种形态本身就是事故(「被使用就是相当于你自己被绕过了」)。否决。

**只改默认值, 不做迁移(让存量 59 行自然老去)。** 改动面最小, 且新数据从今往后是对的。
但存量行不会被任何后续操作回收 —— 它们只在被编辑时才走到新默认值, 而没人会为了这个去编辑 59 行。
结果是「新旧两种模型长期并存」, 正是文档 §3 想避免的状态。用户已明确「不需要过渡」。否决。

**迁移时把缺凭据的成员删掉 / 跳过。** 删掉最干净: 共享入口靠用户名分流, 无凭据的行本来就
无法被承载。但删的是**用户的服务定义**(分组、协议、订阅链接), 而缺凭据不代表这个服务没被使用;
跳过则留回独占端口, 等于没迁移。**实测否决**: 60 个待迁行里 **58 行是 \`auth_mode=none\`**,
删除会抹掉几乎全部服务。否决。

**让成员在无凭据时也进族, 由编译器放宽。** 最省事, 只改一处。
**实测否决**: 编译器遇到无凭据成员会直接失败 ——
\`listener "ioa-vm-mixed" has no credentials but the shared inbound routes members by username\`。
这不是「那个服务不可用」, 而是**整份配置被拒、整个数据面不 apply**(\`Manager.Apply\` 在
\`Compile\` 失败时 \`recordFailureLocked\` 并直接返回)。天真迁移会让**所有**代理一起挂。否决。

**把住宅入口也一并收进 websocket 族。** 表面上最彻底: 「一个进程一个端口」(7890 + 7891),
与用户「只应占用 1 个端口」的表述最接近。
但住宅入口承载 per-session 路由与 2 条会话认证,\`compileSharedInbounds\` 只按 \`IN-USER\` 选组、
不产生会话级路由, 收编等于静默降级住宅代理。**明确记录为已知例外**: 32000 是数据面内部端口
(仅环回), 不是对外入口, 不违反「外部不可知/不可用」。若将来要给住宅入口收敛端口, 需要先给
\`CreateRequest\` 一个能表达「这是渠道托管入口」的字段。否决。

**把默认值收在 API 层(Request 默认), 而不是服务层。** 只需改 \`internal/api\`。
但 \`proxyservice\` / \`quickstart\` / \`residential\` 也直接调用 \`listener.Create\`,
绕过 API 层的默认值就会重新长出独占端口 —— 而这正是本次缺陷的成因(调用方各写各的)。
默认值必须落在**唯一写着那一行记录的那一层**。否决。

## Consequences

- 新收到的 \`shared_inbound\` 省略 = 入 standard 族。**这是行为变更**: 任何依赖「不传参数得到独立端口」
  的调用方, 现在得到的是共享入口。高级协议不受影响。
- 迁移会**改写 58 行的凭据状态**: 这些服务在迁移后需要用户名/密码(由服务端签发并在
  \`/sub/<token>\` 里给出)。没有凭据的 \`0.0.0.0\` 绑定本来就被 \`normalize\` 拒绝,
  所以这些行都是环回绑定, 影响面限于本机消费者。
- 数据面监听端口实测 **61 → 2**(7890 + 32000), 见 \`## Testing\`。
- **测试无需改动。** 起初中段有 5 个 \`internal/dataplane/mihomo\` 测试失败
  (3 个 manager 运行时测试 + 2 个迁移集成测试), 它们钉的正是旧模型。**没有改它们**:
  把默认值收在"该安装确实启用了家族"这一条件上之后, 5 个测试全部**原样通过**
  (\`go test -run 'TestManagerRuns...|TestSharedInboundMigration...' -count=1\` → 全部 PASS)。
  这比改断言更强: 契约在旧测试的约束下依然成立, 说明旧测试钉的是"配置决定行为"
  而不是"不传参数就该独占端口"。新增的 5 个测试
  (\`TestCreateJoinsTheStandardFamilyByDefault\` 等)钉住的是新默认值本身。

## Testing

**失败模式(本轮最重要的发现) —— 默认值与存量迁移必须成对做, 且顺序敏感。**
60 个待迁行里 **58 行 \`auth_mode=none\`**。共享入口按用户名分流, 编译器遇到无凭据成员不是
跳过它, 而是**拒绝整份配置**; \`Manager.Apply\` 随之 \`recordFailureLocked\` 并直接返回。
即: 天真迁移的后果不是"那 58 个服务不可用", 而是**所有代理一起停摆**。数据面与成员凭据的
修复必须在**同一次收敛内、编译之前**完成。

离线(真实库副本, 走真实迁移 + 真实 \`EnsureSharedInbounds\` + 真实编译器):

\`\`\`
rows after migration: map[(dedicated)/vless:1 standard/mixed:62]
compiled mihomo listeners = 2
listener ports: hx-in-626c4bb160f77582@32000(vless), hx-in-shared-standard-mixed@7890(mixed)
\`\`\`

订阅导出(\`ExportByID\`, 带 \`requestHost\`): 迁入族的成员导出的是 **7890**, 不再是自身端口:

\`\`\`
member 良心云   -> host=proxy.example.com port=7890 uri=http://hx:...@proxy.example.com:7890#...
member AI-演示入口 -> host=proxy.example.com port=7890 uri=http://svc-demo:...@proxy.example.com:7890#...
\`\`\`

编译器对无凭据成员**直接拒绝整份配置**的实测(迁移前必须先补凭据的依据):

\`\`\`
COMPILE FAILED: listener "ioa-vm-mixed" has no credentials but the shared inbound
routes members by username; enable username/password authentication
\`\`\`

实机重启(经 HX-Webx \`POST /api/services/hx-proxygroup/stop|start\`, 非 root):

\`\`\`
# 迁移前: 我们托管的 mihomo(pid 9199) 监听 61 个端口
$ ss -tlnp | grep 'pid=9199' | wc -l
61

# 迁移后: 同一个进程只剩 2 个
$ ss -tlnp | grep -c 'pid=333706,'
2
$ ss -tlnp | grep 'pid=333706,' | awk '{print $4}'
127.0.0.1:32000      # 住宅渠道入口(数据面内部, 仅环回)
*:7890               # 唯一对外入口
\`\`\`

订阅导出(实机 \`/sub/<token>`): 迁移前是各自身份端口, 迁移后只剩 7890:

\`\`\`
$ curl -s "http://127.0.0.1:19090/sub/f45e...?format=uri" | head -1
http://svc-6d44...:7994...@127.0.0.1:7890#ioa-vm-mixed
\`\`\`

分流仍然按用户名落到各自的组(两个不同出口, 证明 7890 不是"谁都直连"):

\`\`\`
$ curl -x http://hx:hx-lianxinyun-20260917@127.0.0.1:7890 https://icanhazip.com
203.10.99.34          # 良心云
$ curl -x http://svc-94db...:8360...@127.0.0.1:7890 https://icanhazip.com
42.200.231.184        # gh-hk01(迁移后新签发凭据)
\`\`\`

旧的独占端口已关闭(\`curl -x http://127.0.0.1:17891\` → 连接失败, 实测退出码非 0)。
