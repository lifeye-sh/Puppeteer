# M10 数据库访问代理

管理数据库资源及 MySQL/PostgreSQL 访问、语句控制和受控执行。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：审计与代理。首次交付批次：W4。目录：`backend/internal/modules/database`。

## 开发范围

- 数据库资产 CRUD、连接测试与目标数据库身份
- 原生客户端认证、协议代理和连接生命周期
- SQL 分类、prepared statement、事务与取消
- 接受已审批执行命令并报告确定或未知结果

## 本模块不负责

- 工单状态和审批决定由 M11 管理
- SQL 日志由 M08 保存
- 不能把任意 SELECT 字符串视为安全只读

## 交付切片

- 首次交付：数据库资产、原生协议访问与 SQL 审计。
- 后续交付：W4 工单执行、结果核对及客户端协议完整性验证。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `database_assets`
- `db_connections`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- DatabaseAsset.Save/GetFacts
- DatabaseAccess.Connect
- SQLExecutor.Execute/InspectResult
- SQLPolicy.Classify

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- DatabaseResourceChanged
- DatabaseConnectionClosed
- SQLExecutionCompleted
- SQLExecutionUnknown

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M02 权限与访问策略](M02-authorization.md)
- [M03 资源目录与主机资产](M03-resource.md)
- [M04 凭据与密钥](M04-vault.md)
- [M05 网关与网络连接](M05-connectivity.md)
- [M08 审计与录像](M08-audit.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/database-asset-api.ts](../../../src/api/database-asset-api.ts)
- [src/api/portal-api.ts](../../../src/api/portal-api.ts)
- [src/api/database-sql-log-api.ts](../../../src/api/database-sql-log-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U22 MySQL 代理兼容基线

- 阶段：W4；协作：M01、M02、M03、M05、M08。
- 功能要求：兼容 username@asset_name 与个人数据库密码；启停、监听地址和阻止 DML/DDL 配置；查询和客户端元数据查询经过分类后放行。
- 验收条件：真实 MySQL 客户端可握手、选择数据库、查询元数据和使用 prepared statement；变更受阻并产生审计；PG 是独立扩展适配器，不称官方使用文档已确认。
- 来源：[数据库审计](https://www.next-terminal.com/zh/docs/usage/database.html)、[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。

协作项：U15（主责 M08）、U21（主责 M01）、U23（主责 M11）、U31（主责 M18）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)
- [implementation/database-auth.md](../implementation/database-auth.md)

## 验收条件

- 资源授权与平台身份不被目标凭据替代
- MySQL 首批通过真实客户端协议测试；PG 扩展单独通过后才发布能力
- 无法确定远程提交结果时报告 unknown
- 不同用户与事务状态不能混用连接池

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
