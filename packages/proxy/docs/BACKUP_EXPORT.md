# 备份、恢复与导出

## 1. 两类 Artifact

HX-Pxy 将“备份”和“导出”定义为两个不同用途：

| 类型 | 用途 | 默认内容 | 兼容性 |
| --- | --- | --- | --- |
| Backup | 当前实例故障恢复 | 控制面状态、事务一致数据库快照、运行配置和可选秘密 | 允许依赖当前实例版本与本机密钥 |
| Export | 跨实例迁移、审阅和版本管理 | 可移植 Desired State，不包含秘密和运行时缓存 | 必须具有明确 Schema Version 与迁移逻辑 |

当前里程碑已实现通用 Artifact 管理、非敏感归档和 SQLite Online Backup。加密完整备份、恢复和导入仍待实现。

## 2. 当前已实现

- [x] 创建 Backup / Export Artifact。
- [x] 列表和读取 Artifact 元数据。
- [x] 下载 Artifact。
- [x] 删除 Artifact 及其元数据。
- [x] 校验 Artifact 文件 SHA-256。
- [x] 校验归档内 Manifest、文件数量、大小和逐文件 SHA-256。
- [x] 使用临时文件、`fsync` 和原子重命名发布。
- [x] Artifact 与元数据权限均为 `0600`，目录权限为 `0700`。
- [x] 归档拒绝符号链接和路径穿越。
- [x] 默认跳过被标记为 Sensitive 的来源。
- [x] 未配置加密器时拒绝 `include_secrets=true`。
- [x] 使用 SQLite Online Backup API 生成事务一致数据库快照。
- [x] 在线快照完成后执行数据库完整性与 Schema Version 校验。

当前控制面启动后会生成非敏感的 `control-plane.json`，Backup 与 Export 均可归档该状态。Backup 额外包含事务一致的 SQLite 数据库快照；Export 不包含数据库。运行时 Mihomo 配置、原始订阅快照、应用秘密和本机主密钥仍被标记为 Sensitive，不进入明文归档。

## 3. API

### 3.1 Backup

```text
POST   /api/v1/backups
GET    /api/v1/backups
GET    /api/v1/backups/{id}
GET    /api/v1/backups/{id}/download
POST   /api/v1/backups/{id}/verify
DELETE /api/v1/backups/{id}
```

创建请求：

```json
{
  "description": "before upgrade",
  "include_secrets": false
}
```

### 3.2 Export

```text
POST   /api/v1/exports
GET    /api/v1/exports
GET    /api/v1/exports/{id}
GET    /api/v1/exports/{id}/download
POST   /api/v1/exports/{id}/verify
DELETE /api/v1/exports/{id}
```

API 默认仅允许控制面绑定到环回地址。在管理员认证完成前，程序会拒绝绑定公网地址。

## 4. 归档结构

```text
backup-<id>.tar.gz
├── payload/
│   ├── control-plane-state/
│   └── database/
│       └── hx-pxy.db
└── manifest.json
```

数据库文件由 SQLite Online Backup API 从活跃 WAL 数据库生成，不是文件系统直接复制。Portable Export 仍不包含该数据库快照。

Manifest 示例字段：

```json
{
  "schema_version": 1,
  "application": "HX-Pxy",
  "application_version": "dev",
  "kind": "backup",
  "created_at": "2026-07-25T12:00:00Z",
  "includes_secrets": false,
  "entries": [
    {
      "path": "payload/control-plane-state/control-plane.json",
      "size": 128,
      "mode": 384,
      "mod_time": "2026-07-25T12:00:00Z",
      "sha256": "..."
    }
  ],
  "skipped": []
}
```

Artifact 目录中还存在单独的 `<filename>.meta.json`，用于快速列表和下载校验。元数据不能替代归档内 Manifest；两者必须相互一致。

## 5. 安全语义

### 5.1 非敏感归档

当前默认模式：

- 数据库只有在秘密字段已加密存储后才可作为普通 Backup 来源。
- 主密钥不进入普通 Backup。
- Mihomo 运行配置和原始订阅快照可能包含上游凭据，因此默认跳过。
- Portable Export 不能包含上游节点密码、管理员 Session、SMTP 密码或订阅 Token。

因此，当前 Backup 可以恢复 SQLite Desired State，但前提是目标实例仍保有相同主密钥。它尚不能单独承担跨服务器完整灾难恢复。

### 5.2 完整加密备份

后续完整备份必须先实现以下任一加密方式：

1. 使用接收方公钥生成仅接收方可解密的归档。
2. 使用用户提供的备份密码，通过成熟 KDF 和 AEAD 生成加密归档。

禁止自行设计密码学格式。加密层必须携带版本、KDF 参数、随机 Salt、Nonce、算法标识和认证数据。

## 6. SQLite 备份要求

接入 SQLite 后，不允许直接复制正在写入的 `.db` 文件作为最终实现。必须：

- 使用 SQLite Online Backup API 或等价一致性快照。
- 在快照完成后执行完整性检查。
- 记录数据库 Schema Version、应用版本和快照时间。
- 明确处理 WAL 和 checkpoint。
- 备份失败不能影响代理数据面。

## 7. 恢复流程

恢复功能必须分为两个阶段：

### 7.1 Preflight

- 校验 Artifact SHA-256 和 Manifest。
- 校验应用版本、数据库 Schema Version 和数据面能力。
- 检查目标路径、磁盘空间和权限。
- 识别将被覆盖的资源并生成 Diff。
- 检查端口冲突、外部证书路径和不可移植引用。

### 7.2 Apply

- 暂停配置写入和后台任务。
- 创建恢复前备份。
- 将恢复内容写入候选目录。
- 校验数据库与候选数据面配置。
- 原子切换状态。
- 启动并验证控制面与数据面。
- 失败时恢复恢复前备份。

恢复不得直接从 HTTP 上传内容覆盖生产数据库。

## 8. 后续清单

- [x] SQLite Online Backup Provider。
- [ ] 加密 Artifact Wrapper。
- [ ] Backup 保留策略和磁盘空间预算。
- [ ] 定时 Backup Scheduler。
- [ ] Restore Preflight API。
- [ ] Restore Apply 与自动回滚。
- [ ] Portable Export Schema。
- [ ] Portable Import 与版本迁移。
- [ ] 代理组 Clash / V2RayN / sing-box 客户端订阅导出。
