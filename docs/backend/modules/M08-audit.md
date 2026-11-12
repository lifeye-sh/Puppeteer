# M08 审计与录像

统一接收、保留、检索和回放业务与会话证据。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：审计与代理。首次交付批次：W0。目录：`backend/internal/modules/audit`。

## 开发范围

- 登录、管理、命令、文件、SQL、网站和系统事件审计
- 终端/图形录屏、片段索引、上传、转码与校验
- 审计检索、统计、录像播放与授权下载
- 保留、受限清理、法律保留与删除审计

## 本模块不负责

- 不拥有在线 sessions 表或业务实体
- 不主动决定资产访问授权
- 不将其他模块所有业务字段复制为第二份权威状态

## 交付切片

- 首次交付：管理/登录审计接口、事务写入及查询骨架。
- 后续交付：W2 终端录像；W3 图形录像/转码；W4 SQL 审计接入。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `recordings`
- `recording_segments`
- `audit_logins`
- `audit_operations`
- `audit_commands`
- `audit_files`
- `database_sql_logs`
- `website_access_logs`
- `asset_access_logs`

阶段物理表名映射（同一数据所有权，不另计逻辑表）：`login_logs` → `audit_logins`；`operation_logs` → `audit_operations`；`access_logs` → `asset_access_logs`；`recording_chunks` → `recording_segments`；`session_commands` → `audit_commands`。

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Audit.AppendInTx/Search
- Recorder.Open/Append/Finalize
- Recording.GetPlayback/RequestConvert
- AuditRetention.SchedulePurge
- AccessAudit.RecordAttempt/QueryTimeline

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- RecordingFinalized
- RecordingUploaded
- RecordingConversionFailed
- AuditStorageDegraded

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/login-log-api.ts](../../../src/api/login-log-api.ts)
- [src/api/operation-log-api.ts](../../../src/api/operation-log-api.ts)
- [src/api/session-command-api.ts](../../../src/api/session-command-api.ts)
- [src/api/exec-command-log-api.ts](../../../src/api/exec-command-log-api.ts)
- [src/api/fileystem-log-api.ts](../../../src/api/fileystem-log-api.ts)
- [src/api/database-sql-log-api.ts](../../../src/api/database-sql-log-api.ts)
- [src/api/access-log-api.ts](../../../src/api/access-log-api.ts)
- [src/api/system-event-api.ts](../../../src/api/system-event-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U15 资产访问日志与调查关联

- 阶段：W2/W4；协作：M06、M07、M09、M10、M11、M15。
- 功能要求：登录、后台变更、访问、命令、文件、SQL 和会话录像形成可关联的调查链；访问失败在会话尚未创建时也要留记录。
- 验收条件：按用户/资源/时间/结果定位并关联 trace/session/workOrder；Windows 内部复制不能伪造为浏览器文件日志；没有录像显示原因而非空成功回放。
- 来源：[日志审计](https://www.next-terminal.com/zh/docs/usage/audit.html)。

### U16 分类保留与录屏配置

- 阶段：W3；协作：M18、M12。
- 功能要求：分别配置会话、登录、任务、访问、SQL 日志的保留天数及录像开关，保留设置影响各自清理任务。
- 验收条件：对象删除与索引清理可重入；受保留锁保护的证据不删除；网站禁用访问日志明确降级范围，不连带关闭登录和管理操作审计。
- 来源：[日志审计](https://www.next-terminal.com/zh/docs/usage/audit.html)、[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。

协作项：U01（主责 M18）、U10（主责 M06）、U11（主责 M06）、U12（主责 M06）、U14（主责 M07）、U18（主责 M09）、U19（主责 M09）、U22（主责 M10）、U23（主责 M11）、U24（主责 M01）、U26（主责 M01）、U28（主责 M16）、U30（主责 M18）、U31（主责 M18）、U33（主责 M15）、U35（主责 M17）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-rdp.md](../implementation/native-rdp.md)
- [implementation/backup-restore.md](../implementation/backup-restore.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)
- [implementation/database-auth.md](../implementation/database-auth.md)

## 验收条件

- 业务与管理审计同事务，录像失败可见
- SQL/网站日志只有本模块持久化写入口
- 分区、录像保留和对象清理不会破坏引用
- 非审计授权用户不能通过下载链接绕过检查

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
