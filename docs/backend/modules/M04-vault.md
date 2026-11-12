# M04 凭据与密钥

集中保护可恢复秘密，并管理凭据从创建到轮换和退役的生命周期。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：身份与安全。首次交付批次：W1。目录：`backend/internal/modules/vault`。

## 开发范围

- 密码、私钥、口令、供应商秘密的版本化加密存储
- 凭据引用、显式解密查看和短期秘密租约
- 凭据轮换策略、候选版本、逐资产验证和补偿
- 密钥生成、公钥导出、引用冲突检测

## 本模块不负责

- 不管理用户登录密码哈希
- 定时触发由 M12，远程连接由 M05/M06
- 加密原语与对象存储由 F00，业务秘密权限由应用入口组合 M02 校验

## 交付切片

- 首次交付：秘密加密、凭据引用、短期租约与查看审计。
- 后续交付：W3 轮换状态机与逐资产补偿。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `credentials`
- `secret_versions`
- `secret_access_events`
- `rotation_policies`
- `rotation_policy_targets`
- `rotation_runs`
- `rotation_targets`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Secrets.StoreVersion/Lease/Reveal
- Credential.GetMetadata/GetPublicKey
- Rotation.Request/Reconcile
- RemoteCredentialUpdater（连接能力适配）

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- CredentialVersionActivated
- SecretRevealed
- RotationCompleted
- RotationNeedsReconcile

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)
- [M18 系统设置与产品能力](M18-system.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/credential-api.ts](../../../src/api/credential-api.ts)
- [src/api/credential-rotation-api.ts](../../../src/api/credential-rotation-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U06 凭证引用完整生命周期

- 阶段：W1；协作：M01、M03、M05。
- 功能要求：密码或加密私钥含 Passphrase；生成密钥、提取公钥、MFA 明文查看、共享凭证更新影响检查和删除引用检查。
- 验收条件：资产与 SSH 网关任何一方仍有引用时禁止删除；复制公钥不返回私钥；生成密钥不会被描述为已安装到目标 authorized_keys。
- 来源：[授权凭证](https://www.next-terminal.com/zh/docs/usage/credential.html)。

协作项：U07（主责 M05）、U09（主责 M05）、U10（主责 M06）、U21（主责 M01）、U24（主责 M01）、U26（主责 M01）、U27（主责 M13）、U28（主责 M16）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-client-agent.md](../implementation/native-client-agent.md)
- [implementation/native-rdp.md](../implementation/native-rdp.md)
- [implementation/backup-restore.md](../implementation/backup-restore.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)
- [implementation/database-auth.md](../implementation/database-auth.md)

## 验收条件

- 普通查询与日志中不存在可用秘密
- 空字符串、字段缺失、替换值有明确更新语义
- 共享凭据部分轮换成功有逐资产状态
- 目标操作结果不明时不自动覆盖或重试

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
