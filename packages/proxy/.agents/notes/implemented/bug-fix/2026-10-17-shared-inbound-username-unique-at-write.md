# Agent Note: 共享入口的用户名冲突必须在写库前拒绝, 默认值必须每次唯一

Status: implemented

## Problem

共享入口按用户名分流: `standard` 族的所有 HTTP / SOCKS / Mixed 成员共用 7890 上的
一个 Mixed listener, 编译器用 `IN-USER` 规则把每个用户名指向各自的组。因此同一 carrier
内的用户名必须唯一, 编译器也确实拒绝重复
(`internal/dataplane/mihomo/compiler_shared_inbound.go`):

```
two shared-inbound services for the standard family use the same username "hx-user"
```

但这条不变量此前只在**编译期**成立, 写库路径不检查它。前端新建表单把用户名默认成固定的
`hx-user`(`web/src/components/create-proxy-service-form.tsx`), 所以第一个用默认值建的服务
成功 (实机 `shadowmere 入口`, `/sub/45c0536f…` 导出 `http://hx-user:…@127.0.0.1:7890`),
之后**任何**不改用户名的表单新建都会:

1. 写入 listener 行;
2. 整份候选配置编译失败;
3. `listener.Create` 回滚刚写的行 (`the created listener was removed again`);
4. 调用方看到 `create proxy service failed: create listener: listener apply failed: …`,
   HTTP 语义上被归为数据面故障。

请求本身是错的 (用户名重复), 报成数据面失败会引导调用方重试而不是改请求; 默认值则让
"什么都不改直接创建" 从第二次开始必然失败。

## Decision

**一、`listener.Create` / `Update` 在写库前按 carrier family 查重。**
`assureUniqueMemberUsername` 与编译器使用**同一判据**: 只看已启用成员、只看已启用组、
按 `SharedInboundCarrierKey(owner, kind)` 分族、用户名按编译器同样 `TrimSpace` 后比较,
并排除自身 (Update)。命中返回 `ErrUsernameTaken`, HTTP 映射为
`409 shared_inbound_username_taken`, 消息点名占用者与族。

**二、编译器检查保留。** 它是唯一能覆盖迁移直接写库、以及绕过 listener service 的写入的
边界; 服务层检查只是把可预见的用户错误提前到请求边界。

**三、前端默认用户名改成每次唯一 (`hx-<8 位随机>`)。** 与同表单的密码默认值一致;
用户仍可改成想要的名字。表单路径因此不再需要先失败一次才知道规则。

## Alternatives considered

- **什么都不做 / 只依赖编译器报错。** 它最强的理由是改动面为零, 且错误信息已经点名了
  重复的用户名与族。但调用方拿到的是 502 `dataplane_apply_failed` 加一次写库回滚,
  语义把请求错误伪装成数据面故障; 默认值缺陷还让第二次创建必然失败。否决。
- **只加后端检查, 不改前端默认值。** 检查能给出正确的 409, 但用户仍要在第二次创建时
  被拒绝一次才知道默认值不可用。默认值应该开箱可用, 而不是把缺陷转换成每次都失败的操作。否决。
- **自动改写重复用户名 (mint 一个 `svc-…`)。** 这是最"无感"的方案, 且与缺凭据时自动
  签发凭据的既有行为一致。但用户名是订阅 URL 的一部分, 调用方显式给出的值被静默替换,
  会让已发布给客户端的配置与库里记录不一致。否决。
- **把查重放进 `normalize`。** 复用现有校验入口最省代码, 但 `normalize` 不访问数据库,
  且它处理不了"与其它行比较"这件事; 塞进去会把纯函数变成有 I/O 的校验。否决。

## Consequences

- 直接 `POST/PUT /api/v1/listeners` 得到 409 与可操作消息; `POST /api/v1/proxy-services`
  仍按既有契约返回 422 `proxy_service_create_failed`(proxyservice 把下层错误包装成
  `ErrCreateFailed`), 但消息里包含完整原因。
- 禁用成员、以及组被禁用的成员不占用用户名 —— 与编译器一致; 它们被启用时会再次经过
  同一检查。
- 并发创建仍有 TOCTOU 窗口: 两个请求可能同时通过预检查。最终边界仍是编译器, 最坏情况
  退化为本次修复前的行为 (整份配置被拒 + 回滚), 不会产生错误的数据面配置。

## Testing

- `internal/listener`: 新增 `TestCreateRejectsDuplicateSharedInboundUsername`、
  `TestUpdateRejectsDuplicateSharedInboundUsername`、`TestUpdateKeepsItsOwnUsername`、
  `TestSameUsernameAcrossFamiliesIsAllowed`; 既有 fixture 改为按成员名生成唯一用户名。
- `internal/api`: `TestCreateListenerMapsDuplicateUsernameToConflict` 钉住 409 +
  `shared_inbound_username_taken` 的对外契约。
- 实机证据: 运行实例中 `shadowmere 入口` 的订阅导出为
  `http://hx-user:…@127.0.0.1:7890`, 与报错中的用户名一致。
