# M14 通知与消息投递

把业务事件可靠投递给符合条件的渠道与用户。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：流程与自动化。首次交付批次：W3。目录：`backend/internal/modules/notification`。

## 开发范围

- 邮件/企业微信等渠道、个人绑定和模板
- 通知规则、收件者解析、静默窗口与偏好
- 投递记录、去重、重试、失败诊断
- 审批资格查询和通知测试

## 本模块不负责

- 收件资格不是审批权限
- 不修改审批、资产或告警结果
- 身份验证码发放决策属于 M01，渠道发送可复用此模块端口

## 交付切片

- 首次交付：渠道、绑定、事件规则和可靠投递。
- 后续交付：随审批、告警和其他模块上线增加事件类型。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `notification_channels`
- `notification_bindings`
- `notification_rules`
- `notification_deliveries`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Notification.HandleEvent/TestChannel
- Recipients.Resolve
- Delivery.Send
- Template.Render

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- NotificationDelivered
- NotificationDeliveryFailed

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M01 身份与组织](M01-identity.md)
- [M04 凭据与密钥](M04-vault.md)
- [M08 审计与录像](M08-audit.md)
- [M18 系统设置与产品能力](M18-system.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/notification-api.ts](../../../src/api/notification-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U36 执行反馈与邮件配置

- 阶段：W3/W4；协作：M11、M18。
- 功能要求：SQL 工单完成/失败/结果待核对后通知申请人；SMTP TLS、账号、发件人和测试发送归渠道模块。
- 验收条件：发送失败可独立重试，不重做 SQL；不把数据库密码/完整秘密放进消息；配置测试与生产投递记录可区分。
- 来源：[数据库审计](https://www.next-terminal.com/zh/docs/usage/database.html)、[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。

协作项：U23（主责 M11）、U29（主责 M16）、U31（主责 M18）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 同一事件规则渠道收件者只创建一条投递
- 发送失败不回滚已成功业务事务
- 个人偏好和当前审批资格得到验证
- 重试语义和供应商不支持幂等时的重复可能性明确

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
