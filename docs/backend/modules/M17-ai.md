# M17 AI 运维助手

在现有用户权限内提供可审批、可追溯的 AI 运维能力。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：智能扩展。首次交付批次：W4。目录：`backend/internal/modules/ai`。

## 开发范围

- 模型配置、对话、历史、分支、压缩与用量
- AI WebSocket、消息去重、队列与断线恢复
- 工具调用、参数快照、审批决定、取消和超时
- 工具适配器复用已有资产、会话、文件等业务能力

## 本模块不负责

- 不自行实现资产 CRUD、文件写入或远程 shell
- 模型输出、主机备注不是授权来源
- 用户偏好 user_preferences 通过 M01 端口保存

## 交付切片

- 首次交付：模型/对话、只读工具、去重和用量控制。
- 后续交付：W4 经审批的写工具、取消、故障恢复和预算控制。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `ai_profiles`
- `ai_conversations`
- `ai_messages`
- `ai_turns`
- `ai_events`
- `ai_tool_executions`
- `ai_tool_approvals`
- `ai_usage_ledger`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Conversation.Submit/Load/Cancel
- ToolApproval.Decide
- ModelProvider.Generate
- AuthorizedTool.Execute
- Usage.Reserve/Record

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- AITurnStarted
- AIToolApprovalRequested
- AIToolCompleted
- AIUsageRecorded

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M01 身份与组织](M01-identity.md)
- [M02 权限与访问策略](M02-authorization.md)
- [M03 资源目录与主机资产](M03-resource.md)
- [M04 凭据与密钥](M04-vault.md)
- [M06 远程会话](M06-session.md)
- [M07 文件与共享存储](M07-files.md)
- [M08 审计与录像](M08-audit.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/ai-api.ts](../../../src/api/ai-api.ts)
- [src/api/ai-settings-api.ts](../../../src/api/ai-settings-api.ts)
- [src/api/ai-preferences-api.ts](../../../src/api/ai-preferences-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U35 AI 双重开关与审计助手

- 阶段：W4；协作：M03、M06、M08、M18。
- 功能要求：系统 LLM、Shell 助手/审计助手及单资产 AI 设置分别生效；模型、代理、提示词和执行策略类型化管理。
- 验收条件：系统或资产关闭时不能通过直接 API 执行工具；审计助手只读取已授权日志；模型密钥不进入配置列表和会话审计。
- 来源：[SSH 资产](https://www.next-terminal.com/zh/docs/usage/ssh.html)、[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。

协作项：U31（主责 M18）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 相同 clientMessageId 与 executionId 不重复执行
- 工具参数改变后原审批失效
- 用户撤权后待执行工具被拒绝
- 模型重试不会自动重放未知结果的写操作

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
