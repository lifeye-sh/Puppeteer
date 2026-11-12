# M16 备份与恢复

提供可验证、可恢复的数据保护闭环。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：平台运营。首次交付批次：W3。目录：`backend/internal/modules/backup`。

## 开发范围

- 备份配置、文件目录、手动与定时备份
- S3/SFTP/WebDAV 备份复制和保留
- 版本/校验/密钥依赖清单、备份下载与上传恢复
- 维护模式恢复编排、恢复前备份和恢复验证

## 本模块不负责

- 不直接调用其他业务模块的任意 DELETE/TRUNCATE
- 不把每日逻辑备份等同于 PITR
- 不会在恢复后自动重做历史远程任务

## 交付切片

- 首次交付：可验证的手动备份与隔离恢复。
- 后续交付：W3 定时、异地复制；生产部署前完成恢复演练。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `backup_files`
- `backup_tasks`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Backup.Create/Inspect/Copy
- Restore.Validate/Prepare/Apply/Verify
- BackupContributor.Describe/Export
- MaintenanceCoordinator（应用层实现）
- ArchiveFormat.Inspect/ValidateVersion
- Restore.Preflight/RestartRequired

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- BackupCompleted
- BackupFailed
- RestorePrepared
- RestoreCompleted

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)
- [M04 凭据与密钥](M04-vault.md)
- [M08 审计与录像](M08-audit.md)
- [M18 系统设置与产品能力](M18-system.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/backup-api.ts](../../../src/api/backup-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U28 明确备份边界与格式兼容

- 阶段：W3；协作：M04、M07、M08、M13。
- 功能要求：官方 .ntbak 含 PostgreSQL 与 CA 材料，不含录屏和 Windows 映射盘；本方案提供数据库/CA 备份及独立对象备份清单。 按 D04 使用 nt-backup/v1 的 tar+age 容器、PG自定义dump、CA/KEK和对象清单；格式与官方明确区分。
- 验收条件：备份页明确包含/排除内容；data/recordings 与 data/drive 独立恢复验证；格式未公开时不得宣称能导入官方 .ntbak，也不得把 ZIP 改扩展名冒充兼容。
- 来源：[系统备份与恢复](https://www.next-terminal.com/zh/docs/usage/backup.html)。

### U29 恢复编排与 PostgreSQL 升级

- 阶段：W3；协作：M12、M14、M18。
- 功能要求：立即/每日定时备份、保留天数、S3/SFTP/WebDAV 测试和复制；恢复前安全备份、维护锁、版本校验、重启及恢复验证；主版本升级走导出/恢复。 按 D04 在隔离数据库恢复、验证后切换；外部任务不重放，切换后写入不承诺无损回退。
- 验收条件：验证 pg_dump/pg_restore 版本矩阵；禁止直接复用不同主版本数据目录；保留旧库和回滚方案；恢复不重放 SQL/轮换任务；异地复制失败与本地备份成功分开呈现。
- 来源：[系统备份与恢复](https://www.next-terminal.com/zh/docs/usage/backup.html)、[从 PostgreSQL 16 迁移到 PostgreSQL 18](https://www.next-terminal.com/zh/docs/install/postgresql-16-to-18.html)。

协作项：U27（主责 M13）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/backup-restore.md](../implementation/backup-restore.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 备份清单明确包含与排除对象
- 隔离环境能恢复数据库、对象引用和密钥访问
- 恢复失败保留原实例或明确回退步骤
- 恢复完成后旧票据和危险待执行任务失效

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
