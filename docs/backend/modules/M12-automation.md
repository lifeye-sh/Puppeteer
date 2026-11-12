# M12 任务调度与脚本

统一安排何时执行获准业务任务，管理可复用脚本。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：流程与自动化。首次交付批次：W3。目录：`backend/internal/modules/automation`。

## 开发范围

- cron、时区、未来运行时间、启停与手动触发
- 任务重叠、misfire、执行历史与取消
- 系统/个人脚本片段与批量远程执行
- 通过注册 TaskHandler 调用轮换、续签、备份、清理等业务

## 本模块不负责

- 底层 jobs/job_runs 由 F00 拥有
- 不实现凭据轮换、备份、证书签发的状态机
- 业务处理器不能以 scheduler 身份绕过用户授权

## 交付切片

- 首次交付：脚本片段、远程任务、cron、幂等调度。
- 后续交付：各业务就绪后注册轮换/续签/备份处理器。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `schedules`
- `scheduled_runs`
- `snippets`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Schedule.Save/Trigger/NextRuns
- Snippet.Get/Save
- TaskHandler.Execute/Cancel
- RunHistory.Query

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- ScheduleTriggered
- ScheduledRunCompleted
- ScheduledRunMissed

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)
- [M02 权限与访问策略](M02-authorization.md)
- [M03 资源目录与主机资产](M03-resource.md)
- [M06 远程会话](M06-session.md)
- [M08 审计与录像](M08-audit.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/scheduled-task-api.ts](../../../src/api/scheduled-task-api.ts)
- [src/api/snippet-api.ts](../../../src/api/snippet-api.ts)
- [src/api/snippet-user-api.ts](../../../src/api/snippet-user-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U34 命令片段插入与任务执行分离

- 阶段：W3；协作：M06、M02。
- 功能要求：命令片段检索/使用只插入终端，批量任务另走受控执行用例。
- 验收条件：使用片段不自动附加换行执行；系统片段与个人片段范围明确；命令过滤和审计仍在真实执行入口生效。
- 来源：[SSH 资产](https://www.next-terminal.com/zh/docs/usage/ssh.html)。

协作项：U16（主责 M08）、U29（主责 M16）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 多调度器对同一计划时刻只创建一个任务
- 时区/DST 与 next-ten-runs 使用同一解释
- 任务执行前重新校验委托权限
- 未注册或被禁用的任务类型不能执行

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
