# M11 申请与审批

管理临时访问和 SQL 工单的申请、审核、取消与执行跟踪。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：流程与自动化。首次交付批次：W3。目录：`backend/internal/modules/approval`。

## 开发范围

- 可申请资源范围、临时访问申请和审批
- 审批者范围、禁止自批、时长与策略上限
- SQL 工单、执行任务、结果跟踪与人工核对
- 审批事件、历史记录和通知触发

## 本模块不负责

- 不直接写 grants，由应用流程调用 M02 公开接口
- 不连接数据库执行 SQL，由 M10 执行
- 不直接发送邮件或企业微信消息

## 交付切片

- 首次交付：临时访问申请、审批、取消和授权联动。
- 后续交付：W4 与 M10 集成 SQL 工单和 unknown 人工核对。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `approval_scopes`
- `access_requests`
- `db_work_orders`
- `work_order_executions`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- AccessRequest.Submit/Approve/Reject/Cancel
- WorkOrder.Submit/Decide/RecordResult/Reconcile
- ApprovalScope.Check
- ApprovalUnitOfWork（应用层编排）

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- AccessRequestSubmitted
- AccessRequestReviewed
- WorkOrderApproved
- WorkOrderReconcileRequired

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M01 身份与组织](M01-identity.md)
- [M02 权限与访问策略](M02-authorization.md)
- [M03 资源目录与主机资产](M03-resource.md)
- [M08 审计与录像](M08-audit.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/access-request-api.ts](../../../src/api/access-request-api.ts)
- [src/api/db-work-order-api.ts](../../../src/api/db-work-order-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U23 SQL 审批后自动执行

- 阶段：W4；协作：M10、M08、M14。
- 功能要求：提交资产/数据库/SQL/原因，管理员决定后自动调度已批准内容，保存结果并通知申请人；代理不能因存在工单而任意放开写 SQL。
- 验收条件：审批事务与执行意图原子提交，重复审批不重复执行；SQL 变动重审；超时结果不明进入 unknown，DDL 隐式提交和部分成功单独记录。
- 来源：[数据库审计](https://www.next-terminal.com/zh/docs/usage/database.html)。

协作项：U15（主责 M08）、U36（主责 M14）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)
- [implementation/database-auth.md](../implementation/database-auth.md)

## 验收条件

- 重复审批只能生成一个授权或执行意图
- 取消已批准申请与撤销 grant 原子提交
- 执行参数改变必须重新审批
- SQL 结果不明不自动重做，人工核对留下审计

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
