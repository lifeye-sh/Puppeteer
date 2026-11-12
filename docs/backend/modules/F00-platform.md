# F00 公共基础设施

提供所有业务共用的事务、任务、事件、存储、加密与可观测性机制。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：技术底座。首次交付批次：W0。目录：`backend/internal/platform`。

## 开发范围

- PostgreSQL 连接、事务上下文、迁移与测试环境
- outbox、幂等记录、带租约的任务队列及重试机制
- 对象存储、加密原语、时钟、ID、日志、指标和 tracing
- HTTP 错误映射、请求限制、健康检查与协议测试工具

## 本模块不负责

- 不包含用户、资产、授权或审批规则
- 不执行远程命令、SQL、轮换等业务副作用

## 交付切片

- 首次交付：事务、迁移、outbox/jobs、存储与测试底座。
- 后续交付：按实际负载增加扩展适配器。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `migration_history`
- `jobs`
- `job_runs`
- `outbox_events`
- `event_deliveries`
- `idempotency_records`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- UnitOfWork.Within
- Outbox.Append
- JobQueue.Enqueue/Claim/Ack
- BlobStore.Put/Get/Delete
- KeyEnvelope.Encrypt/Decrypt

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- TaskLeaseExpired（基础设施信号，由任务所属模块解释）

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- 无业务模块前置。

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- 基础设施无独立业务页面，参考任务、事务与部署设计。

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

本模块提供跨需求共用的基础机制，不拥有使用场景规则。

协作项：无独立登记项。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/backup-restore.md](../implementation/backup-restore.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 同一事务的业务写入与 outbox 同成败
- 旧 lease_epoch 的 Worker 不能提交结果
- 对象上传重复调用不生成重复对象
- 外部副作用结果不明时由业务决定是否重试

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
